//go:build desktop && smoke

package main

import (
	"bufio"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// The smoke build (tags desktop and smoke) adds a control socket, so the desktop smoke test
// (§24, layer D) drives the real window, tray and update code. Release builds lack it.

type smokeState struct {
	mu       sync.Mutex
	opened   []string
	asked    bool
	install  func()
	requests []smokeRequest
}

type smokeRequest struct {
	Path   string `json:"path"`
	Remote string `json:"remote"`
	Cookie bool   `json:"cookie"`
	Agent  string `json:"agent"`
}

var st = &smokeState{}

func init() {
	smokeEarly = func(s *shell) {
		s.openURL = func(u string) error {
			st.mu.Lock()
			st.opened = append(st.opened, u)
			st.mu.Unlock()
			return nil
		}
		s.asked = func(install func()) {
			st.mu.Lock()
			st.asked, st.install = true, install
			st.mu.Unlock()
		}
		s.wrap = func(h http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/api/") {
					_, err := r.Cookie("fettle_local")
					st.mu.Lock()
					if len(st.requests) < 500 {
						st.requests = append(st.requests, smokeRequest{Path: r.URL.Path, Remote: r.RemoteAddr, Cookie: err == nil, Agent: r.UserAgent()})
					}
					st.mu.Unlock()
				}
				h.ServeHTTP(w, r)
			})
		}
	}
	smoke = func(s *shell) {
		sock := filepath.Join(s.data, "smoke.sock")
		_ = os.Remove(sock)
		l, err := net.Listen("unix", sock)
		if err != nil {
			panic(err)
		}
		go func() {
			for {
				c, err := l.Accept()
				if err != nil {
					return
				}
				go serveSmoke(s, c)
			}
		}()
	}
}

type smokeCmd struct {
	Cmd string `json:"cmd"`
	Arg string `json:"arg,omitempty"`
}

func serveSmoke(s *shell, c net.Conn) {
	defer c.Close()
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	enc := json.NewEncoder(c)
	for sc.Scan() {
		var cmd smokeCmd
		if json.Unmarshal(sc.Bytes(), &cmd) != nil {
			_ = enc.Encode(map[string]string{"error": "bad command"})
			continue
		}
		out := map[string]any{}
		switch cmd.Cmd {
		case "state":
			var names []string
			for _, w := range s.wails.Window.GetAll() {
				names = append(names, w.Name())
			}
			on, _ := s.wails.Autostart.IsEnabled()
			st.mu.Lock()
			out["visible"] = s.win.IsVisible()
			out["windows"] = names
			out["opened"] = append([]string(nil), st.opened...)
			out["asked"] = st.asked
			out["requests"] = append([]smokeRequest(nil), st.requests...)
			st.mu.Unlock()
			out["autostart"] = on
			out["version"] = version
			out["base"] = s.base
		case "close-window":
			application.InvokeSync(func() { s.win.Close() })
		case "show":
			application.InvokeSync(func() { s.show("") })
		case "js":
			application.InvokeSync(func() { s.win.ExecJS(cmd.Arg) })
		case "autostart-on":
			out["error"] = errString(s.wails.Autostart.Enable())
		case "autostart-off":
			out["error"] = errString(s.wails.Autostart.Disable())
		case "check-update":
			go s.updates()
		case "install-update":
			st.mu.Lock()
			f := st.install
			st.mu.Unlock()
			if f == nil {
				out["error"] = "no update dialog was shown"
			} else {
				f()
			}
		case "quit":
			_ = enc.Encode(out)
			application.InvokeSync(func() { s.wails.Quit() })
			return
		default:
			out["error"] = "unknown command"
		}
		_ = enc.Encode(out)
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
