package app_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alternayte/fettle/internal/app"
	"github.com/alternayte/fettle/internal/apptest"
)

func TestSCN_OPS_006_ComputerImageChoice(t *testing.T) {
	t.Setenv("FETTLE_COMPUTER_IMAGE", "")
	release := "ghcr.io/alternayte/fettle-computer:1.0.0-rc.4"
	cases := []struct {
		env, setting, ldflag string
		readEnv              bool
		want                 string
	}{
		// A build without the ldflag is a local build, whatever its version says.
		{"", "", "", true, "fettle-computer:dev"},
		// A pre-release is a release: the ldflag names its published image.
		{"", "", release, true, release},
		{"", "ghcr.io/acme/computer:7", release, true, "ghcr.io/acme/computer:7"},
		{"", "  ", release, true, release},
		{"ghcr.io/acme/computer:env", "ghcr.io/acme/computer:7", release, true, "ghcr.io/acme/computer:env"},
		// A pin to another release of this image is left over from an upgrade.
		{"", "ghcr.io/alternayte/fettle-computer:1.0.0-rc.3", release, true, release},
		{"", "ghcr.io/alternayte/fettle-computer@sha256:abc", release, true, release},
		{"", "ghcr.io/alternayte/fettle-computer:1.0.0-rc.3", "", true, "ghcr.io/alternayte/fettle-computer:1.0.0-rc.3"},
		// The desktop app does not read the variable.
		{"ghcr.io/alternayte/fettle-computer:1.0.0-rc.3", "", release, false, release},
		{"ghcr.io/acme/computer:env", "ghcr.io/acme/computer:7", release, false, "ghcr.io/acme/computer:7"},
	}
	for _, c := range cases {
		t.Setenv("FETTLE_COMPUTER_IMAGE", c.env)
		if got := app.ComputerImage(c.ldflag, c.setting, c.readEnv); got != c.want {
			t.Errorf("env %q setting %q ldflag %q readEnv %v: %q, want %q", c.env, c.setting, c.ldflag, c.readEnv, got, c.want)
		}
	}
}

type instanceSetting struct {
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	Default   json.RawMessage `json:"default"`
	IsDefault bool            `json:"is_default"`
	Secret    bool            `json:"secret"`
	Set       bool            `json:"set"`
	Locked    bool            `json:"locked"`
	Env       string          `json:"env"`
}

func instanceSettings(e *apptest.Env) map[string]instanceSetting {
	var out struct {
		Settings []instanceSetting `json:"settings"`
	}
	e.MustDo("GET", "/admin/settings", nil, &out)
	m := map[string]instanceSetting{}
	for _, s := range out.Settings {
		m[s.Key] = s
	}
	return m
}

func TestSCN_OPS_007_InstanceSettingsLockSecretsAndApplyAtOnce(t *testing.T) {
	for _, k := range []string{"FETTLE_COMPUTER_IMAGE", "HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(k, "")
	}
	e := apptest.Start(t, "sqlite", func(c *app.Config) { c.ComputerImage = "ghcr.io/alternayte/fettle-computer:1.0.0-rc.4" })
	s := instanceSettings(e)
	img := s["computers.image"]
	if string(img.Default) != `"ghcr.io/alternayte/fettle-computer:1.0.0-rc.4"` || !img.IsDefault || img.Locked || img.Env != "FETTLE_COMPUTER_IMAGE" {
		t.Fatalf("image setting: %+v", img)
	}
	if _, ok := s["computers.use_host_settings"]; ok {
		t.Fatal("a server that is not the desktop app shows the host settings option")
	}

	// A new image applies to the next computer, and an empty value resets it.
	e.MustDo("PUT", "/admin/settings/computers.image", map[string]any{"value": "ghcr.io/acme/computer:7"}, nil)
	if e.App.Computers.Image != "ghcr.io/acme/computer:7" {
		t.Fatalf("manager image %q", e.App.Computers.Image)
	}
	e.MustDo("PUT", "/admin/settings/computers.image", map[string]any{"value": ""}, nil)
	if e.App.Computers.Image != "ghcr.io/alternayte/fettle-computer:1.0.0-rc.4" || !instanceSettings(e)["computers.image"].IsDefault {
		t.Fatalf("reset image %q", e.App.Computers.Image)
	}
	if code, _ := e.Do("PUT", "/admin/settings/computers.image", map[string]any{"value": "not an image"}, nil); code != http.StatusUnprocessableEntity {
		t.Fatalf("a bad image: %d", code)
	}
	// A pin to another release of this image is refused.
	if code, _ := e.Do("PUT", "/admin/settings/computers.image", map[string]any{"value": "ghcr.io/alternayte/fettle-computer:1.0.0-rc.3"}, nil); code != http.StatusUnprocessableEntity {
		t.Fatalf("an image of another release: %d", code)
	}

	// The variable locks the field.
	t.Setenv("FETTLE_COMPUTER_IMAGE", "ghcr.io/acme/computer:env")
	if l := instanceSettings(e)["computers.image"]; !l.Locked || l.Env != "FETTLE_COMPUTER_IMAGE" || string(l.Value) != `"ghcr.io/acme/computer:env"` {
		t.Fatalf("locked image: %+v", l)
	}
	if code, body := e.Do("PUT", "/admin/settings/computers.image", map[string]any{"value": "ghcr.io/acme/computer:8"}, nil); code != http.StatusConflict || !strings.Contains(body, "FETTLE_COMPUTER_IMAGE") {
		t.Fatalf("set a locked field: %d %s", code, body)
	}
	t.Setenv("FETTLE_COMPUTER_IMAGE", "")

	// A space owner cannot change a setting of the server through the space settings.
	if code, _ := e.Do("PUT", e.SP("/settings/computers.proxy"), map[string]any{"value": "http://proxy.acme.test:3128"}, nil); code != http.StatusUnprocessableEntity {
		t.Fatalf("instance setting through the space: %d", code)
	}

	// The proxy and the roots apply to the server HTTP client without a restart.
	caPEM, cert := tlsFixture(t)
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("acme")) }))
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	ts.StartTLS()
	t.Cleanup(ts.Close)
	_, port, _ := net.SplitHostPort(ts.Listener.Addr().String())
	proxyURL, tunnels := connectProxy(t, ts.Listener.Addr().String())
	target := "https://fake-model.test:" + port + "/"
	if resp, err := e.App.HTTP.Get(target); err == nil {
		_ = resp.Body.Close()
		t.Fatal("the fixture host answers without the proxy")
	}
	if code, body := e.Do("PUT", "/admin/settings/computers.proxy", map[string]any{"value": strings.Replace(proxyURL, "http://", "http://bot:acme-proxy-pass@", 1)}, nil); code != http.StatusUnprocessableEntity || !strings.Contains(body, "password") {
		t.Fatalf("a password in the URL: %d %s", code, body)
	}
	e.MustDo("PUT", "/admin/settings/computers.proxy", map[string]any{"value": strings.Replace(proxyURL, "http://", "http://bot@", 1)}, nil)
	e.MustDo("PUT", "/admin/settings/computers.proxy_password", map[string]any{"value": "acme-proxy-pass"}, nil)
	e.MustDo("PUT", "/admin/settings/computers.extra_ca", map[string]any{"value": string(caPEM)}, nil)
	resp, err := e.App.HTTP.Get(target)
	if err != nil {
		t.Fatalf("through the proxy: %v", err)
	}
	_ = resp.Body.Close()
	if tunnels.Load() == 0 {
		t.Fatal("the request did not go through the proxy")
	}

	// The secret is write-only: the API, the audit log and the server log never hold it.
	pw := instanceSettings(e)["computers.proxy_password"]
	if !pw.Secret || !pw.Set || string(pw.Value) != "null" {
		t.Fatalf("secret view: %+v", pw)
	}
	_, list := e.Do("GET", "/admin/settings", nil, nil)
	_, audit := e.Do("GET", e.SP("/audit?limit=500"), nil, nil)
	if strings.Contains(list, "acme-proxy-pass") || strings.Contains(audit, "acme-proxy-pass") {
		t.Fatal("the proxy password is in the API")
	}
	if strings.Contains(e.App.Vault.Redactor.Global("acme-proxy-pass"), "acme-proxy-pass") {
		t.Fatal("the redactor does not know the proxy password")
	}

	// A computer gets the roots, and the password apart from the URL.
	n, err := e.App.Computers.Network(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n.ProxyPassword != "acme-proxy-pass" || strings.Contains(n.Proxy, "acme-proxy-pass") || !strings.Contains(n.Proxy, "bot@") || !strings.Contains(n.RootsPEM, "BEGIN CERTIFICATE") {
		t.Fatalf("computer network: %+v", n)
	}

	// Clearing the secret leaves it unset.
	e.MustDo("PUT", "/admin/settings/computers.proxy_password", map[string]any{"value": ""}, nil)
	if instanceSettings(e)["computers.proxy_password"].Set {
		t.Fatal("a cleared secret reads as set")
	}
}
