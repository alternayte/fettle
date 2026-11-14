package host_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/alternayte/fettle/internal/computers/conformance"
	"github.com/alternayte/fettle/internal/providers/host"
)

func agentd(t *testing.T) string {
	if b := os.Getenv("FETTLE_AGENTD_BIN"); b != "" {
		return b
	}
	bin := filepath.Join(t.TempDir(), "fettle-agentd")
	_, file, _, _ := runtime.Caller(0)
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/fettle-agentd")
	cmd.Dir = filepath.Join(filepath.Dir(file), "..", "..", "..")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build agentd: %v\n%s", err, out)
	}
	return bin
}

// TestSCN_CMP_016_HostConformance covers the provider part of SCN-CMP-016; the hard-ask card
// to enable the host provider is in the app tests.
func TestSCN_CMP_016_HostConformance(t *testing.T) {
	p := host.New(agentd(t), t.TempDir())
	t.Run("provider", func(t *testing.T) { conformance.Run(t, p, "") })
	t.Run("manager", func(t *testing.T) { conformance.Manager(t, "host", p, "") })
}
