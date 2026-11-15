package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alternayte/fettle/internal/apptest"
	"github.com/alternayte/fettle/internal/testenv"
)

func TestSCN_CORE_006_DoctorReportsEachCheck(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "fettle")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	testenv.Each(t, func(t *testing.T, dialect string) {
		e := apptest.Start(t, dialect)
		e.SetupModel("openai-chat", "", 128000)
		// A connector whose server does not listen.
		var c, acc struct {
			ID string `json:"id"`
		}
		e.MustDo("POST", e.SP("/connectors"), map[string]any{"name": "fakemail", "url": "http://127.0.0.1:9/mcp", "auth": "header", "header_name": "X-Api-Key"}, &c)
		e.MustDo("POST", e.SP("/connectors/%s/accounts", c.ID), map[string]any{"label": "work", "value": "acme-mail-key-3301"}, &acc)
		// In SQLite mode one process owns the database, so the server stops first.
		sctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_ = e.App.Shutdown(sctx)
		cancel()

		cmd := exec.Command(bin, "doctor", "--data", e.Cfg.Data, "--db", e.Cfg.DB)
		cmd.Env = append(os.Environ(), "FETTLE_MASTER_KEY="+testenv.TestMasterKey, "FETTLE_NO_KEYCHAIN=1", "FETTLE_DEFAULT_PROVIDER=docker", "FETTLE_LOG_LEVEL=error")
		out, err := cmd.CombinedOutput()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() == 0 {
			t.Fatalf("doctor exit: %v\n%s", err, out)
		}
		lines := map[string]string{}
		for _, l := range strings.Split(string(out), "\n") {
			mark, rest, ok := strings.Cut(l, "  ")
			if !ok {
				continue
			}
			name, _, _ := strings.Cut(rest, ":")
			lines[name] = mark + " " + rest
		}
		want := map[string]string{"model provider fake openai-chat": "PASS", "computer provider docker": "PASS", "connector fakemail": "FAIL"}
		for name, mark := range want {
			if !strings.HasPrefix(lines[name], mark) {
				t.Errorf("%s: %q, want %s\n%s", name, lines[name], mark, out)
			}
		}
		t.Logf("doctor:\n%s", out)
		if l := lines["connector fakemail"]; !strings.Contains(l, "127.0.0.1:9") && !strings.Contains(strings.ToLower(l), "refused") {
			t.Errorf("the connector check has no error text: %q", l)
		}
	})
}
