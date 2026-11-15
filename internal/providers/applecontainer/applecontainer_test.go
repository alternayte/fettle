//go:build providers && darwin

package applecontainer_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alternayte/fettle/internal/computers"
	"github.com/alternayte/fettle/internal/computers/conformance"
	"github.com/alternayte/fettle/internal/providers/applecontainer"
)

func image() string {
	if v := os.Getenv("FETTLE_COMPUTER_IMAGE"); v != "" {
		return v
	}
	return "fettle-computer:dev"
}

// root is a lasting runtime folder for this host, so later runs reuse the install.
func root(t *testing.T) string {
	if v := os.Getenv("FETTLE_APPLECONTAINER_ROOT"); v != "" {
		return v
	}
	d, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(d, "fettle-providers", "applecontainer")
}

func TestSCN_CMP_006_AppleContainerInstallAndConformance(t *testing.T) {
	if err := applecontainer.Supported(); err != nil {
		conformance.Report(t, "SCN-CMP-006", "applecontainer", false, map[string]any{"skipped": err.Error()})
		t.Fatal(err)
	}
	r := root(t)
	_, installed := os.Stat(filepath.Join(r, "install", "bin", "container"))
	p := applecontainer.New(r, image())
	var steps []computers.Progress
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	start := time.Now()
	if err := p.Prepare(ctx, func(pr computers.Progress) {
		if pr.State != "running" {
			steps = append(steps, pr)
		}
	}); err != nil {
		conformance.Report(t, "SCN-CMP-006", "applecontainer", false, map[string]any{"prepare": err.Error()})
		t.Fatalf("prepare: %v", err)
	}
	if _, err := os.Stat(filepath.Join(r, "install", "bin", "container")); err != nil {
		t.Fatal("the runtime is not in the data folder")
	}
	pass := false
	defer func() {
		conformance.Report(t, "SCN-CMP-006", "applecontainer", pass && !t.Failed(), map[string]any{
			"fresh_install": installed != nil, "prepare_ms": time.Since(start).Milliseconds(), "steps": steps, "runtime": applecontainer.Version,
		})
	}()
	t.Run("provider", func(t *testing.T) { conformance.Run(t, p, image()) })
	t.Run("manager", func(t *testing.T) { conformance.Manager(t, "applecontainer", p, image()) })
	pass = true
}

func TestSCN_CMP_010_AppleContainerResumeTimes(t *testing.T) {
	if err := applecontainer.Supported(); err != nil {
		t.Fatal(err)
	}
	p := applecontainer.New(root(t), image())
	if err := p.Prepare(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	resume, replace := conformance.ResumeTimes(t, "applecontainer", p, image(), 200)
	pass := resume <= 5*time.Second
	conformance.Report(t, "SCN-CMP-010", "applecontainer", pass, map[string]any{"resume_ms": resume.Milliseconds(), "import_200mib_ms": replace.Milliseconds()})
	if !pass {
		t.Fatalf("resume %s; NFR-006 allows 5 s on applecontainer", resume)
	}
}
