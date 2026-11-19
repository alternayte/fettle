//go:build providers && darwin

package applecontainer_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// sweep deletes the computers in the runtime under r. The root is a lasting folder, and a run
// that hits the go test timeout ends without its cleanups, so each test sweeps before and
// after it runs. The service must run on r, which Prepare makes sure of.
func sweep(t *testing.T, r string) {
	t.Helper()
	bin := filepath.Join(r, "install", "bin", "container")
	out, err := exec.Command(bin, "list", "--all", "--format", "json").Output()
	if err != nil {
		t.Errorf("list the computers: %v", err)
		return
	}
	var list []struct {
		Configuration struct {
			ID     string            `json:"id"`
			Labels map[string]string `json:"labels"`
		} `json:"configuration"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		t.Errorf("read the computer list: %v", err)
		return
	}
	for _, c := range list {
		if c.Configuration.Labels["fettle.computer"] != "1" {
			continue
		}
		if out, err := exec.Command(bin, "delete", "--force", c.Configuration.ID).CombinedOutput(); err != nil {
			t.Errorf("delete the computer %s: %s", c.Configuration.ID, strings.TrimSpace(string(out)))
		}
	}
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
	sweep(t, r)
	t.Cleanup(func() { sweep(t, r) })
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
	r := root(t)
	p := applecontainer.New(r, image())
	if err := p.Prepare(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	sweep(t, r)
	t.Cleanup(func() { sweep(t, r) })
	resume, replace := conformance.ResumeTimes(t, "applecontainer", p, image(), 200)
	pass := resume <= 5*time.Second
	conformance.Report(t, "SCN-CMP-010", "applecontainer", pass, map[string]any{"resume_ms": resume.Milliseconds(), "import_200mib_ms": replace.Milliseconds()})
	if !pass {
		t.Fatalf("resume %s; NFR-006 allows 5 s on applecontainer", resume)
	}
}
