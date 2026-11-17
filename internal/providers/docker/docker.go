// Package docker is the docker computer provider: one container per computer through the
// Docker Engine API. It works with Docker Desktop, Docker Engine and Podman (§10.4).
package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/alternayte/fettle/internal/computers"
)

const agentdPort = "7421/tcp"

// Provider runs computers as containers.
type Provider struct {
	c *client.Client
	// Image is the computer image reference.
	Image string
	// Platform selects the image platform, empty for the host default.
	Platform string
	mu       sync.Mutex
}

// SetImage changes the computer image for the next preparation and machine.
func (p *Provider) SetImage(image string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Image = image
}

func (p *Provider) image() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.Image
}

// New connects to the Docker Engine from DOCKER_HOST or the default socket.
func New(image string) (*Provider, error) {
	c, err := client.New(client.FromEnv)
	if err != nil {
		return nil, err
	}
	return &Provider{c: c, Image: image}, nil
}

// Kind implements computers.Provider.
func (p *Provider) Kind() string { return "docker" }

// Caps implements computers.Provider.
func (p *Provider) Caps() computers.Caps {
	return computers.Caps{NetworkAllowlist: true, Screen: true, Persistent: true, MaxVCPU: 4, MaxMemMiB: 8192}
}

// Ping checks that the engine answers (doctor).
func (p *Provider) Ping(ctx context.Context) error {
	_, err := p.c.Ping(ctx, client.PingOptions{})
	if err != nil {
		return fmt.Errorf("docker does not answer: %w", err)
	}
	return nil
}

// Prepare pulls the computer image when it is missing.
func (p *Provider) Prepare(ctx context.Context, progress func(computers.Progress)) error {
	report := func(pr computers.Progress) {
		if progress != nil {
			progress(pr)
		}
	}
	report(computers.Progress{Step: "docker", State: "running"})
	if err := p.Ping(ctx); err != nil {
		report(computers.Progress{Step: "docker", State: "fail", Detail: err.Error()})
		return err
	}
	report(computers.Progress{Step: "docker", State: "pass"})
	img := p.image()
	if _, err := p.c.ImageInspect(ctx, img); err == nil {
		report(computers.Progress{Step: "image", State: "pass", Detail: img})
		return nil
	}
	report(computers.Progress{Step: "image", State: "running", Detail: img})
	fail := func(err error) error {
		err = computers.ImageError(img, err)
		report(computers.Progress{Step: "image", State: "fail", Detail: err.Error()})
		return err
	}
	rc, err := p.c.ImagePull(ctx, img, client.ImagePullOptions{})
	if err != nil {
		return fail(err)
	}
	defer rc.Close()
	dec := json.NewDecoder(rc)
	total, current := map[string]int64{}, map[string]int64{}
	for {
		var m struct {
			ID             string `json:"id"`
			Status         string `json:"status"`
			Error          string `json:"error"`
			ProgressDetail struct {
				Current int64 `json:"current"`
				Total   int64 `json:"total"`
			} `json:"progressDetail"`
		}
		if err := dec.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fail(err)
		}
		if m.Error != "" {
			return fail(errors.New(m.Error))
		}
		if m.ProgressDetail.Total > 0 && strings.HasPrefix(m.Status, "Downloading") {
			total[m.ID], current[m.ID] = m.ProgressDetail.Total, m.ProgressDetail.Current
			var t, c int64
			for k := range total {
				t += total[k]
				c += current[k]
			}
			report(computers.Progress{Step: "image", State: "running", Percent: int(c * 100 / max(t, 1)), Detail: computers.DownloadDetail(t), TotalMB: int(t >> 20)})
		}
	}
	report(computers.Progress{Step: "image", State: "pass", Detail: img})
	return nil
}

func containerName(zoneID string) string {
	return "fettle-" + strings.ReplaceAll(zoneID, "-", "")[:12] + "-" + fmt.Sprint(time.Now().UnixNano()%1_000_000)
}

// Create starts a container for a zone with agentd on a loopback port.
func (p *Provider) Create(ctx context.Context, s computers.Spec) (computers.Machine, error) {
	img := s.Image
	if img == "" {
		img = p.image()
	}
	size := computers.Sizes[s.Size]
	if size.VCPU == 0 {
		size = computers.Sizes[computers.SizeBrowser]
	}
	env := make([]string, 0, len(s.Env))
	for k, v := range s.Env {
		env = append(env, k+"="+v)
	}
	sort.Strings(env)
	var hosts []string
	for h, ip := range s.ExtraHosts {
		hosts = append(hosts, h+":"+ip)
	}
	sort.Strings(hosts)
	port := network.MustParsePort(agentdPort)
	cfg := &container.Config{
		Image:        img,
		Env:          env,
		ExposedPorts: network.PortSet{port: struct{}{}},
		Labels:       map[string]string{"fettle.zone": s.ZoneID, "fettle.computer": "1"},
	}
	hc := &container.HostConfig{
		PortBindings: network.PortMap{port: []network.PortBinding{{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: "0"}}},
		// agentd needs NET_ADMIN only for its boot step; it then drops to the fettle user.
		CapAdd:     []string{"NET_ADMIN"},
		ExtraHosts: hosts,
		Resources:  container.Resources{NanoCPUs: int64(size.VCPU) * 1e9, Memory: int64(size.MemMiB) << 20},
		ShmSize:    512 << 20,
		Init:       ptr(false),
	}
	res, err := p.c.ContainerCreate(ctx, client.ContainerCreateOptions{Config: cfg, HostConfig: hc, Name: containerName(s.ZoneID)})
	if err != nil {
		return nil, fmt.Errorf("create container: %w", err)
	}
	m := &machine{p: p, id: res.ID}
	if err := m.Resume(ctx); err != nil {
		_ = m.Destroy(context.WithoutCancel(ctx))
		return nil, err
	}
	return m, nil
}

func ptr[T any](v T) *T { return &v }

// Attach finds an existing container.
func (p *Provider) Attach(ctx context.Context, ref string) (computers.Machine, error) {
	if _, err := p.c.ContainerInspect(ctx, ref, client.ContainerInspectOptions{}); err != nil {
		if cerrdefs.IsNotFound(err) {
			return nil, computers.ErrGone
		}
		return nil, err
	}
	return &machine{p: p, id: ref}, nil
}

type machine struct {
	p  *Provider
	id string
}

func (m *machine) Ref() string { return m.id }

func (m *machine) inspect(ctx context.Context) (container.InspectResponse, error) {
	res, err := m.p.c.ContainerInspect(ctx, m.id, client.ContainerInspectOptions{})
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return container.InspectResponse{}, computers.ErrGone
		}
		return container.InspectResponse{}, err
	}
	return res.Container, nil
}

// AgentdURL returns the loopback URL of agentd's published port.
func (m *machine) AgentdURL(ctx context.Context) (string, error) {
	c, err := m.inspect(ctx)
	if err != nil {
		return "", err
	}
	if c.State == nil || !c.State.Running {
		return "", fmt.Errorf("the container is not running")
	}
	if c.NetworkSettings == nil {
		return "", fmt.Errorf("the container has no network settings")
	}
	for _, b := range c.NetworkSettings.Ports[network.MustParsePort(agentdPort)] {
		if b.HostPort != "" {
			return "https://127.0.0.1:" + b.HostPort, nil
		}
	}
	return "", fmt.Errorf("agentd port is not published")
}

// Sleep stops the container; docker has no snapshot.
func (m *machine) Sleep(ctx context.Context) error {
	t := 10
	_, err := m.p.c.ContainerStop(ctx, m.id, client.ContainerStopOptions{Timeout: &t})
	if cerrdefs.IsNotFound(err) {
		return computers.ErrGone
	}
	return err
}

// Resume starts the container.
func (m *machine) Resume(ctx context.Context) error {
	_, err := m.p.c.ContainerStart(ctx, m.id, client.ContainerStartOptions{})
	if cerrdefs.IsNotFound(err) {
		return computers.ErrGone
	}
	return err
}

// Fork is a copy fork, which the computer manager does (§10.4).
func (m *machine) Fork(context.Context) (computers.Machine, error) {
	return nil, computers.ErrUnsupported
}

// Destroy removes the container.
func (m *machine) Destroy(ctx context.Context) error {
	_, err := m.p.c.ContainerRemove(ctx, m.id, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
	if cerrdefs.IsNotFound(err) {
		return nil
	}
	return err
}

// Stats returns CPU and memory figures.
func (m *machine) Stats(ctx context.Context) (computers.Stats, error) {
	res, err := m.p.c.ContainerStats(ctx, m.id, client.ContainerStatsOptions{Stream: false})
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return computers.Stats{}, computers.ErrGone
		}
		return computers.Stats{}, err
	}
	defer res.Body.Close()
	var s container.StatsResponse
	if err := json.NewDecoder(res.Body).Decode(&s); err != nil {
		return computers.Stats{}, err
	}
	out := computers.Stats{MemMiB: int(s.MemoryStats.Usage >> 20)}
	cpuDelta := float64(s.CPUStats.CPUUsage.TotalUsage - s.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(s.CPUStats.SystemUsage - s.PreCPUStats.SystemUsage)
	if sysDelta > 0 {
		out.CPUPercent = cpuDelta / sysDelta * float64(max(s.CPUStats.OnlineCPUs, 1)) * 100
	}
	return out, nil
}
