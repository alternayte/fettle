//go:build smoke

// Package smoke runs the layer S checks (§24): the Compose stack, the install script and
// the release artifacts. They need Docker.
package smoke

import (
	"archive/tar"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func root() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}

func report(t *testing.T, scn string, pass bool, extra map[string]any) {
	line := map[string]any{"scn": scn, "pass": pass, "layer": "S", "host": runtime.GOOS + "/" + runtime.GOARCH, "at": time.Now().UTC().Format(time.RFC3339)}
	for k, v := range extra {
		line[k] = v
	}
	b, _ := json.Marshal(line)
	t.Log(string(b))
	if dir := os.Getenv("FETTLE_REPORT_DIR"); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
		if f, err := os.OpenFile(filepath.Join(dir, "smoke.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			_, _ = f.Write(append(b, '\n'))
			_ = f.Close()
		}
	}
}

func run(t *testing.T, env []string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = root()
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

// serverImage builds the server image once for the checks.
func serverImage(t *testing.T) string {
	t.Helper()
	img := "fettle:smoke"
	run(t, nil, "docker", "build", "-q", "-t", img, "-f", "deploy/Dockerfile", ".")
	return img
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

func TestSCN_OPS_002_ComposeStartsWithPostgresAndMinIO(t *testing.T) {
	img := serverImage(t)
	port := freePort(t)
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	env := []string{"FETTLE_IMAGE=" + img, "FETTLE_MASTER_KEY=" + base64.StdEncoding.EncodeToString(key), fmt.Sprintf("FETTLE_PORT=%d", port)}
	project := fmt.Sprintf("fettle-smoke-%d", port)
	t.Cleanup(func() {
		cmd := exec.Command("docker", "compose", "-p", project, "-f", "deploy/compose.yaml", "down", "-v")
		cmd.Dir, cmd.Env = root(), append(os.Environ(), env...)
		_ = cmd.Run()
	})
	start := time.Now()
	run(t, env, "docker", "compose", "-p", project, "-f", "deploy/compose.yaml", "up", "-d", "--no-build")
	var body string
	ready := false
	for time.Since(start) < 120*time.Second {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/readyz", port))
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			body = string(b)
			if resp.StatusCode == 200 {
				ready = true
				break
			}
		}
		time.Sleep(time.Second)
	}
	ms := time.Since(start).Milliseconds()
	pass := ready && strings.Contains(body, `"database":"ok"`) && strings.Contains(body, `"object_store":"ok"`)
	report(t, "SCN-OPS-002", pass, map[string]any{"ms": ms})
	if !pass {
		logs := run(t, env, "docker", "compose", "-p", project, "-f", "deploy/compose.yaml", "logs", "--tail", "40")
		t.Fatalf("/readyz is not 200 within 120 s: %s\n%s", body, logs)
	}
}

// release writes a local release folder: the linux tarball for the Docker host's
// architecture, SHA256SUMS and the unit file, as the release pipeline publishes them.
func release(t *testing.T, dir string) {
	t.Helper()
	arch := strings.TrimSpace(run(t, nil, "docker", "version", "-f", "{{.Server.Arch}}"))
	bin := filepath.Join(dir, "build")
	for _, p := range []string{"fettle", "fettle-agentd"} {
		run(t, []string{"GOOS=linux", "GOARCH=" + arch, "CGO_ENABLED=0"}, "go", "build", "-trimpath", "-o", filepath.Join(bin, p), "./cmd/"+p)
	}
	archive := fmt.Sprintf("fettle_linux_%s.tar.gz", arch)
	f, err := os.Create(filepath.Join(dir, archive))
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, p := range []string{"build/fettle", "build/fettle-agentd", "../../deploy/fettle.service"} {
		src := filepath.Join(dir, p)
		if strings.HasPrefix(p, "../") {
			src = filepath.Join(root(), "deploy", "fettle.service")
		}
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		_ = tw.WriteHeader(&tar.Header{Name: filepath.Base(src), Mode: 0o755, Size: int64(len(b))})
		_, _ = tw.Write(b)
	}
	_ = tw.Close()
	_ = gz.Close()
	_ = f.Close()
	b, _ := os.ReadFile(filepath.Join(dir, archive))
	sum := sha256.Sum256(b)
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(hex.EncodeToString(sum[:])+"  "+archive+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSCN_OPS_003_InstallScriptOnDebianWithSystemd(t *testing.T) {
	dir := t.TempDir()
	release(t, dir)
	// A Debian 12 image with systemd and curl, the "clean Debian" of the scenario.
	ctxDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(ctxDir, "Dockerfile"), []byte("FROM debian:bookworm-slim\nRUN apt-get update && apt-get install -y --no-install-recommends systemd systemd-sysv curl ca-certificates && rm -rf /var/lib/apt/lists/*\nSTOPSIGNAL SIGRTMIN+3\nCMD [\"/lib/systemd/systemd\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, nil, "docker", "build", "-q", "-t", "fettle-smoke-debian:12", ctxDir)
	name := fmt.Sprintf("fettle-smoke-install-%d", time.Now().UnixNano())
	run(t, nil, "docker", "run", "-d", "--name", name, "--privileged", "--cgroupns=host", "-v", "/sys/fs/cgroup:/sys/fs/cgroup:rw",
		"-v", dir+":/release:ro", "-v", filepath.Join(root(), "install.sh")+":/install.sh:ro", "fettle-smoke-debian:12")
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })
	for i := 0; i < 30; i++ {
		out, _ := exec.Command("docker", "exec", name, "systemctl", "is-system-running").CombinedOutput()
		if s := strings.TrimSpace(string(out)); s == "running" || s == "degraded" {
			break
		}
		time.Sleep(time.Second)
	}
	// A tampered tarball is refused before anything is installed.
	bad := t.TempDir()
	release(t, bad)
	sums, _ := os.ReadFile(filepath.Join(bad, "SHA256SUMS"))
	_ = os.WriteFile(filepath.Join(bad, "SHA256SUMS"), []byte(strings.Repeat("0", 64)+string(sums[64:])), 0o644)
	run(t, nil, "docker", "cp", bad, name+":/bad")
	out, err := exec.Command("docker", "exec", "-e", "FETTLE_RELEASE_URL=file:///bad", name, "sh", "/install.sh").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "does not match SHA256SUMS") {
		t.Fatalf("a tampered release installed: %v\n%s", err, out)
	}
	if _, err := exec.Command("docker", "exec", name, "test", "-e", "/usr/local/bin/fettle").CombinedOutput(); err == nil {
		t.Fatal("the tampered binary is in /usr/local/bin")
	}

	start := time.Now()
	out, err = exec.Command("docker", "exec", "-e", "FETTLE_RELEASE_URL=file:///release", "-e", "FETTLE_BASE_URL=http://localhost:7420", name, "sh", "/install.sh").CombinedOutput()
	if err != nil {
		t.Fatalf("install.sh: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Checksum OK") {
		t.Fatalf("install.sh did not check the checksum:\n%s", out)
	}
	active, ready := false, false
	for time.Since(start) < 60*time.Second {
		a, _ := exec.Command("docker", "exec", name, "systemctl", "is-active", "fettle").CombinedOutput()
		active = strings.TrimSpace(string(a)) == "active"
		r, _ := exec.Command("docker", "exec", name, "curl", "-fsS", "localhost:7420/readyz").CombinedOutput()
		ready = strings.Contains(string(r), `"ready":true`)
		if active && ready {
			break
		}
		time.Sleep(time.Second)
	}
	report(t, "SCN-OPS-003", active && ready, map[string]any{"ms": time.Since(start).Milliseconds()})
	if !active || !ready {
		logs, _ := exec.Command("docker", "exec", name, "journalctl", "-u", "fettle", "--no-pager", "-n", "30").CombinedOutput()
		t.Fatalf("the service is active %t, ready %t:\n%s", active, ready, logs)
	}
}
