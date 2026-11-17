// Package kiln is the kiln computer provider: Firecracker microVMs on a Kiln host through the
// Kiln Go SDK (§10.4). Fettle reaches agentd through a Kiln port stream: a raw TCP connection
// over a WebSocket, with Fettle's own TLS inside it, so the agentd certificate stays pinned
// end to end.
package kiln

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	kiln "github.com/alternayte/kiln/client"

	"github.com/alternayte/fettle/internal/computers"
)

const agentdPort = 7421

// Provider runs computers as Kiln sandboxes.
type Provider struct {
	c     *kiln.Client
	base  string
	token string
	// Image is the computer image that the templates build from.
	Image string
	// Version names the templates, so a new release builds new ones.
	Version string
	imu     sync.Mutex
	// HTTP carries the stream upgrade; nil uses the default client.
	HTTP *http.Client
}

// New returns a provider for a Kiln host at baseURL with an API token.
func New(baseURL, token, image, version string) *Provider {
	base := strings.TrimRight(baseURL, "/")
	return &Provider{c: kiln.New(base, token), base: base, token: token, Image: image, Version: version}
}

// Kind implements computers.Provider.
func (p *Provider) Kind() string { return "kiln" }

// Caps implements computers.Provider.
func (p *Provider) Caps() computers.Caps {
	return computers.Caps{Snapshot: true, NativeFork: true, NetworkAllowlist: true, Screen: true, Persistent: true, MaxVCPU: 4, MaxMemMiB: 8192}
}

// Ping checks that the Kiln host answers with this token (doctor, computer add).
func (p *Provider) Ping(ctx context.Context) error {
	if _, err := p.c.Health(ctx); err != nil {
		return fmt.Errorf("the Kiln host at %s does not answer: %w", p.base, err)
	}
	if _, err := p.c.Templates(ctx); err != nil {
		return fmt.Errorf("the Kiln host refuses this token: %w", err)
	}
	return nil
}

// template is the template name of a size for this release.
func (p *Provider) template(size computers.Size) string {
	v := strings.NewReplacer(".", "-", "+", "-", "_", "-").Replace(p.Version)
	if v == "" {
		v = "dev"
	}
	return "fettle-" + v + "-" + computers.ImageHash(p.image()) + "-" + string(size)
}

// SetImage changes the computer image. The next preparation builds templates from it.
func (p *Provider) SetImage(image string) {
	p.imu.Lock()
	defer p.imu.Unlock()
	p.Image = image
}

func (p *Provider) image() string {
	p.imu.Lock()
	defer p.imu.Unlock()
	return p.Image
}

// Prepare builds one template per size from the computer image when it is missing, and
// waits until each is ready.
func (p *Provider) Prepare(ctx context.Context, progress func(computers.Progress)) error {
	report := func(pr computers.Progress) {
		if progress != nil {
			progress(pr)
		}
	}
	report(computers.Progress{Step: "kiln", State: "running", Detail: p.base})
	if err := p.Ping(ctx); err != nil {
		report(computers.Progress{Step: "kiln", State: "fail", Detail: err.Error()})
		return err
	}
	report(computers.Progress{Step: "kiln", State: "pass", Detail: p.base})
	for _, size := range []computers.Size{computers.SizeSmall, computers.SizeBrowser, computers.SizeDesktop} {
		step := "template " + string(size)
		name := p.template(size)
		t, err := p.c.Template(ctx, name)
		switch {
		case kiln.NotFound(err):
			spec := computers.Sizes[size]
			req := kiln.TemplateRequest{
				Name: name, Image: p.image(), VCPUs: spec.VCPU, MemoryMB: spec.MemMiB, DiskMB: spec.DiskGiB * 1024,
				// Each sandbox gets the egress policy of its zone at create (open: observe mode,
				// allowlist: the zone's hosts); the template itself allows none.
				EgressAllow: []string{},
				// agentd starts as root, applies the network rules and drops to fettle (§10.5).
				Start: []string{"/usr/bin/tini", "--", "/usr/local/bin/fettle-agentd"},
				Port:  agentdPort,
			}
			report(computers.Progress{Step: step, State: "running", Detail: "building " + name})
			if err := p.c.CreateTemplate(ctx, req); err != nil && !kiln.Conflict(err) {
				report(computers.Progress{Step: step, State: "fail", Detail: err.Error()})
				return fmt.Errorf("build template %s from the image %s: %w. %s", name, p.image(), err, computers.ImageHint)
			}
		case err != nil:
			report(computers.Progress{Step: step, State: "fail", Detail: err.Error()})
			return err
		case t.State == "ready":
			report(computers.Progress{Step: step, State: "pass", Detail: name})
			continue
		}
		for {
			t, err = p.c.Template(ctx, name)
			if err != nil {
				report(computers.Progress{Step: step, State: "fail", Detail: err.Error()})
				return err
			}
			if t.State == "ready" {
				report(computers.Progress{Step: step, State: "pass", Detail: name})
				break
			}
			if t.State == "failed" || t.Error != "" {
				report(computers.Progress{Step: step, State: "fail", Detail: t.Error})
				return fmt.Errorf("template %s from the image %s failed: %s. %s", name, p.image(), t.Error, computers.ImageHint)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * time.Second):
			}
		}
	}
	return nil
}

// Create restores a sandbox of the zone's size. The start command gets the agentd token,
// the TLS key pair and the network rules as environment; Kiln keeps the values in guest
// memory only.
func (p *Provider) Create(ctx context.Context, s computers.Spec) (computers.Machine, error) {
	size := s.Size
	if _, ok := computers.Sizes[size]; !ok {
		size = computers.SizeBrowser
	}
	tmpl := p.template(size)
	if s.Image != "" && s.Image != p.image() {
		tmpl = s.Image // a provider template named by the caller
	}
	sb, err := p.c.CreateSandbox(ctx, kiln.SandboxRequest{
		Template: tmpl, Lifecycle: "persistent",
		// Fettle decides when a computer sleeps (§10.2); the Kiln idle deadline is a backstop.
		IdleSeconds: 24 * 3600,
		Metadata:    map[string]any{"fettle_zone": s.ZoneID},
		Env:         s.Env,
	})
	if err != nil {
		return nil, fmt.Errorf("create sandbox: %w", err)
	}
	m := &machine{p: p, id: sb.ID}
	// agentd also applies the zone's rules with nftables inside the guest (§10.5).
	if hosts, ok := allowHosts(s.Env); ok {
		_, err = p.c.SetEgress(ctx, sb.ID, hosts)
	} else {
		_, err = p.c.ObserveEgress(ctx, sb.ID)
	}
	if err != nil {
		_ = m.Destroy(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("set the egress policy: %w", err)
	}
	return m, nil
}

// allowHosts reads the hosts of an allowlist zone from the agentd network rules. ok is
// false for an open zone.
func allowHosts(env map[string]string) (hosts []string, ok bool) {
	var n struct {
		Mode       string   `json:"mode"`
		AllowHosts []string `json:"allow_hosts"`
	}
	if err := json.Unmarshal([]byte(env["FETTLE_AGENTD_NETWORK"]), &n); err != nil || n.Mode != "allowlist" {
		return nil, false
	}
	return append([]string{}, n.AllowHosts...), true
}

// Attach finds an existing sandbox.
func (p *Provider) Attach(ctx context.Context, ref string) (computers.Machine, error) {
	sb, err := p.c.Sandbox(ctx, ref)
	if kiln.NotFound(err) || err == nil && sb.DestroyedAt != nil {
		return nil, computers.ErrGone
	}
	if err != nil {
		return nil, err
	}
	return &machine{p: p, id: ref}, nil
}

type machine struct {
	p  *Provider
	id string
}

func (m *machine) Ref() string { return m.id }

func gone(err error) error {
	if kiln.NotFound(err) {
		return computers.ErrGone
	}
	return err
}

// AgentdURL names the stream of the sandbox; DialAgentd carries the connection.
func (m *machine) AgentdURL(ctx context.Context) (string, error) {
	sb, err := m.p.c.Sandbox(ctx, m.id)
	if err != nil {
		return "", gone(err)
	}
	if sb.DestroyedAt != nil {
		return "", computers.ErrGone
	}
	return fmt.Sprintf("https://%s.kiln:%d", m.id, agentdPort), nil
}

// DialAgentd opens a Kiln port stream to agentd. The agentd client runs its pinned TLS
// inside it. A sleeping sandbox wakes when the stream opens.
func (m *machine) DialAgentd(ctx context.Context, _, _ string) (net.Conn, error) {
	u, err := url.Parse(m.p.base)
	if err != nil {
		return nil, err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	}
	u.Path = strings.TrimRight(u.Path, "/") + fmt.Sprintf("/v1/sandboxes/%s/ports/%d/stream", url.PathEscape(m.id), agentdPort)
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+m.p.token)
	conn, resp, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{HTTPHeader: hdr, HTTPClient: m.p.HTTP})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return nil, computers.ErrGone
		}
		return nil, fmt.Errorf("kiln stream to agentd: %w", err)
	}
	conn.SetReadLimit(-1)
	// The connection outlives the dial context: the HTTP transport reuses it.
	return websocket.NetConn(context.WithoutCancel(ctx), conn, websocket.MessageBinary), nil
}

// Sleep snapshots the sandbox and stops it; it keeps its memory and disk (§10.2).
func (m *machine) Sleep(ctx context.Context) error {
	_, err := m.p.c.Snapshot(ctx, m.id, true)
	return gone(err)
}

// Resume wakes the sandbox from its snapshot. Any request wakes it; a status read waits
// until it runs.
func (m *machine) Resume(ctx context.Context) error {
	if _, err := m.p.c.Exec(ctx, m.id, kiln.ExecRequest{Cmd: []string{"true"}}); err != nil {
		return gone(err)
	}
	return nil
}

// Fork makes a native copy of the running sandbox: memory, processes and disk.
func (m *machine) Fork(ctx context.Context) (computers.Machine, error) {
	refs, err := m.p.c.Fork(ctx, m.id, 1, true)
	if err != nil {
		return nil, gone(err)
	}
	if len(refs) != 1 {
		return nil, errors.New("kiln returned no fork")
	}
	return &machine{p: m.p, id: refs[0].ID}, nil
}

// Destroy removes the sandbox.
func (m *machine) Destroy(ctx context.Context) error {
	err := m.p.c.DeleteSandbox(ctx, m.id)
	if kiln.NotFound(err) {
		return nil
	}
	return err
}

// Stats reports the state only: the Kiln API has no per-sandbox resource figures.
func (m *machine) Stats(ctx context.Context) (computers.Stats, error) {
	if _, err := m.p.c.Sandbox(ctx, m.id); err != nil {
		return computers.Stats{}, gone(err)
	}
	return computers.Stats{}, nil
}
