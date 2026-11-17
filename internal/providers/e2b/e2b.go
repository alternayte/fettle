// Package e2b is the e2b computer provider: E2B sandboxes through the E2B REST API and
// envd, with an own small client (§10.4). The provider builds one template per size from
// the computer image on first use, sleeps a computer with pause and wakes it with connect.
//
// E2B reaches a sandbox port only through its HTTPS proxy, which ends TLS. agentd therefore
// serves a WebSocket bridge on BridgePort that pipes raw bytes to its own TLS listener, and
// the machine dials agentd through that bridge: Fettle's TLS runs inside the WebSocket, so
// the agentd certificate stays pinned end to end and E2B carries only ciphertext.
package e2b

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/alternayte/fettle/agentd"
	"github.com/alternayte/fettle/internal/computers"
)

const (
	// DefaultAPI is the E2B API of the E2B cloud.
	DefaultAPI = "https://api.e2b.app"
	// BridgePort is the guest port of the agentd WebSocket bridge.
	BridgePort = 7422
	agentdPort = 7421
	// sandboxTimeout is the E2B time to live in seconds. Fettle decides when a computer
	// sleeps (§10.2); at the end of the time to live E2B pauses the sandbox, and an open
	// agentd connection extends it.
	sandboxTimeout = 3600
	// TrafficHeader carries the traffic access token of a sandbox to its port URLs.
	TrafficHeader = "e2b-traffic-access-token"
)

// Start is the command that envd runs as root after create. agentd applies the network
// rules and drops to fettle (§10.5).
var Start = []string{"/usr/bin/tini", "--", "/usr/local/bin/fettle-agentd"}

// imageEnv repeats the ENV lines of the computer image, because envd starts processes with
// its own environment.
var imageEnv = map[string]string{
	"FETTLE_AGENTD_HOME":        "/home/fettle",
	"FETTLE_AGENTD_USER":        "fettle",
	"CHROME_BIN":                "/usr/bin/chromium",
	"FETTLE_BROWSER_NO_SANDBOX": "1",
	"LANG":                      "C.UTF-8",
}

// Provider runs computers as E2B sandboxes.
type Provider struct {
	key string
	// API is the base URL of the E2B API; New sets DefaultAPI.
	API string
	// Image is the computer image that the templates build from.
	Image string
	// Version names the templates, so a new release builds new ones.
	Version string
	imu     sync.Mutex
	// HTTP carries the API, envd and port traffic; nil uses the default client.
	HTTP *http.Client
	// PollEvery is the interval of the template build checks; zero is 2 seconds.
	PollEvery time.Duration
}

// New returns a provider for an E2B API key.
func New(apiKey, image, version string) *Provider {
	return &Provider{key: apiKey, API: DefaultAPI, Image: image, Version: version}
}

func (p *Provider) httpClient() *http.Client {
	if p.HTTP != nil {
		return p.HTTP
	}
	return http.DefaultClient
}

// Kind implements computers.Provider.
func (p *Provider) Kind() string { return "e2b" }

// Caps implements computers.Provider. E2B enforces an egress allowlist of host names at its
// edge, and agentd applies the same rules in the guest.
func (p *Provider) Caps() computers.Caps {
	return computers.Caps{Snapshot: true, NativeFork: true, NetworkAllowlist: true, Screen: true, Persistent: true, MaxVCPU: 4, MaxMemMiB: 8192}
}

// Ping checks that the E2B API answers and accepts the key (doctor, computer add).
func (p *Provider) Ping(ctx context.Context) error {
	var list []json.RawMessage
	err := p.call(ctx, http.MethodGet, "/v2/templates?limit=1", nil, &list)
	switch s := statusOf(err); {
	case err == nil:
		return nil
	case s == http.StatusUnauthorized || s == http.StatusForbidden:
		return fmt.Errorf("E2B refuses this API key: %w", err)
	case s != 0:
		return fmt.Errorf("the E2B API at %s answers with an error: %w", p.API, err)
	default:
		return fmt.Errorf("the E2B API at %s does not answer: %w", p.API, err)
	}
}

// Template is the template name of a size for this release.
func (p *Provider) Template(size computers.Size) string {
	v := strings.ToLower(strings.NewReplacer(".", "-", "+", "-", "_", "-").Replace(p.Version))
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

func (p *Provider) poll() time.Duration {
	if p.PollEvery > 0 {
		return p.PollEvery
	}
	return 2 * time.Second
}

func wait(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// Prepare builds one template per size from the computer image when it is missing or its
// last build failed, and waits until each is ready.
func (p *Provider) Prepare(ctx context.Context, progress func(computers.Progress)) error {
	report := func(pr computers.Progress) {
		if progress != nil {
			progress(pr)
		}
	}
	report(computers.Progress{Step: "e2b", State: "running", Detail: p.API})
	if err := p.Ping(ctx); err != nil {
		report(computers.Progress{Step: "e2b", State: "fail", Detail: err.Error()})
		return err
	}
	report(computers.Progress{Step: "e2b", State: "pass", Detail: p.API})
	for _, size := range []computers.Size{computers.SizeSmall, computers.SizeBrowser, computers.SizeDesktop} {
		step := "template " + string(size)
		if err := p.prepareTemplate(ctx, size, func(state, detail string) {
			report(computers.Progress{Step: step, State: state, Detail: detail})
		}); err != nil {
			report(computers.Progress{Step: step, State: "fail", Detail: err.Error()})
			return err
		}
	}
	return nil
}

func (p *Provider) prepareTemplate(ctx context.Context, size computers.Size, report func(state, detail string)) error {
	name := p.Template(size)
	for {
		status, err := p.templateStatus(ctx, name)
		if err != nil {
			return err
		}
		switch status {
		case "ready":
			report("pass", name)
			return nil
		case "building", "waiting":
			// Another Fettle process, or an earlier start, builds it.
			report("running", "building "+name)
			if err := wait(ctx, p.poll()); err != nil {
				return err
			}
			continue
		}
		// Missing, or the last build failed: build it now.
		report("running", "building "+name+" from "+p.image())
		return p.build(ctx, name, size)
	}
}

// templateStatus returns the build status of a named template, or "" when it is missing.
func (p *Provider) templateStatus(ctx context.Context, name string) (string, error) {
	var a templateAlias
	err := p.call(ctx, http.MethodGet, "/templates/aliases/"+esc(name), nil, &a)
	if statusOf(err) == http.StatusNotFound {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("look up template %s: %w", name, err)
	}
	var t templateWithBuilds
	if err := p.call(ctx, http.MethodGet, "/templates/"+esc(a.TemplateID)+"?limit=1", nil, &t); err != nil {
		return "", fmt.Errorf("read template %s: %w", name, err)
	}
	return t.BuildStatus, nil
}

// build requests a template build from the computer image and waits for its result. The
// template has no start command: envd starts agentd after create with the computer's own
// token and key pair, which the template snapshot must not hold.
func (p *Provider) build(ctx context.Context, name string, size computers.Size) error {
	spec := computers.Sizes[size]
	var res templateBuildResponse
	if err := p.call(ctx, http.MethodPost, "/v3/templates", templateBuildRequest{Name: name, CPUCount: spec.VCPU, MemoryMB: spec.MemMiB}, &res); err != nil {
		return fmt.Errorf("build template %s: %w", name, err)
	}
	if err := p.call(ctx, http.MethodPost, "/v2/templates/"+esc(res.TemplateID)+"/builds/"+esc(res.BuildID), templateBuildStart{FromImage: p.image()}, nil); err != nil {
		return fmt.Errorf("start the build of template %s: %w", name, err)
	}
	for {
		var info templateBuildInfo
		if err := p.call(ctx, http.MethodGet, "/templates/"+esc(res.TemplateID)+"/builds/"+esc(res.BuildID)+"/status?limit=0", nil, &info); err != nil {
			return fmt.Errorf("read the build of template %s: %w", name, err)
		}
		switch info.Status {
		case "ready":
			return nil
		case "error":
			msg := "the build failed"
			if info.Reason != nil && info.Reason.Message != "" {
				msg = info.Reason.Message
				if info.Reason.Step != "" {
					msg += " (step " + info.Reason.Step + ")"
				}
			}
			return fmt.Errorf("template %s from the image %s: %s. %s", name, p.image(), msg, computers.ImageHint)
		}
		if err := wait(ctx, p.poll()); err != nil {
			return err
		}
	}
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

// Create starts a sandbox of the zone's size and starts agentd in it through envd. An
// allowlist zone gets its hosts as the E2B egress allowlist; agentd applies the same rules in
// the guest (§10.5). The port URLs need the traffic access token of the sandbox.
func (p *Provider) Create(ctx context.Context, s computers.Spec) (computers.Machine, error) {
	size := s.Size
	if _, ok := computers.Sizes[size]; !ok {
		size = computers.SizeBrowser
	}
	tmpl := p.Template(size)
	if s.Image != "" && s.Image != p.image() {
		tmpl = s.Image // a provider template named by the caller
	}
	private := false
	nw := &networkConfig{AllowPublicTraffic: &private}
	if hosts, ok := allowHosts(s.Env); ok {
		// Allowed entries take precedence over denied ones; with no hosts nothing is allowed.
		nw.AllowOut, nw.DenyOut = hosts, []string{"0.0.0.0/0"}
	}
	var sb sandbox
	if err := p.call(ctx, http.MethodPost, "/v2/sandboxes", newSandbox{
		TemplateID: tmpl, Timeout: sandboxTimeout, AutoPause: true, Network: nw,
		Metadata: map[string]string{"fettle_zone": s.ZoneID},
	}, &sb); err != nil {
		return nil, fmt.Errorf("create sandbox: %w", err)
	}
	m := &machine{p: p, id: sb.SandboxID}
	env := make(map[string]string, len(s.Env)+len(imageEnv)+2)
	for k, v := range imageEnv {
		env[k] = v
	}
	for k, v := range s.Env {
		env[k] = v
	}
	env["FETTLE_AGENTD_WS_ADDR"] = fmt.Sprintf("0.0.0.0:%d", BridgePort)
	if len(s.ExtraHosts) > 0 {
		hosts := make([]string, 0, len(s.ExtraHosts))
		for h, ip := range s.ExtraHosts {
			hosts = append(hosts, h+"="+ip)
		}
		sort.Strings(hosts)
		env["FETTLE_AGENTD_HOSTS"] = strings.Join(hosts, ",")
	}
	if err := p.startProcess(ctx, &sb, "fettle-agentd", Start, env); err != nil {
		_ = m.Destroy(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("start agentd: %w", err)
	}
	return m, nil
}

// Attach finds an existing sandbox.
func (p *Provider) Attach(ctx context.Context, ref string) (computers.Machine, error) {
	if _, err := p.State(ctx, ref); err != nil {
		return nil, err
	}
	return &machine{p: p, id: ref}, nil
}

// State returns the E2B state of a sandbox: running or paused.
func (p *Provider) State(ctx context.Context, ref string) (string, error) {
	var d sandboxDetail
	if err := p.call(ctx, http.MethodGet, "/sandboxes/"+esc(ref), nil, &d); err != nil {
		return "", gone(err)
	}
	return d.State, nil
}

func gone(err error) error {
	if statusOf(err) == http.StatusNotFound {
		return computers.ErrGone
	}
	return err
}

func domainOf(d *string) string {
	if d == nil || *d == "" {
		return "e2b.app"
	}
	return *d
}

// portURL is the proxy URL of a sandbox port.
func (p *Provider) portURL(scheme string, port int, id, domain string) string {
	return fmt.Sprintf("%s://%d-%s.%s", scheme, port, id, domain)
}

type machine struct {
	p  *Provider
	id string
}

func (m *machine) Ref() string { return m.id }

// AgentdURL names agentd for the client; DialAgentd carries the connection.
func (m *machine) AgentdURL(ctx context.Context) (string, error) {
	if _, err := m.p.State(ctx, m.id); err != nil {
		return "", err
	}
	return fmt.Sprintf("https://%s.e2b:%d", m.id, agentdPort), nil
}

// connect resumes a paused sandbox, extends its time to live and returns its domain and
// traffic access token.
func (m *machine) connect(ctx context.Context) (*sandbox, error) {
	var sb sandbox
	if err := m.p.call(ctx, http.MethodPost, "/v2/sandboxes/"+esc(m.id)+"/connect", connectSandbox{Timeout: sandboxTimeout}, &sb); err != nil {
		return nil, gone(err)
	}
	return &sb, nil
}

// DialAgentd opens a WebSocket to the agentd bridge through the E2B port URL. The agentd
// client runs its pinned TLS inside it. A paused sandbox resumes first.
func (m *machine) DialAgentd(ctx context.Context, _, _ string) (net.Conn, error) {
	sb, err := m.connect(ctx)
	if err != nil {
		return nil, err
	}
	hdr := http.Header{}
	if sb.TrafficAccessToken != nil && *sb.TrafficAccessToken != "" {
		hdr.Set(TrafficHeader, *sb.TrafficAccessToken)
	}
	u := m.p.portURL("wss", BridgePort, m.id, domainOf(sb.Domain)) + agentd.BridgePath
	conn, resp, err := websocket.Dial(ctx, u, &websocket.DialOptions{HTTPHeader: hdr, HTTPClient: m.p.HTTP})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("e2b bridge to agentd: %w", err)
	}
	conn.SetReadLimit(-1)
	// The connection outlives the dial context: the HTTP transport reuses it.
	bg := context.WithoutCancel(ctx)
	kc := &keptConn{Conn: websocket.NetConn(bg, conn, websocket.MessageBinary), stop: make(chan struct{})}
	go kc.keep(bg, m)
	return kc, nil
}

// keptConn extends the time to live of the sandbox while Fettle holds a connection to agentd.
// The agentd client closes idle connections, so an idle computer still reaches its timeout.
type keptConn struct {
	net.Conn
	once sync.Once
	stop chan struct{}
}

func (c *keptConn) Close() error {
	c.once.Do(func() { close(c.stop) })
	return c.Conn.Close()
}

func (c *keptConn) keep(ctx context.Context, m *machine) {
	t := time.NewTicker(sandboxTimeout * time.Second / 4)
	defer t.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-t.C:
			cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			_ = m.p.call(cctx, http.MethodPost, "/sandboxes/"+esc(m.id)+"/timeout", timeoutRequest{Timeout: sandboxTimeout}, nil)
			cancel()
		}
	}
}

// Sleep pauses the sandbox with its memory; it keeps its processes, memory and disk (§10.2).
func (m *machine) Sleep(ctx context.Context) error {
	for attempt := 0; ; attempt++ {
		err := m.p.call(ctx, http.MethodPost, "/sandboxes/"+esc(m.id)+"/pause", map[string]any{}, nil)
		switch statusOf(err) {
		case 0:
			return err
		case http.StatusConflict:
			// The sandbox is already paused.
			return nil
		case http.StatusServiceUnavailable:
			// E2B cannot pause it right now and leaves it running.
			if attempt < 5 {
				if werr := wait(ctx, time.Duration(attempt+1)*time.Second); werr != nil {
					return werr
				}
				continue
			}
		}
		return gone(err)
	}
}

// Resume resumes a paused sandbox from its memory snapshot.
func (m *machine) Resume(ctx context.Context) error {
	_, err := m.connect(ctx)
	return err
}

// Fork makes a native copy of the running sandbox: memory, processes and disk. The copy runs
// the same agentd with the same token and certificate.
func (m *machine) Fork(ctx context.Context) (computers.Machine, error) {
	var res []forkResult
	if err := m.p.call(ctx, http.MethodPost, "/sandboxes/"+esc(m.id)+"/fork", forkRequest{Timeout: sandboxTimeout, Count: 1}, &res); err != nil {
		return nil, gone(err)
	}
	if len(res) != 1 {
		return nil, errors.New("e2b returned no fork")
	}
	if res[0].Error != nil {
		return nil, fmt.Errorf("e2b fork: %d: %s", res[0].Error.Code, res[0].Error.Message)
	}
	if res[0].Sandbox == nil || res[0].Sandbox.SandboxID == "" {
		return nil, errors.New("e2b returned no fork")
	}
	return &machine{p: m.p, id: res[0].Sandbox.SandboxID}, nil
}

// Destroy kills the sandbox.
func (m *machine) Destroy(ctx context.Context) error {
	err := m.p.call(ctx, http.MethodDelete, "/sandboxes/"+esc(m.id), nil, nil)
	if statusOf(err) == http.StatusNotFound {
		return nil
	}
	return err
}

// Stats returns the last metric entry of the sandbox.
func (m *machine) Stats(ctx context.Context) (computers.Stats, error) {
	var ms []sandboxMetric
	if err := m.p.call(ctx, http.MethodGet, "/sandboxes/"+esc(m.id)+"/metrics", nil, &ms); err != nil {
		return computers.Stats{}, gone(err)
	}
	if len(ms) == 0 {
		return computers.Stats{}, nil
	}
	last := ms[0]
	for _, x := range ms[1:] {
		if x.TimestampUnix >= last.TimestampUnix {
			last = x
		}
	}
	return computers.Stats{CPUPercent: last.CPUUsedPct, MemMiB: int(last.MemUsed >> 20), DiskMiB: int(last.DiskUsed >> 20)}, nil
}
