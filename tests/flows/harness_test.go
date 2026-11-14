//go:build e2e

// Package flows runs end-to-end flows against the built fettle binary (layer E).
package flows

import (
	"bufio"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alternayte/fettle/internal/apptest"
	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
	"github.com/alternayte/fettle/internal/testenv"
)

func binary(t *testing.T) string {
	b := os.Getenv("FETTLE_BIN")
	if b == "" {
		b = "../../bin/fettle"
	}
	abs, err := filepath.Abs(b)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("the fettle binary is missing at %s; run just build", abs)
	}
	return abs
}

// server is one running fettle process.
type server struct {
	cmd      *exec.Cmd
	data     string
	logs     *strings.Builder
	env      []string
	fixtures map[string]string // fixture site -> container ID
	*apptest.Env
}

// startServer runs `fettle serve` on dialect with a fresh data folder and the fake model.
func startServer(t *testing.T, dialect string, env ...string) *server {
	t.Helper()
	fake, err := fakemodel.New()
	if err != nil {
		t.Fatal(err)
	}
	fs := httptest.NewServer(fake)
	t.Cleanup(fs.Close)
	data := t.TempDir()
	db := "sqlite"
	if dialect == "postgres" {
		db = testenv.PostgresURL(t)
	}
	return startOn(t, data, db, fake, fs.URL, env...)
}

func startOn(t *testing.T, data, db string, fake *fakemodel.Server, fakeURL string, env ...string) *server {
	t.Helper()
	cmd := exec.Command(binary(t), "serve", "--data", data, "--db", db, "--addr", "127.0.0.1:0")
	cmd.Env = append(os.Environ(), append([]string{"FETTLE_NO_KEYCHAIN=1", "FETTLE_LOG_LEVEL=info"}, env...)...)
	out, _ := cmd.StderrPipe()
	logs := &strings.Builder{}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(out)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		once := false
		for sc.Scan() {
			logs.WriteString(sc.Text() + "\n")
			if !once && strings.Contains(sc.Text(), "fettle is ready") {
				once = true
				close(ready)
			}
		}
	}()
	select {
	case <-ready:
	case <-time.After(60 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("fettle did not start:\n%s", logs.String())
	}
	s := &server{cmd: cmd, data: data, logs: logs}
	t.Cleanup(func() { s.stop() })
	addr, _ := os.ReadFile(filepath.Join(data, "fettle.addr"))
	tok, _ := os.ReadFile(filepath.Join(data, "local.token"))
	s.Env = apptest.Attach(t, "http://"+strings.TrimSpace(string(addr)), strings.TrimSpace(string(tok)), fake, fakeURL)
	return s
}

func (s *server) stop() {
	if s.cmd.ProcessState == nil {
		_ = s.cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { _ = s.cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(35 * time.Second):
			_ = s.cmd.Process.Kill()
		}
	}
}
