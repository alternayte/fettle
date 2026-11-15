//go:build desktopsmoke && darwin

// Package desktop runs the desktop app smoke test (layer D, §24): it builds the Wails app
// with the smoke control socket, launches it, and drives the real window, tray, update
// and first-run wizard.
package desktop

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/alternayte/fettle/internal/providers/applecontainer"

	"github.com/alternayte/fettle/internal/desktop"
	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
)

const (
	v1 = "0.2.0-smoke.1"
	v2 = "0.2.0-smoke.2"
)

func root(t *testing.T) string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// buildApp builds Fettle.app with the smoke socket at version into dir.
func buildApp(t *testing.T, dir, version string) string {
	t.Helper()
	app := filepath.Join(dir, "Fettle.app")
	bin := filepath.Join(app, "Contents", "MacOS", "fettle-desktop")
	cmd := exec.Command("go", "build", "-tags", "desktop smoke", "-ldflags", "-X main.version="+version, "-o", bin, "./cmd/fettle-desktop")
	cmd.Dir = root(t)
	cmd.Env = append(os.Environ(), "MACOSX_DEPLOYMENT_TARGET=13.0", "CGO_CFLAGS=-mmacosx-version-min=13.0", "CGO_LDFLAGS=-mmacosx-version-min=13.0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build the app: %v\n%s", err, out)
	}
	plist, err := os.ReadFile(filepath.Join(root(t), "desktop", "macos", "Info.plist"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte(strings.ReplaceAll(string(plist), "VERSION", version)), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("codesign", "--force", "--sign", "-", app).CombinedOutput(); err != nil {
		t.Fatalf("codesign: %v\n%s", err, out)
	}
	return app
}

// tarApp packs an app bundle as an update package (a tar.gz with the bundle at the top).
func tarApp(t *testing.T, app, out string) string {
	t.Helper()
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	base := filepath.Dir(app)
	err = filepath.WalkDir(app, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(base, p)
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			link, _ = os.Readlink(p)
		}
		h, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		h.Name = rel
		if d.IsDir() {
			h.Name += "/"
		}
		if err := tw.WriteHeader(h); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			src, err := os.Open(p)
			if err != nil {
				return err
			}
			defer func() { _ = src.Close() }()
			_, err = io.Copy(tw, src)
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []io.Closer{tw, gz, f} {
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// smoke is a connection to the app's control socket.
type smoke struct {
	t    *testing.T
	sock string
}

type state struct {
	Visible   bool     `json:"visible"`
	Windows   []string `json:"windows"`
	Opened    []string `json:"opened"`
	Asked     bool     `json:"asked"`
	Autostart bool     `json:"autostart"`
	Version   string   `json:"version"`
	Base      string   `json:"base"`
	Requests  []struct {
		Path   string `json:"path"`
		Remote string `json:"remote"`
		Cookie bool   `json:"cookie"`
		Agent  string `json:"agent"`
	} `json:"requests"`
	Error string `json:"error"`
}

func (s *smoke) send(cmd, arg string) (state, error) {
	var st state
	c, err := net.DialTimeout("unix", s.sock, 2*time.Second)
	if err != nil {
		return st, err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	b, _ := json.Marshal(map[string]string{"cmd": cmd, "arg": arg})
	if _, err := c.Write(append(b, '\n')); err != nil {
		return st, err
	}
	line, err := bufio.NewReader(c).ReadBytes('\n')
	if err != nil {
		return st, err
	}
	err = json.Unmarshal(line, &st)
	return st, err
}

func (s *smoke) must(cmd, arg string) state {
	s.t.Helper()
	st, err := s.send(cmd, arg)
	if err != nil {
		s.t.Fatalf("smoke %s: %v", cmd, err)
	}
	if st.Error != "" {
		s.t.Fatalf("smoke %s: %s", cmd, st.Error)
	}
	return st
}

func (s *smoke) waitFor(what string, limit time.Duration, ok func(state) bool) state {
	s.t.Helper()
	deadline := time.Now().Add(limit)
	var st state
	var err error
	for time.Now().Before(deadline) {
		if st, err = s.send("state", ""); err == nil && ok(st) {
			return st
		}
		time.Sleep(200 * time.Millisecond)
	}
	s.t.Fatalf("%s: not within %s (last state %+v, error %v)", what, limit, st, err)
	return st
}

// api calls the local server with the local token.
type api struct {
	t     *testing.T
	base  string
	token string
}

func (a *api) do(method, path string, body any, out any) int {
	a.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, a.base+"/api/v1"+path, r)
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if out != nil {
		_ = json.Unmarshal(b, out)
	}
	return resp.StatusCode
}

// screenLocked reports whether the macOS session is locked. WebKit suspends the page's
// timers in an occluded window, so the steps that drive the page need an unlocked screen.
func screenLocked() bool {
	out, err := exec.Command("ioreg", "-n", "Root", "-d1", "-a").Output()
	if err != nil {
		return false
	}
	i := strings.Index(string(out), "<key>CGSSessionScreenIsLocked</key>")
	return i >= 0 && strings.HasPrefix(strings.TrimSpace(string(out[i+len("<key>CGSSessionScreenIsLocked</key>"):])), "<true/>")
}

func report(t *testing.T, scn string, pass bool, extra map[string]any) {
	line := map[string]any{"scn": scn, "pass": pass, "layer": "D", "host": runtime.GOOS + "/" + runtime.GOARCH, "at": time.Now().UTC().Format(time.RFC3339)}
	if out, err := exec.Command("sw_vers", "-productVersion").Output(); err == nil {
		line["os"] = "macOS " + strings.TrimSpace(string(out))
	}
	for k, v := range extra {
		line[k] = v
	}
	b, _ := json.Marshal(line)
	t.Log(string(b))
	if dir := os.Getenv("FETTLE_REPORT_DIR"); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
		if f, err := os.OpenFile(filepath.Join(dir, "desktop.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			_, _ = f.Write(append(b, '\n'))
			_ = f.Close()
		}
	}
}

// wizardJS completes the first-run wizard in the window like a person: it picks This
// computer, goes past the computer step, adds the fake model as an OpenAI-compatible
// provider, keeps the owner model and types a name.
func wizardJS(baseURL string) string {
	return `(() => {
  if (window.__wizard) return;
  window.__wizard = true;
  const done = {};
  const btn = (text) => [...document.querySelectorAll("button")].find((b) => b.textContent.trim() === text && !b.disabled);
  const set = (id, v) => {
    const el = document.getElementById(id);
    if (!el) return false;
    const proto = el.tagName === "SELECT" ? HTMLSelectElement.prototype : HTMLInputElement.prototype;
    Object.getOwnPropertyDescriptor(proto, "value").set.call(el, v);
    el.dispatchEvent(new Event(el.tagName === "SELECT" ? "change" : "input", { bubbles: true }));
    return true;
  };
  const tick = () => {
    const h = document.querySelector("h1")?.textContent ?? "";
    if (h.startsWith("Where does Fettle run")) btn("Continue")?.click();
    else if (h.startsWith("A computer for your bots")) btn("Continue")?.click();
    else if (h.startsWith("Add a model provider")) {
      if (!done.preset && set("setup-preset", "openai-compatible")) { done.preset = true; return; }
      if (done.preset && !done.form && set("setup-base", "` + baseURL + `") && set("setup-key", "sk-fake-desktop-key-0001")) { done.form = true; return; }
      if (done.form && !done.saved && btn("Save and test")) { btn("Save and test").click(); done.saved = true; return; }
      if (done.saved) btn("Continue")?.click();
    } else if (h.startsWith("Pick the owner model")) (btn("Continue") ?? btn("Open the chat"))?.click();
    else if (h.startsWith("Tell your bots about you")) {
      if (!done.name && set("you-name", "Ada")) { done.name = true; return; }
      btn("Open the chat")?.click();
    }
  };
  setInterval(tick, 300);
})();`
}

func TestSCN_UX_001_DesktopWindowTrayUpdateAndFirstRun(t *testing.T) {
	work := t.TempDir()
	// A unix socket path has at most 104 bytes on macOS, so the data folder is short.
	data, err := os.MkdirTemp("/tmp", "fdt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(data) })
	// A cached Apple container runtime keeps the computer step from a download (NFR-003
	// assumes a cached image). FETTLE_APPLECONTAINER_CACHE names a prepared runtime root.
	if c := os.Getenv("FETTLE_APPLECONTAINER_CACHE"); c != "" {
		if err := os.MkdirAll(filepath.Join(data, "runtime"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(c, filepath.Join(data, "runtime", "applecontainer")); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			stop := exec.Command(filepath.Join(c, "install", "bin", "container"), "system", "stop")
			_ = stop.Run()
		})
	}

	// The fake model answers the onboarding kickoff.
	fake, err := fakemodel.New()
	if err != nil {
		t.Fatal(err)
	}
	fake.AddScript(fakemodel.Script{Name: "kickoff", Match: fakemodel.Match{User: "Start the onboarding interview"}, Steps: []fakemodel.Step{{Text: "Hello Ada. I am Chief. What is your work?"}}})
	fs := httptest.NewServer(fake)
	t.Cleanup(fs.Close)

	// Two builds: the installed app and the update.
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	app1 := buildApp(t, filepath.Join(work, "v1"), v1)
	app2 := buildApp(t, filepath.Join(work, "v2"), v2)
	pkg := tarApp(t, app2, filepath.Join(work, "update.tar.gz"))
	raw, _ := os.ReadFile(pkg)
	sum := sha256.Sum256(raw)
	hexSum := hex.EncodeToString(sum[:])
	feed := http.NewServeMux()
	feedSrv := httptest.NewServer(feed)
	t.Cleanup(feedSrv.Close)
	feed.HandleFunc("/update.json", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(desktop.Manifest{Version: v2, Packages: map[string]desktop.Package{
			desktop.Platform(): {URL: feedSrv.URL + "/pkg", SHA256: hexSum, Signature: desktop.Sign(priv, v2, desktop.Platform(), hexSum)},
		}})
	})
	feed.HandleFunc("/pkg", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(raw) })

	opened := time.Now()
	cmd := exec.Command(filepath.Join(app1, "Contents", "MacOS", "fettle-desktop"))
	cmd.Env = append(os.Environ(), "FETTLE_DATA="+data, "FETTLE_UPDATE_FEED="+feedSrv.URL+"/update.json",
		"FETTLE_UPDATE_KEY="+base64.StdEncoding.EncodeToString(pub), "FETTLE_LOG_LEVEL=info")
	logs, _ := os.Create(filepath.Join(work, "app.log"))
	cmd.Stdout, cmd.Stderr = logs, logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		_ = exec.Command("pkill", "-f", filepath.Join(work, "v")).Run()
		if t.Failed() {
			b, _ := os.ReadFile(filepath.Join(work, "app.log"))
			t.Logf("app log:\n%s", b)
			logs, _ := filepath.Glob(filepath.Join(data, "logs", "*"))
			for _, f := range logs {
				b, _ := os.ReadFile(f)
				t.Logf("%s:\n%s", filepath.Base(f), b)
			}
		}
	})
	sm := &smoke{t: t, sock: filepath.Join(data, "smoke.sock")}
	st := sm.waitFor("the app starts", 60*time.Second, func(s state) bool { return s.Base != "" && s.Visible })
	token, err := os.ReadFile(filepath.Join(data, "local.token"))
	if err != nil {
		t.Fatal(err)
	}
	a := &api{t: t, base: st.Base, token: strings.TrimSpace(string(token))}

	locked := screenLocked()
	if locked {
		for _, scn := range []string{"SCN-UX-002", "SCN-UX-003", "NFR-003"} {
			report(t, scn, false, map[string]any{"skipped": "the screen is locked; WebKit suspends the page in an occluded window"})
		}
	} else {
		// SCN-UX-003 and NFR-003: the provider is detected, the tester completes the wizard,
		// and the Chief's first reply streams within 120 s of opening the app.
		var setup struct {
			Default string `json:"default"`
		}
		a.do("GET", "/computer-setup", nil, &setup)
		// D-15: applecontainer on macOS 26 on Apple silicon, else docker when it answers, else
		// none (the wizard explains the options).
		want := ""
		if applecontainer.Supported() == nil {
			want = "applecontainer"
		} else if exec.Command("docker", "info").Run() == nil {
			want = "docker"
		}
		if setup.Default != want {
			t.Fatalf("the default provider on this Mac is %q, want %q", setup.Default, want)
		}
		var firstReply time.Duration
		lastJS := time.Time{}
		deadline := opened.Add(120 * time.Second)
		for time.Now().Before(deadline) {
			// A reload of the page drops the script; the page's guard keeps one copy running.
			if time.Since(lastJS) > 5*time.Second {
				sm.must("js", wizardJS(fs.URL+"/v1"))
				lastJS = time.Now()
			}
			var bots struct {
				Bots []struct {
					ChatID    string `json:"chat_id"`
					IsPrimary bool   `json:"is_primary"`
				} `json:"bots"`
			}
			var me struct {
				Spaces []struct {
					ID string `json:"id"`
				} `json:"spaces"`
			}
			if a.do("GET", "/me", nil, &me) == 200 && len(me.Spaces) > 0 {
				sp := me.Spaces[0].ID
				a.do("GET", "/spaces/"+sp+"/bots", nil, &bots)
				for _, b := range bots.Bots {
					if !b.IsPrimary {
						continue
					}
					var msgs struct {
						Messages []struct {
							AuthorKind string `json:"author_kind"`
							Body       string `json:"body"`
						} `json:"messages"`
					}
					a.do("GET", "/spaces/"+sp+"/chats/"+b.ChatID+"/messages?limit=20", nil, &msgs)
					for _, m := range msgs.Messages {
						if m.AuthorKind == "bot" && strings.Contains(m.Body, "What is your work?") {
							firstReply = time.Since(opened)
						}
					}
				}
			}
			if firstReply > 0 {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		report(t, "SCN-UX-003", firstReply > 0, map[string]any{"first_reply_ms": firstReply.Milliseconds(), "provider": setup.Default})
		report(t, "NFR-003", firstReply > 0 && firstReply <= 120*time.Second, map[string]any{"first_reply_ms": firstReply.Milliseconds()})
		if firstReply == 0 {
			// Show where the first run stopped: the setup state, the providers and the page's
			// last requests.
			var me map[string]any
			a.do("GET", "/me", nil, &me)
			t.Logf("me: %v", me)
			if sps, _ := me["spaces"].([]any); len(sps) > 0 {
				if sp, _ := sps[0].(map[string]any); sp != nil {
					var mp any
					a.do("GET", fmt.Sprintf("/spaces/%v/model-providers", sp["id"]), nil, &mp)
					t.Logf("model providers: %v", mp)
				}
			}
			if st, err := sm.send("state", ""); err == nil {
				n := len(st.Requests)
				for _, r := range st.Requests[max(0, n-15):] {
					t.Logf("page request %s", r.Path)
				}
			}
			t.Fatal("the Chief's first reply did not stream within 120 s of opening the app")
		}

		// SCN-UX-002 and SI-16: app data goes to 127.0.0.1 over HTTP with the token; another
		// origin opens the system browser; a screen opens in its own window.
		st = sm.must("state", "")
		if !strings.HasPrefix(st.Base, "http://127.0.0.1:") {
			t.Fatalf("the window loads %s, want http://127.0.0.1", st.Base)
		}
		var appReqs int
		for _, r := range st.Requests {
			if !strings.HasPrefix(r.Remote, "127.0.0.1:") {
				t.Fatalf("a request came from %s", r.Remote)
			}
			if strings.HasPrefix(r.Path, "/api/v1/") && r.Cookie && strings.Contains(r.Agent, "AppleWebKit") {
				appReqs++
			}
		}
		if appReqs == 0 {
			t.Fatalf("the window made no API requests with the local cookie: %+v", st.Requests)
		}
		src, _ := os.ReadFile(filepath.Join(root(t), "cmd", "fettle-desktop", "main.go"))
		if n := len(regexp.MustCompile(`application\.NewService\(`).FindAll(src, -1)); n != 1 || !strings.Contains(string(src), "application.NewService(s.notes)") {
			t.Fatalf("the app binds %d services; only the notifications service may be bound", n)
		}
		sm.must("js", `(() => { const a = document.createElement("a"); a.href = "https://docs.test/away"; a.textContent = "away"; document.body.appendChild(a); a.click(); a.remove(); })();`)
		sm.waitFor("another origin opens in the system browser", 10*time.Second, func(s state) bool {
			for _, u := range s.Opened {
				if u == "https://docs.test/away" {
					return true
				}
			}
			return false
		})
		sm.must("js", `(() => { const a = document.createElement("a"); a.href = "/screen/00000000-0000-0000-0000-000000000001"; a.textContent = "Screen"; document.body.appendChild(a); a.click(); a.remove(); })();`)
		sm.waitFor("a screen opens in its own window", 10*time.Second, func(s state) bool {
			for _, w := range s.Windows {
				if strings.HasPrefix(w, "screen-") {
					return true
				}
			}
			return false
		})
		if u := sm.must("state", "").Base; u != st.Base {
			t.Fatalf("the main window left the app origin: %s", u)
		}
		report(t, "SCN-UX-002", true, map[string]any{"api_requests": appReqs})
	}

	// SCN-UX-001: closing the window keeps the server; Start at login registers; the master
	// key is in the keychain; a test update installs after confirmation; Quit stops it.
	sm.must("close-window", "")
	sm.waitFor("the window hides", 10*time.Second, func(s state) bool { return !s.Visible })
	if code := a.do("GET", "/me", nil, nil); code != 200 {
		t.Fatalf("after the window closed the server answers %d", code)
	}
	sm.must("autostart-on", "")
	if !sm.must("state", "").Autostart {
		t.Fatal("Start at login did not register")
	}
	sm.must("autostart-off", "")
	if sm.must("state", "").Autostart {
		t.Fatal("Start at login did not unregister")
	}
	// A locked login keychain refuses access, so the key check needs an unlocked session.
	if !locked {
		if out, err := exec.Command("security", "find-generic-password", "-s", "Fettle", "-a", "master-key").CombinedOutput(); err != nil {
			t.Fatalf("the master key is not in the keychain: %v %s", err, out)
		}
		if _, err := os.Stat(filepath.Join(data, "master.key")); err == nil {
			t.Fatal("the master key is in a file, not only in the keychain")
		}
	}
	// The app checks the feed at start, so the question may wait already. With the window
	// closed to the tray the question waits for the window, so the window opens again.
	if !sm.must("state", "").Asked {
		sm.must("check-update", "")
		sm.must("show", "")
	}
	sm.waitFor("the update asks first", 60*time.Second, func(s state) bool { return s.Asked })
	if v := sm.must("state", "").Version; v != v1 {
		t.Fatalf("the update installed before the confirmation: version %s", v)
	}
	sm.must("install-update", "")
	exited := make(chan struct{})
	go func() { _, _ = cmd.Process.Wait(); close(exited) }()
	select {
	case <-exited:
	case <-time.After(60 * time.Second):
		t.Fatal("the old version did not exit after the update")
	}
	a.base = sm.waitFor("the new version runs", 90*time.Second, func(s state) bool { return s.Version == v2 }).Base
	if b, _ := os.ReadFile(filepath.Join(app1, "Contents", "Info.plist")); !strings.Contains(string(b), v2) {
		t.Fatal("the installed bundle is not the new version")
	}
	if _, err := sm.send("quit", ""); err != nil {
		t.Fatal(err)
	}
	stopped := false
	for i := 0; i < 100; i++ {
		if a.do("GET", "/me", nil, nil) == 0 {
			stopped = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !stopped {
		t.Fatal("Quit did not stop the server")
	}
	if locked {
		report(t, "SCN-UX-001", false, map[string]any{"skipped": "the screen is locked; the keychain refuses access", "update": v1 + " -> " + v2})
	} else {
		report(t, "SCN-UX-001", true, map[string]any{"update": v1 + " -> " + v2})
	}
	if locked {
		t.Fatal("the screen is locked: SCN-UX-002, SCN-UX-003, NFR-003 and the keychain check of SCN-UX-001 did not run; run just desktop in an unlocked session")
	}
}
