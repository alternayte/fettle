package app_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alternayte/fettle/internal/app"
	"github.com/alternayte/fettle/internal/apptest"
	fakee2b "github.com/alternayte/fettle/internal/fakes/e2b"
)

// fettle computer add e2b --key tests the key against the E2B API and names the fault.
func TestE2BCheckTestsKey(t *testing.T) {
	fake, err := fakee2b.New("acme-e2b-key", fakee2b.NewDocker())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fake.Close)
	ca := filepath.Join(t.TempDir(), "e2b-ca.pem")
	if err := os.WriteFile(ca, fake.CAPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FETTLE_E2B_API_URL", fake.URL)
	t.Setenv("FETTLE_E2B_API_KEY", "")
	e := apptest.Start(t, "sqlite", func(c *app.Config) { c.ExtraCAFile = ca })
	var out struct {
		Pass       bool   `json:"pass"`
		Error      string `json:"error"`
		Configured bool   `json:"configured"`
	}
	e.MustDo("POST", "/computer-setup/e2b-check", map[string]string{"key": "acme-e2b-key"}, &out)
	if !out.Pass || out.Configured {
		t.Fatalf("a working key: %+v", out)
	}
	e.MustDo("POST", "/computer-setup/e2b-check", map[string]string{"key": "acme-wrong-key"}, &out)
	if out.Pass || !strings.Contains(out.Error, "refuses this API key") || strings.Contains(out.Error, "acme-wrong-key") {
		t.Fatalf("a wrong key: %+v", out)
	}
	if _, ok := e.App.Computers.Providers["e2b"]; ok {
		t.Fatal("e2b is on without FETTLE_E2B_API_KEY")
	}

	// With the key, the server has the provider, but it is never the default (D-15).
	t.Setenv("FETTLE_E2B_API_KEY", "acme-e2b-key")
	on := apptest.Start(t, "sqlite", func(c *app.Config) { c.ExtraCAFile = ca })
	if _, ok := on.App.Computers.Providers["e2b"]; !ok || on.App.Computers.Default == "e2b" {
		t.Fatalf("providers %v, default %s", on.App.Computers.Providers, on.App.Computers.Default)
	}
	on.MustDo("POST", "/computer-setup/e2b-check", map[string]string{"key": "acme-e2b-key"}, &out)
	if !out.Pass || !out.Configured {
		t.Fatalf("a configured server: %+v", out)
	}
}
