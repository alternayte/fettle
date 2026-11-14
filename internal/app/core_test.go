package app_test

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alternayte/fettle/internal/app"
	"github.com/alternayte/fettle/internal/apptest"
	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
	"github.com/alternayte/fettle/internal/models"
	"github.com/alternayte/fettle/internal/testenv"
)

// TestMain lets a test re-run this binary as a "serve" process (SCN-CORE-003).
func TestMain(m *testing.M) {
	if os.Getenv("FETTLE_TEST_SERVE") == "1" {
		serveChild()
		return
	}
	os.Exit(m.Run())
}

func serveChild() {
	cfg := app.ConfigFromEnv()
	cfg.Addr = "127.0.0.1:0"
	a, err := app.New(context.Background(), &cfg)
	if err != nil {
		fmt.Println("ERROR", err)
		os.Exit(1)
	}
	if err := a.Start(context.Background(), a.Handler()); err != nil {
		fmt.Println("ERROR", err)
		os.Exit(1)
	}
	fmt.Println("READY", a.Addr())
	_, _ = io.Copy(io.Discard, os.Stdin) // run until the parent closes stdin
	_ = a.Shutdown(context.Background())
}

func TestSCN_CORE_003_ConcurrentStartMigratesOnce(t *testing.T) {
	for _, d := range testenv.Dialects {
		t.Run(d, func(t *testing.T) {
			dir := t.TempDir()
			db := "sqlite:" + filepath.Join(dir, "fettle.db")
			if d == "postgres" {
				db = testenv.EmptyPostgresURL(t)
			}
			var wg sync.WaitGroup
			addrs := make([]string, 2)
			errs := make([]error, 2)
			for i := 0; i < 2; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					cmd := exec.Command(os.Args[0], "-test.run=^$")
					cmd.Env = append(os.Environ(), "FETTLE_TEST_SERVE=1", "FETTLE_DATA="+filepath.Join(dir, fmt.Sprint("data", i)), "FETTLE_DB="+db, "FETTLE_MASTER_KEY="+testenv.TestMasterKey, "FETTLE_NO_KEYCHAIN=1", "FETTLE_LOG_LEVEL=error")
					stdin, _ := cmd.StdinPipe()
					out, _ := cmd.StdoutPipe()
					if err := cmd.Start(); err != nil {
						errs[i] = err
						return
					}
					t.Cleanup(func() { _ = stdin.Close(); _ = cmd.Wait() })
					sc := bufio.NewScanner(out)
					for sc.Scan() {
						line := sc.Text()
						if strings.HasPrefix(line, "READY ") {
							addrs[i] = strings.TrimPrefix(line, "READY ")
							return
						}
						if strings.HasPrefix(line, "ERROR") {
							errs[i] = fmt.Errorf("%s", line)
							return
						}
					}
				}(i)
			}
			wg.Wait()
			for i := range addrs {
				if errs[i] != nil || addrs[i] == "" {
					t.Fatalf("process %d: %v", i, errs[i])
				}
				resp, err := http.Get("http://" + addrs[i] + "/readyz")
				if err != nil || resp.StatusCode != 200 {
					t.Fatalf("process %d not ready: %v", i, err)
				}
				resp.Body.Close()
			}
			// Each migration version was applied exactly once.
			cfg := &app.Config{DB: db, Data: filepath.Join(dir, "check")}
			if err := cfg.Normalize(); err != nil {
				t.Fatal(err)
			}
			d2, err := app.OpenDatabase(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer d2.Close()
			var total, distinct int
			if err := d2.Engine.QueryRow(context.Background(), "SELECT COUNT(*), COUNT(DISTINCT version) FROM drel_migrations").Scan(&total, &distinct); err != nil {
				t.Fatal(err)
			}
			if total == 0 || total != distinct {
				t.Fatalf("migrations: %d rows, %d versions", total, distinct)
			}
		})
	}
}

func TestSCN_CORE_004_LocalModeLoopbackAndToken(t *testing.T) {
	bad := &app.Config{Mode: app.ModeLocal, Addr: "0.0.0.0:7420", Data: t.TempDir()}
	if err := bad.Normalize(); err == nil {
		t.Fatal("local mode accepted a non-loopback address")
	}
	e := apptest.Start(t, "sqlite")
	host, _, _ := net.SplitHostPort(e.App.Addr())
	if host != "127.0.0.1" {
		t.Fatalf("listens on %s", host)
	}
	resp, err := http.Get(e.URL + "/api/v1/me")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("no token: %d", resp.StatusCode)
	}
	req, _ := http.NewRequest("GET", e.URL+"/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("wrong token: %d", resp.StatusCode)
	}
	if code, _ := e.Do("GET", "/me", nil, nil); code != 200 {
		t.Fatalf("with token: %d", code)
	}
	st, err := os.Stat(filepath.Join(e.Cfg.Data, "local.token"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("local.token mode %v %v", st.Mode().Perm(), err)
	}
	// The UI cookie also carries the token proof, and a cross-origin write with it is refused.
	resp, _ = http.Post(e.URL+"/api/v1/auth/local", "application/json", strings.NewReader(`{"token":"`+e.Token+`"}`))
	resp.Body.Close()
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		cookie = c
	}
	if cookie == nil || !cookie.HttpOnly || cookie.Value == e.Token {
		t.Fatalf("cookie %+v", cookie)
	}
	req, _ = http.NewRequest("PATCH", e.URL+"/api/v1/me", strings.NewReader(`{"name":"Ada"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://evil.test")
	req.AddCookie(cookie)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("cross-origin cookie write: %d", resp.StatusCode)
	}
}

func TestSCN_CORE_005_HealthReadyMetrics(t *testing.T) {
	e := apptest.Start(t, "sqlite")
	get := func(p string) (int, string) {
		resp, err := http.Get(e.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if c, _ := get("/healthz"); c != 200 {
		t.Fatalf("healthz %d", c)
	}
	if c, b := get("/readyz"); c != 200 {
		t.Fatalf("readyz %d %s", c, b)
	}
	c, b := get("/metrics")
	for _, fam := range []string{"fettle_runs", "fettle_model_tokens_total", "fettle_model_cost_usd_total", "fettle_computer_starts_total", "fettle_computer_active_seconds_total", "fettle_cards_total", "fettle_push_deliveries_total"} {
		if c != 200 || !strings.Contains(b, "\n"+fam) && !strings.HasPrefix(b, fam) {
			t.Fatalf("metrics lack %s", fam)
		}
	}
	objs := filepath.Join(e.Cfg.Data, "objects")
	if err := os.Chmod(objs, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(objs, 0o700) })
	if c, b := get("/readyz"); c != 503 || !strings.Contains(b, "object_store") {
		t.Fatalf("readyz with unwritable store: %d %s", c, b)
	}
}

// tlsFixture makes a private CA and a server certificate for fake-model.test.
func tlsFixture(t *testing.T) (caPEM []byte, cert tls.Certificate) {
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Fettle Test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true}
	caKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, _ := x509.ParseCertificate(caDER)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "fake-model.test"}, DNSNames: []string{"fake-model.test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	leafKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), tls.Certificate{Certificate: [][]byte{leafDER}, PrivateKey: leafKey}
}

// connectProxy is an HTTPS proxy that maps fake-model.test to target and counts tunnels.
func connectProxy(t *testing.T, target string) (string, *atomic.Int64) {
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || !strings.HasPrefix(r.Host, "fake-model.test:") {
			http.Error(w, "no", 403)
			return
		}
		n.Add(1)
		up, err := net.Dial("tcp", target)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		hj, _ := w.(http.Hijacker)
		w.WriteHeader(200)
		c, _, err := hj.Hijack()
		if err != nil {
			return
		}
		go func() { _, _ = io.Copy(up, c); up.Close() }()
		go func() { _, _ = io.Copy(c, up); c.Close() }()
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &n
}

func TestSCN_CORE_007_ProxyAndExtraCA(t *testing.T) {
	caPEM, cert := tlsFixture(t)
	fake, _ := fakemodel.New()
	ts := httptest.NewUnstartedServer(fake)
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	ts.StartTLS()
	t.Cleanup(ts.Close)
	_, port, _ := net.SplitHostPort(ts.Listener.Addr().String())
	proxyURL, tunnels := connectProxy(t, ts.Listener.Addr().String())
	t.Setenv("HTTPS_PROXY", proxyURL)
	t.Setenv("NO_PROXY", "")
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	_ = os.WriteFile(caFile, caPEM, 0o600)
	base := "https://fake-model.test:" + port + "/v1"

	hc, err := app.NewHTTPClient(caFile)
	if err != nil {
		t.Fatal(err)
	}
	s := models.Probe(context.Background(), hc, models.Conn{APIType: models.APIOpenAIChat, BaseURL: base}, "fake/large")
	for _, c := range s.Checks {
		if !c.Pass {
			t.Fatalf("check %s failed: %s", c.Key, c.Detail)
		}
	}
	if tunnels.Load() == 0 {
		t.Fatal("the calls did not go through the proxy")
	}
	plain, _ := app.NewHTTPClient("")
	s = models.Probe(context.Background(), plain, models.Conn{APIType: models.APIOpenAIChat, BaseURL: base}, "fake/large")
	if s.Checks[0].Pass || !strings.Contains(s.Checks[0].Detail, "certificate") {
		t.Fatalf("without the CA: %+v", s.Checks[0])
	}
}

func TestSCN_CORE_012_TelemetryOffByDefault(t *testing.T) {
	var mu sync.Mutex
	var bodies []map[string]any
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		mu.Lock()
		bodies = append(bodies, m)
		mu.Unlock()
	}))
	t.Cleanup(sink.Close)
	t.Setenv("FETTLE_TELEMETRY_URL", sink.URL)
	e := apptest.Start(t, "sqlite")
	var v struct {
		Enabled bool     `json:"enabled"`
		Fields  []string `json:"fields"`
	}
	e.MustDo("GET", "/admin/telemetry", nil, &v)
	if v.Enabled {
		t.Fatal("telemetry is on in a fresh instance")
	}
	time.Sleep(300 * time.Millisecond)
	if sent, _ := e.App.Telemetry.SendNow(context.Background()); sent {
		t.Fatal("telemetry sent while off")
	}
	mu.Lock()
	if len(bodies) != 0 {
		t.Fatalf("telemetry requests while off: %d", len(bodies))
	}
	mu.Unlock()
	e.MustDo("PUT", "/admin/telemetry", map[string]any{"enabled": true}, nil)
	if sent, err := e.App.Telemetry.SendNow(context.Background()); !sent || err != nil {
		t.Fatalf("telemetry did not send: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	allowed := map[string]bool{}
	for _, f := range v.Fields {
		allowed[f] = true
	}
	if len(bodies) != 1 || len(bodies[0]) != len(v.Fields) {
		t.Fatalf("payload %v", bodies)
	}
	for k := range bodies[0] {
		if !allowed[k] {
			t.Fatalf("payload has unlisted field %s", k)
		}
	}
}

func TestSCN_MOD_001_PresetsAndProviderCRUD(t *testing.T) {
	testenv.Each(t, func(t *testing.T, dialect string) {
		e := apptest.Start(t, dialect)
		var presets struct{ Presets []models.Preset }
		e.MustDo("GET", "/model-presets", nil, &presets)
		if len(presets.Presets) != 8 || presets.Presets[0].ID != "openrouter" {
			t.Fatalf("presets %+v", presets.Presets)
		}
		for _, p := range presets.Presets {
			if p.BaseURL == "" {
				continue // the generic preset needs a URL from the user
			}
			var out struct {
				ID        string `json:"id"`
				APIType   string `json:"api_type"`
				BaseURL   string `json:"base_url"`
				AuthStyle string `json:"auth_style"`
				HasKey    bool   `json:"has_key"`
				KeyLast4  string `json:"key_last4"`
			}
			e.MustDo("POST", e.SP("/model-providers"), map[string]any{"preset": p.ID, "key": "sk-preset-key-" + p.ID}, &out)
			if out.APIType != p.APIType || out.BaseURL != p.BaseURL || out.AuthStyle != p.AuthStyle || !out.HasKey || out.KeyLast4 == "" {
				t.Fatalf("preset %s filled %+v", p.ID, out)
			}
			e.MustDo("PATCH", e.SP("/model-providers/%s", out.ID), map[string]any{"name": p.Name + " edited", "api_type": p.APIType, "base_url": p.BaseURL, "auth_style": p.AuthStyle}, nil)
			e.MustDo("DELETE", e.SP("/model-providers/%s", out.ID), nil, nil)
			if c, _ := e.Do("PATCH", e.SP("/model-providers/%s", out.ID), map[string]any{"name": "x", "api_type": p.APIType, "base_url": p.BaseURL}, nil); c != 404 {
				t.Fatalf("deleted provider still answers: %d", c)
			}
		}
		// The API never returns a key.
		_, body := e.Do("GET", e.SP("/model-providers"), nil, nil)
		if strings.Contains(body, "sk-preset-key") {
			t.Fatal("the provider list returns a key")
		}
	})
}
