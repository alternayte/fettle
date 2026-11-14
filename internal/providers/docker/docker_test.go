//go:build providers

package docker_test

import (
	"os"
	"testing"
	"time"

	"github.com/alternayte/fettle/internal/computers/conformance"
	"github.com/alternayte/fettle/internal/providers/docker"
)

func image() string {
	if v := os.Getenv("FETTLE_COMPUTER_IMAGE"); v != "" {
		return v
	}
	return "fettle-computer:dev"
}

func TestSCN_CMP_005_DockerConformance(t *testing.T) {
	p, err := docker.New(image())
	if err != nil {
		conformance.Report(t, "SCN-CMP-005", "docker", false, map[string]any{"skipped": err.Error()})
		t.Fatalf("docker: %v", err)
	}
	pass := false
	defer func() { conformance.Report(t, "SCN-CMP-005", "docker", pass && !t.Failed(), nil) }()
	t.Run("provider", func(t *testing.T) { conformance.Run(t, p, image()) })
	t.Run("manager", func(t *testing.T) { conformance.Manager(t, "docker", p, image()) })
	pass = true
}

func TestSCN_CMP_010_DockerResumeTimes(t *testing.T) {
	p, err := docker.New(image())
	if err != nil {
		conformance.Report(t, "SCN-CMP-010", "docker", false, map[string]any{"skipped": err.Error()})
		t.Fatalf("docker: %v", err)
	}
	resume, replace := conformance.ResumeTimes(t, "docker", p, image(), 200)
	pass := resume <= 30*time.Second && replace <= 30*time.Second
	conformance.Report(t, "SCN-CMP-010", "docker", pass, map[string]any{"resume_ms": resume.Milliseconds(), "import_200mib_ms": replace.Milliseconds()})
	if !pass {
		t.Fatalf("resume %s, start with a 200 MiB import %s; NFR-006 allows 30 s", resume, replace)
	}
}
