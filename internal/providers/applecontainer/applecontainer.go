// Package applecontainer runs computers as lightweight VMs with Apple container 1.x on
// macOS 26 on Apple silicon (§10.4). Fettle installs the pinned, signed runtime into its data
// folder without sudo when it is missing (REQ-CMP-006), and drives it through its CLI.
package applecontainer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alternayte/fettle/internal/computers"
)

// The pinned runtime (D-152).
const (
	Version       = "1.6.1"
	PackageURL    = "https://github.com/apple/container/releases/download/1.6.1/container-1.6.1-installer-signed.pkg"
	PackageSHA256 = "cd47846819f06478d067060efe21e516b562ac0a9af8eecad6d5fea0b556b200"
	// TeamID is Apple's Developer ID team that signs the package.
	TeamID = "UPBK2H6LZM"
)

const agentdPort = 7421

// ErrHost means this computer cannot run Apple container.
var ErrHost = errors.New("apple container needs macOS 26 on Apple silicon")

// Provider is the applecontainer provider.
type Provider struct {
	// Root holds the runtime: Root/install (binaries), Root/app (images and VMs).
	Root  string
	Image string
	// HTTP downloads the runtime package.
	HTTP *http.Client
	mu   sync.Mutex
}

// New returns the provider with its runtime under root.
func New(root, image string) *Provider {
	return &Provider{Root: root, Image: image, HTTP: &http.Client{Timeout: 30 * time.Minute}}
}

// Kind implements computers.Provider.
func (p *Provider) Kind() string { return "applecontainer" }

// Caps implements computers.Provider.
func (p *Provider) Caps() computers.Caps {
	return computers.Caps{NetworkAllowlist: true, Screen: true, Persistent: true, MaxVCPU: 4, MaxMemMiB: 8192}
}

// Supported reports whether this host can run Apple container (C-12).
func Supported() error {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return ErrHost
	}
	out, err := exec.Command("sw_vers", "-productVersion").Output()
	if err != nil {
		return ErrHost
	}
	major, _ := strconv.Atoi(strings.SplitN(strings.TrimSpace(string(out)), ".", 2)[0])
	if major < 26 {
		return ErrHost
	}
	return nil
}

func (p *Provider) bin() string     { return filepath.Join(p.Root, "install", "bin", "container") }
func (p *Provider) install() string { return filepath.Join(p.Root, "install") }
func (p *Provider) app() string     { return filepath.Join(p.Root, "app") }

// cli runs the container CLI and returns its standard output.
func (p *Provider) cli(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, p.bin(), args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return out, fmt.Errorf("container %s: %s", args[0], msg)
	}
	return out, nil
}

// Ping checks that the runtime answers (doctor).
func (p *Provider) Ping(ctx context.Context) error {
	if err := Supported(); err != nil {
		return err
	}
	if _, err := os.Stat(p.bin()); err != nil {
		return fmt.Errorf("the Apple container runtime is not installed yet; Fettle installs it when it prepares")
	}
	out, err := p.cli(ctx, "system", "status")
	if err != nil || !strings.Contains(string(out), "running") {
		return fmt.Errorf("the Apple container service does not run")
	}
	return nil
}

// Prepare installs the pinned runtime when it is missing, starts its service and loads the
// computer image.
func (p *Provider) Prepare(ctx context.Context, progress func(computers.Progress)) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	report := func(pr computers.Progress) {
		if progress != nil {
			progress(pr)
		}
	}
	if err := Supported(); err != nil {
		report(computers.Progress{Step: "runtime", State: "fail", Detail: err.Error()})
		return err
	}
	if _, err := os.Stat(p.bin()); err != nil {
		report(computers.Progress{Step: "runtime", State: "running", Detail: "Apple container " + Version})
		if err := p.installRuntime(ctx, report); err != nil {
			report(computers.Progress{Step: "runtime", State: "fail", Detail: err.Error()})
			return err
		}
	}
	report(computers.Progress{Step: "runtime", State: "pass", Detail: "Apple container " + Version})
	out, err := p.cli(ctx, "system", "status")
	running := err == nil && strings.Contains(string(out), "running")
	if running && !p.ownsService(string(out)) {
		// The service of this user runs on other folders (another data folder, or a
		// deleted one). Its kernel and images are not ours, so it restarts on ours.
		report(computers.Progress{Step: "service", State: "running", Detail: "restart on this data folder"})
		if _, err := p.cli(ctx, "system", "stop"); err != nil {
			report(computers.Progress{Step: "service", State: "fail", Detail: err.Error()})
			return err
		}
		running = false
	}
	if !running {
		report(computers.Progress{Step: "service", State: "running"})
		if _, err := p.cli(ctx, "system", "start", "--install-root", p.install(), "--app-root", p.app(), "--enable-kernel-install", "--timeout", "120"); err != nil {
			report(computers.Progress{Step: "service", State: "fail", Detail: err.Error()})
			return err
		}
	}
	report(computers.Progress{Step: "service", State: "pass"})
	if _, err := p.cli(ctx, "image", "inspect", p.Image); err == nil {
		report(computers.Progress{Step: "image", State: "pass", Detail: p.Image})
		return nil
	}
	report(computers.Progress{Step: "image", State: "running", Detail: p.Image})
	if err := p.loadImage(ctx); err != nil {
		report(computers.Progress{Step: "image", State: "fail", Detail: err.Error()})
		return err
	}
	report(computers.Progress{Step: "image", State: "pass", Detail: p.Image})
	return nil
}

// ownsService reports whether the running service (system status output) uses this
// provider's app root.
func (p *Provider) ownsService(status string) bool {
	for _, line := range strings.Split(status, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == "paths.appRoot" {
			want, _ := filepath.EvalSymlinks(p.app())
			got, _ := filepath.EvalSymlinks(strings.TrimSuffix(f[1], "/"))
			return want != "" && want == got
		}
	}
	return false
}

// loadImage pulls the image from its registry, or, for a local build without a registry
// (fettle-computer:dev), copies it from Docker.
func (p *Provider) loadImage(ctx context.Context) error {
	if strings.Contains(strings.SplitN(p.Image, "/", 2)[0], ".") {
		_, err := p.cli(ctx, "image", "pull", p.Image)
		return err
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("the image %s is a local build and Docker is not here to copy it from", p.Image)
	}
	tmp, err := os.CreateTemp("", "fettle-image-*.tar")
	if err != nil {
		return err
	}
	_ = tmp.Close()
	defer func() { _ = os.Remove(tmp.Name()) }()
	if out, err := exec.CommandContext(ctx, "docker", "save", p.Image, "-o", tmp.Name()).CombinedOutput(); err != nil {
		return fmt.Errorf("docker save: %s", strings.TrimSpace(string(out)))
	}
	_, err = p.cli(ctx, "image", "load", "-i", tmp.Name())
	return err
}

// installRuntime downloads the pinned package, checks its checksum and Apple's signature,
// and unpacks its payload into the data folder. No step needs sudo.
func (p *Provider) installRuntime(ctx context.Context, report func(computers.Progress)) error {
	if err := os.MkdirAll(p.Root, 0o755); err != nil {
		return err
	}
	pkg := filepath.Join(p.Root, "container-"+Version+".pkg")
	if err := p.download(ctx, pkg, report); err != nil {
		return err
	}
	defer func() { _ = os.Remove(pkg) }()
	out, err := exec.CommandContext(ctx, "pkgutil", "--check-signature", pkg).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "Developer ID Installer: Apple Inc.") || !strings.Contains(string(out), "("+TeamID+")") {
		return errors.New("the Apple container package is not signed by Apple")
	}
	tmp, err := os.MkdirTemp(p.Root, "expand-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	x := filepath.Join(tmp, "x")
	if out, err := exec.CommandContext(ctx, "pkgutil", "--expand-full", pkg, x).CombinedOutput(); err != nil {
		return fmt.Errorf("unpack the package: %s", strings.TrimSpace(string(out)))
	}
	_ = os.RemoveAll(p.install())
	return os.Rename(filepath.Join(x, "Payload"), p.install())
}

func (p *Provider) download(ctx context.Context, dst string, report func(computers.Progress)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, PackageURL, nil)
	if err != nil {
		return err
	}
	resp, err := p.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("download Apple container: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download Apple container: status %d", resp.StatusCode)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	pr := &progressReader{r: resp.Body, total: resp.ContentLength, report: report}
	if _, err := io.Copy(io.MultiWriter(f, h), pr); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != PackageSHA256 {
		return errors.New("the Apple container package does not match the pinned checksum")
	}
	return nil
}

type progressReader struct {
	r      io.Reader
	n      int64
	total  int64
	last   int
	report func(computers.Progress)
}

func (r *progressReader) Read(b []byte) (int, error) {
	n, err := r.r.Read(b)
	r.n += int64(n)
	if r.total > 0 {
		if pct := int(r.n * 100 / r.total); pct != r.last {
			r.last = pct
			r.report(computers.Progress{Step: "runtime", State: "running", Percent: pct, Detail: fmt.Sprintf("%d of %d MB", r.n>>20, r.total>>20)})
		}
	}
	return n, err
}

func name(zoneID string) string {
	z := strings.ReplaceAll(zoneID, "-", "")
	if len(z) > 12 {
		z = z[:12]
	}
	return "fettle-" + z + "-" + strconv.FormatInt(time.Now().UnixNano()%1_000_000, 10)
}

// Create starts a VM for a zone. agentd listens on the VM address (D-152).
func (p *Provider) Create(ctx context.Context, s computers.Spec) (computers.Machine, error) {
	img := s.Image
	if img == "" {
		img = p.Image
	}
	size := computers.Sizes[s.Size]
	if size.VCPU == 0 {
		size = computers.Sizes[computers.SizeBrowser]
	}
	id := name(s.ZoneID)
	args := []string{"run", "-d", "--name", id, "--cpus", strconv.Itoa(size.VCPU), "--memory", strconv.Itoa(size.MemMiB) + "M",
		"--cap-add", "CAP_NET_ADMIN", "--shm-size", "512M", "-l", "fettle.computer=1", "-l", "fettle.zone=" + s.ZoneID}
	keys := make([]string, 0, len(s.Env))
	for k := range s.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "-e", k+"="+s.Env[k])
	}
	if len(s.ExtraHosts) > 0 {
		var hosts []string
		for h, ip := range s.ExtraHosts {
			hosts = append(hosts, h+"="+ip)
		}
		sort.Strings(hosts)
		args = append(args, "-e", "FETTLE_AGENTD_HOSTS="+strings.Join(hosts, ","))
	}
	args = append(args, img)
	if _, err := p.cli(ctx, args...); err != nil {
		_, _ = p.cli(context.WithoutCancel(ctx), "delete", "--force", id)
		return nil, err
	}
	return &machine{p: p, id: id}, nil
}

// Attach finds an existing VM.
func (p *Provider) Attach(ctx context.Context, ref string) (computers.Machine, error) {
	m := &machine{p: p, id: ref}
	if _, err := m.inspect(ctx); err != nil {
		return nil, err
	}
	return m, nil
}

type machine struct {
	p  *Provider
	id string
}

func (m *machine) Ref() string { return m.id }

type inspectOut struct {
	Status struct {
		State    string `json:"state"`
		Networks []struct {
			IPv4 string `json:"ipv4Address"`
		} `json:"networks"`
	} `json:"status"`
	// Older 1.x versions keep these at the top level.
	State    string `json:"state"`
	Networks []struct {
		Address string `json:"address"`
	} `json:"networks"`
}

func (m *machine) inspect(ctx context.Context) (inspectOut, error) {
	out, err := m.p.cli(ctx, "inspect", m.id)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			return inspectOut{}, computers.ErrGone
		}
		return inspectOut{}, err
	}
	var list []inspectOut
	if err := json.Unmarshal(out, &list); err != nil || len(list) == 0 {
		return inspectOut{}, computers.ErrGone
	}
	return list[0], nil
}

// AgentdURL returns the URL of agentd on the VM address.
func (m *machine) AgentdURL(ctx context.Context) (string, error) {
	in, err := m.inspect(ctx)
	if err != nil {
		return "", err
	}
	state := in.Status.State
	if state == "" {
		state = in.State
	}
	if state != "running" {
		return "", fmt.Errorf("the VM is %s", state)
	}
	addr := ""
	for _, n := range in.Status.Networks {
		if n.IPv4 != "" {
			addr = n.IPv4
		}
	}
	for _, n := range in.Networks {
		if addr == "" && n.Address != "" {
			addr = n.Address
		}
	}
	addr = strings.SplitN(addr, "/", 2)[0]
	if addr == "" {
		return "", errors.New("the VM has no address yet")
	}
	return fmt.Sprintf("https://%s:%d", addr, agentdPort), nil
}

// Sleep stops the VM; Apple container has no snapshot (§10.4).
func (m *machine) Sleep(ctx context.Context) error {
	if _, err := m.p.cli(ctx, "stop", m.id); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			return computers.ErrGone
		}
		return err
	}
	return nil
}

// Resume starts the VM.
func (m *machine) Resume(ctx context.Context) error {
	if _, err := m.p.cli(ctx, "start", m.id); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			return computers.ErrGone
		}
		return err
	}
	return nil
}

// Fork is a copy fork, which the computer manager does (§10.4).
func (m *machine) Fork(context.Context) (computers.Machine, error) {
	return nil, computers.ErrUnsupported
}

// Destroy removes the VM.
func (m *machine) Destroy(ctx context.Context) error {
	if _, err := m.p.cli(ctx, "delete", "--force", m.id); err != nil && !strings.Contains(strings.ToLower(err.Error()), "not found") {
		return err
	}
	return nil
}

// Stats returns the memory figure of the VM.
func (m *machine) Stats(ctx context.Context) (computers.Stats, error) {
	out, err := m.p.cli(ctx, "stats", "--no-stream", "--format", "json", m.id)
	if err != nil {
		return computers.Stats{}, err
	}
	var list []struct {
		CPUUsageUsec     int64 `json:"cpuUsageUsec"`
		MemoryUsageBytes int64 `json:"memoryUsageBytes"`
	}
	if json.Unmarshal(out, &list) != nil || len(list) == 0 {
		return computers.Stats{}, nil
	}
	return computers.Stats{MemMiB: int(list[0].MemoryUsageBytes >> 20)}, nil
}
