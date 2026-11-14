//go:build e2e

package flows

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alternayte/fettle/internal/testenv"
)

const ownerEmail, ownerPassword = "owner@acme.test", "acme-owner-pass-4410"

// serverMode is a fettle process in server mode with one owner account.
type serverMode struct {
	t       *testing.T
	url     string
	baseURL string
	cookie  string
	space   string
}

// startServerMode creates the owner with `fettle admin create-user` and runs `fettle serve --mode server`.
func startServerMode(t *testing.T, baseURL string, env ...string) *serverMode {
	t.Helper()
	data := t.TempDir()
	base := append(os.Environ(), "FETTLE_NO_KEYCHAIN=1", "FETTLE_MASTER_KEY="+testenv.TestMasterKey, "FETTLE_BASE_URL="+baseURL, "FETTLE_PASSWORD="+ownerPassword)
	base = append(base, env...)
	cu := exec.Command(binary(t), "admin", "create-user", "--data", data, "--email", ownerEmail, "--name", "Owner", "--admin")
	cu.Env = base
	if out, err := cu.CombinedOutput(); err != nil {
		t.Fatalf("create-user: %v\n%s", err, out)
	}
	cmd := exec.Command(binary(t), "serve", "--mode", "server", "--data", data, "--addr", "127.0.0.1:0")
	cmd.Env = base
	out, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Signal(os.Interrupt); _ = cmd.Wait() })
	ready := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(out)
		once := false
		for sc.Scan() {
			if !once && strings.Contains(sc.Text(), "fettle is ready") {
				once = true
				close(ready)
			}
		}
	}()
	select {
	case <-ready:
	case <-time.After(60 * time.Second):
		t.Fatal("fettle did not start in server mode")
	}
	addr, _ := os.ReadFile(filepath.Join(data, "fettle.addr"))
	return &serverMode{t: t, url: "http://" + strings.TrimSpace(string(addr)), baseURL: baseURL}
}

func (s *serverMode) do(method, path string, body any, origin string) (int, http.Header, string) {
	s.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, s.url+path, r)
	req.Header.Set("Content-Type", "application/json")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if s.cookie != "" {
		req.Header.Set("Cookie", s.cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(b)
}

func TestSCN_AUTH_004_SessionCookieOriginAndRateLimit(t *testing.T) {
	s := startServerMode(t, "https://fettle.test")
	code, h, body := s.do("POST", "/api/auth/sign-in/email", map[string]string{"email": ownerEmail, "password": ownerPassword}, s.baseURL)
	if code != 200 {
		t.Fatalf("sign-in: %d %s", code, body)
	}
	var set string
	for _, sc := range h.Values("Set-Cookie") {
		if strings.HasPrefix(sc, "fettle_session=") {
			set = sc
		}
	}
	for _, flag := range []string{"HttpOnly", "Secure", "SameSite=Lax"} {
		if !strings.Contains(set, flag) {
			t.Fatalf("session cookie lacks %s: %s", flag, set)
		}
	}
	s.cookie = strings.SplitN(set, ";", 2)[0]
	code, _, body = s.do("GET", "/api/v1/me", nil, "")
	if code != 200 {
		t.Fatalf("GET /me: %d %s", code, body)
	}
	var me struct {
		Spaces []struct{ ID string } `json:"spaces"`
	}
	_ = json.Unmarshal([]byte(body), &me)
	if len(me.Spaces) != 1 {
		t.Fatalf("owner spaces: %s", body)
	}
	path := "/api/v1/spaces/" + me.Spaces[0].ID + "/vault"
	item := map[string]any{"name": "site", "kind": "secret", "value": "shh-value-1234"}
	if code, _, _ = s.do("POST", path, item, "https://evil.test"); code != 403 {
		t.Fatalf("a cookie POST from a foreign origin got %d", code)
	}
	if code, _, body = s.do("POST", path, item, s.baseURL); code/100 != 2 {
		t.Fatalf("a cookie POST from the own origin got %d %s", code, body)
	}
	// A fresh server counts only failed sign-ins in this minute.
	s = startServerMode(t, "https://fettle.test")
	for i := 1; i <= 11; i++ {
		code, _, _ = s.do("POST", "/api/auth/sign-in/email", map[string]string{"email": ownerEmail, "password": "wrong-password-" + string(rune('a'+i))}, s.baseURL)
		if i <= 10 && code == 429 {
			t.Fatalf("sign-in %d was limited", i)
		}
	}
	if code != 429 {
		t.Fatalf("the eleventh failed sign-in got %d, want 429", code)
	}
}
