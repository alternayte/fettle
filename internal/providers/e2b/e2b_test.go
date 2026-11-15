package e2b_test

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/alternayte/fettle/agentd"
	"github.com/alternayte/fettle/agentd/client"
	"github.com/alternayte/fettle/internal/computers"
	fakee2b "github.com/alternayte/fettle/internal/fakes/e2b"
	"github.com/alternayte/fettle/internal/providers/e2b"
)

// inProcess runs each sandbox as an agentd in this test process behind its WebSocket bridge,
// so the provider, the fake proxy and the pinned TLS run without Docker.
type inProcess struct {
	t       *testing.T
	mu      sync.Mutex
	bridges map[string]*httptest.Server
	closers map[string][]func()
}

func (p *inProcess) Image(_ context.Context, ref string) error {
	if strings.Contains(ref, "missing") {
		return errors.New("manifest unknown: " + ref)
	}
	return nil
}

func (p *inProcess) Create(context.Context, string, string, int, int) error { return nil }

func (p *inProcess) Start(_ context.Context, id string, _ []string, env map[string]string) error {
	srv, err := agentd.New(agentd.Config{
		Token: env["FETTLE_AGENTD_TOKEN"], TLSCert: []byte(env["FETTLE_AGENTD_TLS_CERT"]), TLSKey: []byte(env["FETTLE_AGENTD_TLS_KEY"]),
		Home: p.t.TempDir(), User: "fettle", Network: agentd.NetworkPolicy{Mode: agentd.NetworkOpen}, Version: "e2b-" + id,
	}, agentd.NetworkState{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		return err
	}
	pair, err := tls.X509KeyPair([]byte(env["FETTLE_AGENTD_TLS_CERT"]), []byte(env["FETTLE_AGENTD_TLS_KEY"]))
	if err != nil {
		return err
	}
	ts := httptest.NewUnstartedServer(srv.Handler())
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS13}
	ts.StartTLS()
	bridge := httptest.NewServer(agentd.BridgeHandler(ts.Listener.Addr().String(), nil))
	p.mu.Lock()
	p.bridges[id] = bridge
	p.closers[id] = append(p.closers[id], bridge.Close, ts.Close, func() { _ = srv.Close() })
	p.mu.Unlock()
	return nil
}

func (p *inProcess) Pause(context.Context, string) error        { return nil }
func (p *inProcess) Unpause(context.Context, string) error      { return nil }
func (p *inProcess) Fork(context.Context, string, string) error { return nil }

func (p *inProcess) Remove(_ context.Context, id string) error {
	p.mu.Lock()
	fs := p.closers[id]
	delete(p.closers, id)
	delete(p.bridges, id)
	p.mu.Unlock()
	for _, f := range fs {
		f()
	}
	return nil
}

func (p *inProcess) Addr(_ context.Context, id string, port int) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	b := p.bridges[id]
	if b == nil || port != e2b.BridgePort {
		return "", fakee2b.ErrNoPort
	}
	return b.Listener.Addr().String(), nil
}

func start(t *testing.T) (*fakee2b.Server, *e2b.Provider) {
	t.Helper()
	fake, err := fakee2b.New("acme-e2b-key", &inProcess{t: t, bridges: map[string]*httptest.Server{}, closers: map[string][]func(){}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fake.Close)
	p := e2b.New("acme-e2b-key", "ghcr.io/alternayte/fettle-computer:1.2.3", "v1.2.3")
	p.API, p.HTTP, p.PollEvery = fake.URL, fake.Client(), 10*time.Millisecond
	return fake, p
}

func spec(t *testing.T, network string) (computers.Spec, string, []byte) {
	t.Helper()
	cert, key, err := agentd.GenerateCert([]string{"fettle-computer"})
	if err != nil {
		t.Fatal(err)
	}
	return computers.Spec{ZoneID: "zone-acme", Size: computers.SizeSmall, ExtraHosts: map[string]string{"docs.test": "192.0.2.1"}, Env: map[string]string{
		"FETTLE_AGENTD_TOKEN": "acme-agentd-token", "FETTLE_AGENTD_TLS_CERT": string(cert), "FETTLE_AGENTD_TLS_KEY": string(key), "FETTLE_AGENTD_NETWORK": network,
	}}, "acme-agentd-token", cert
}

func health(t *testing.T, m computers.Machine, token string, cert []byte) (client.Health, error) {
	t.Helper()
	url, err := m.AgentdURL(t.Context())
	if err != nil {
		return client.Health{}, err
	}
	cl, err := client.New(url, token, cert, computers.DialOf(m))
	if err != nil {
		t.Fatal(err)
	}
	return cl.Health(t.Context())
}

// dialStatus opens a WebSocket and returns the HTTP status of a refused upgrade.
func dialStatus(ctx context.Context, url string, hc *http.Client, hdr http.Header) (int, error) {
	conn, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: hc, HTTPHeader: hdr})
	if conn != nil {
		_ = conn.CloseNow()
	}
	if resp == nil {
		return 0, err
	}
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
	return resp.StatusCode, err
}

// The agentd client pins the guest certificate through the E2B proxy: the proxy ends only
// the outer TLS, and Fettle's TLS runs inside the WebSocket to the agentd bridge.
func TestE2BBridgeCarriesPinnedTLS(t *testing.T) {
	fake, p := start(t)
	ctx := t.Context()
	for range 2 {
		if err := p.Prepare(ctx, nil); err != nil {
			t.Fatalf("prepare: %v", err)
		}
	}
	for _, size := range []computers.Size{computers.SizeSmall, computers.SizeBrowser, computers.SizeDesktop} {
		name := p.Template(size)
		if n := fake.Builds(name); n != 1 {
			t.Fatalf("template %s built %d times, want once", name, n)
		}
		if img := fake.BuildImage(name); img != p.Image {
			t.Fatalf("template %s builds from %q", name, img)
		}
	}
	if p.Template(computers.SizeSmall) != "fettle-v1-2-3-small" {
		t.Fatalf("template name %q", p.Template(computers.SizeSmall))
	}

	s, token, cert := spec(t, `{"mode":"allowlist","allow_hosts":["docs.acme.test","*.acme.test"]}`)
	m, err := p.Create(ctx, s)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	info, _ := fake.Sandbox(m.Ref())
	if info.Public || info.TrafficToken == "" || !info.AutoPause || info.Metadata["fettle_zone"] != "zone-acme" {
		t.Fatalf("sandbox settings: %+v", info)
	}
	if !slices.Equal(info.AllowOut, []string{"docs.acme.test", "*.acme.test"}) || !slices.Equal(info.DenyOut, []string{"0.0.0.0/0"}) {
		t.Fatalf("egress allowlist: allow %v deny %v", info.AllowOut, info.DenyOut)
	}
	if !slices.Equal(info.Cmd, e2b.Start) || info.Env["FETTLE_AGENTD_WS_ADDR"] != "0.0.0.0:7422" ||
		info.Env["FETTLE_AGENTD_HOME"] != "/home/fettle" || info.Env["FETTLE_AGENTD_HOSTS"] != "docs.test=192.0.2.1" {
		t.Fatalf("agentd start: %v %v", info.Cmd, info.Env)
	}

	h, err := health(t, m, token, cert)
	if err != nil || h.Version != "e2b-"+m.Ref() {
		t.Fatalf("health through the bridge: %+v %v", h, err)
	}
	if fake.PortHits(e2b.BridgePort) == 0 {
		t.Fatal("the client did not use the port URL of the bridge")
	}
	other, _, _ := agentd.GenerateCert([]string{"x"})
	if _, err := health(t, m, token, other); err == nil || !strings.Contains(err.Error(), "pinned") {
		t.Fatalf("a client pinned to another certificate connected: %v", err)
	}
	if _, err := health(t, m, "wrong", cert); !errors.Is(err, client.ErrUnauthorized) {
		t.Fatalf("a wrong computer token: %v", err)
	}

	// The port URL refuses a request without the traffic access token, and a paused sandbox.
	bridgeURL := "wss://7422-" + m.Ref() + "." + fakee2b.Domain + agentd.BridgePath
	if code, err := dialStatus(ctx, bridgeURL, p.HTTP, nil); code != http.StatusForbidden {
		t.Fatalf("a bridge request without the traffic token: %d %v", code, err)
	}
	if err := m.Sleep(ctx); err != nil {
		t.Fatalf("sleep: %v", err)
	}
	if st, _ := p.State(ctx, m.Ref()); st != "paused" {
		t.Fatalf("sleep left the sandbox %q, want paused", st)
	}
	if err := m.Sleep(ctx); err != nil {
		t.Fatalf("sleep of a paused sandbox: %v", err)
	}
	hdr := http.Header{e2b.TrafficHeader: {info.TrafficToken}}
	if code, err := dialStatus(ctx, bridgeURL, p.HTTP, hdr); code != http.StatusBadGateway {
		t.Fatalf("the bridge of a paused sandbox answered: %d %v", code, err)
	}
	if err := m.Resume(ctx); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if st, _ := p.State(ctx, m.Ref()); st != "running" {
		t.Fatalf("resume left the sandbox %q", st)
	}
	if _, err := health(t, m, token, cert); err != nil {
		t.Fatalf("health after resume: %v", err)
	}

	// A fork runs the same agentd, with the same token and certificate.
	f, err := m.Fork(ctx)
	if err != nil {
		t.Fatalf("fork: %v", err)
	}
	if f.Ref() == m.Ref() {
		t.Fatal("the fork has the ID of its parent")
	}
	if h, err := health(t, f, token, cert); err != nil || h.Version != "e2b-"+f.Ref() {
		t.Fatalf("health of the fork: %+v %v", h, err)
	}
	if st, err := f.Stats(ctx); err != nil || st.MemMiB != 256 {
		t.Fatalf("stats: %+v %v", st, err)
	}
	if err := f.Destroy(ctx); err != nil {
		t.Fatal(err)
	}

	ref := m.Ref()
	if err := m.Destroy(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Destroy(ctx); err != nil {
		t.Fatalf("a second destroy: %v", err)
	}
	if _, err := p.Attach(ctx, ref); !errors.Is(err, computers.ErrGone) {
		t.Fatalf("attach after destroy: %v", err)
	}
	if _, err := m.AgentdURL(ctx); !errors.Is(err, computers.ErrGone) {
		t.Fatalf("agentd URL after destroy: %v", err)
	}
	if err := m.Sleep(ctx); !errors.Is(err, computers.ErrGone) {
		t.Fatalf("sleep after destroy: %v", err)
	}
	if len(fake.Sandboxes()) != 0 {
		t.Fatalf("sandboxes left: %v", fake.Sandboxes())
	}
}

// An open zone has no egress limit at E2B; agentd still runs in open mode.
func TestE2BOpenZoneHasNoEgressLimit(t *testing.T) {
	fake, p := start(t)
	if err := p.Prepare(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	s, _, _ := spec(t, `{"mode":"open"}`)
	m, err := p.Create(t.Context(), s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background()) })
	info, _ := fake.Sandbox(m.Ref())
	if len(info.AllowOut) != 0 || len(info.DenyOut) != 0 || info.Public {
		t.Fatalf("open zone network: %+v", info)
	}
}

// A wrong key and a failed template build name the fault.
func TestE2BFaults(t *testing.T) {
	fake, p := start(t)
	wrong := e2b.New("acme-wrong-key", p.Image, "test")
	wrong.API, wrong.HTTP = fake.URL, fake.Client()
	if err := wrong.Ping(t.Context()); err == nil || !strings.Contains(err.Error(), "refuses this API key") {
		t.Fatalf("a wrong key: %v", err)
	}
	if _, err := p.Attach(t.Context(), "inone"); !errors.Is(err, computers.ErrGone) {
		t.Fatalf("a lost sandbox: %v", err)
	}
	p.Image = "ghcr.io/alternayte/missing:1"
	var steps []computers.Progress
	err := p.Prepare(t.Context(), func(pr computers.Progress) { steps = append(steps, pr) })
	if err == nil || !strings.Contains(err.Error(), "manifest unknown") {
		t.Fatalf("a missing image: %v", err)
	}
	if last := steps[len(steps)-1]; last.State != "fail" || last.Step != "template small" {
		t.Fatalf("last step %+v", last)
	}
	// A later preparation builds the template again.
	p.Image = "ghcr.io/alternayte/fettle-computer:1.2.3"
	if err := p.Prepare(t.Context(), nil); err != nil {
		t.Fatalf("prepare after a failed build: %v", err)
	}
	if n := fake.Builds(p.Template(computers.SizeSmall)); n != 2 {
		t.Fatalf("builds of the small template: %d", n)
	}
}
