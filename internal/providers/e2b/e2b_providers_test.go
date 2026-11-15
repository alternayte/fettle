//go:build providers

package e2b_test

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/agentd"
	"github.com/alternayte/fettle/internal/computers"
	"github.com/alternayte/fettle/internal/computers/conformance"
	"github.com/alternayte/fettle/internal/providers/e2b"
)

// SCN-CMP-009: with a real key in FETTLE_E2B_API_KEY, the conformance suite passes on e2b and
// sleep pauses the sandbox. Without the key the scenario reports why it did not run.
func TestSCN_CMP_009_E2BConformance(t *testing.T) {
	key := os.Getenv("FETTLE_E2B_API_KEY")
	if key == "" {
		conformance.Report(t, "SCN-CMP-009", "e2b", false, map[string]any{"skipped": "FETTLE_E2B_API_KEY is not set"})
		return
	}
	image := os.Getenv("FETTLE_E2B_IMAGE")
	if image == "" {
		image = "ghcr.io/alternayte/fettle-computer:latest"
	}
	p := e2b.New(key, image, "providers-test")
	if u := os.Getenv("FETTLE_E2B_API_URL"); u != "" {
		p.API = u
	}
	pass := false
	defer func() {
		conformance.Report(t, "SCN-CMP-009", "e2b", pass && !t.Failed(), map[string]any{"remote": true})
	}()
	ctx := context.Background()
	if err := p.Ping(ctx); err != nil {
		t.Fatalf("connection test: %v", err)
	}
	t.Run("provider", func(t *testing.T) { conformance.Run(t, p, "") })
	t.Run("manager", func(t *testing.T) { conformance.Manager(t, "e2b", p, "") })
	t.Run("sleep_pauses", func(t *testing.T) {
		cert, key, err := agentd.GenerateCert([]string{"fettle-computer"})
		if err != nil {
			t.Fatal(err)
		}
		m, err := p.Create(ctx, computers.Spec{ZoneID: uuid.NewString(), Size: computers.SizeSmall, Env: map[string]string{
			"FETTLE_AGENTD_TOKEN": "tok-" + uuid.NewString(), "FETTLE_AGENTD_TLS_CERT": string(cert), "FETTLE_AGENTD_TLS_KEY": string(key), "FETTLE_AGENTD_NETWORK": `{"mode":"open"}`,
		}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = m.Destroy(context.Background()) })
		if err := m.Sleep(ctx); err != nil {
			t.Fatal(err)
		}
		if st, err := p.State(ctx, m.Ref()); err != nil || st != "paused" {
			t.Fatalf("after sleep the sandbox is %q %v, want paused", st, err)
		}
	})
	pass = true
}
