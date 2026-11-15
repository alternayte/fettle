//go:build providers

package kiln_test

import (
	"os"
	"testing"
	"time"

	"github.com/alternayte/fettle/internal/computers/conformance"
	"github.com/alternayte/fettle/internal/providers/kiln"
)

// remote returns the provider for the Kiln host in FETTLE_KILN_URL and FETTLE_KILN_TOKEN.
// Without them this host has no Kiln, and the scenario reports why.
func remote(t *testing.T, scn string) *kiln.Provider {
	t.Helper()
	url, token := os.Getenv("FETTLE_KILN_URL"), os.Getenv("FETTLE_KILN_TOKEN")
	if url == "" || token == "" {
		conformance.Report(t, scn, "kiln", false, map[string]any{"skipped": "FETTLE_KILN_URL and FETTLE_KILN_TOKEN are not set"})
		return nil
	}
	image := os.Getenv("FETTLE_KILN_IMAGE")
	if image == "" {
		image = "ghcr.io/alternayte/fettle-computer:latest"
	}
	return kiln.New(url, token, image, "providers-test")
}

func TestSCN_CMP_018_RemoteKilnConformance(t *testing.T) {
	p := remote(t, "SCN-CMP-018")
	if p == nil {
		return
	}
	pass := false
	defer func() {
		conformance.Report(t, "SCN-CMP-018", "kiln", pass && !t.Failed(), map[string]any{"remote": true})
	}()
	if err := p.Ping(t.Context()); err != nil {
		t.Fatalf("connection test: %v", err)
	}
	t.Run("provider", func(t *testing.T) { conformance.Run(t, p, "") })
	t.Run("manager", func(t *testing.T) { conformance.Manager(t, "kiln", p, "") })
	pass = true
}

func TestSCN_CMP_010_KilnResumeTimes(t *testing.T) {
	p := remote(t, "SCN-CMP-010")
	if p == nil {
		return
	}
	resume, replace := conformance.ResumeTimes(t, "kiln", p, "", 200)
	pass := resume <= 5*time.Second
	conformance.Report(t, "SCN-CMP-010", "kiln", pass, map[string]any{"resume_ms": resume.Milliseconds(), "import_200mib_ms": replace.Milliseconds()})
	if !pass {
		t.Fatalf("resume %s; NFR-006 allows 5 s on kiln", resume)
	}
}
