//go:build desktop && smoke

package main

import (
	"bufio"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// The smoke build (tags desktop and smoke) adds a control socket, so the desktop smoke test
// (§24, layer D) drives the real window, tray and update code. Release builds lack it.

// smokeAgent runs in the page: it fetches queued test scripts and runs them as inline
// scripts, which the UI's CSP allows. Wails runs ExecJS only after its own JS runtime
// starts, and the Fettle UI does not load that runtime.
const smokeAgent = `<script>(function(){setInterval(function(){fetch("/__smoke/next").then(function(r){return r.status===200?r.text():null}).then(function(code){if(!code)return;var s=document.createElement("script");s.textContent=code;document.head.appendChild(s)}).catch(function(){})},300)})();</script>`

type smokeState struct {
	js       chan string
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

var st = &smokeState{js: make(chan string, 64)}

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
				if r.URL.Path == "/__smoke/next" {
					select {
					case code := <-st.js:
						w.Header().Set("Content-Type", "text/javascript")
						_, _ = w.Write([]byte(code))
					default:
						w.WriteHeader(http.StatusNoContent)
					}
					return
				}
				if r.Method == http.MethodGet && !strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, "/assets/") {
					rec := httptest.NewRecorder()
					h.ServeHTTP(rec, r)
					body := rec.Body.Bytes()
					if strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
						body = []byte(strings.Replace(string(body), "</head>", smokeAgent+"</head>", 1))
					}
					for k, v := range rec.Header() {
						w.Header()[k] = v
					}
					w.Header().Del("Content-Length")
					w.WriteHeader(rec.Code)
					_, _ = w.Write(body)
					return
				}
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
			s.mu.Lock()
			out["visible"] = !s.hidden
			out["base"] = s.base
			s.mu.Unlock()
			out["windows"] = names
			out["opened"] = append([]string(nil), st.opened...)
			out["asked"] = st.asked
			out["requests"] = append([]smokeRequest(nil), st.requests...)
			st.mu.Unlock()
			out["autostart"] = on
			out["version"] = version
		case "close-window":
			application.InvokeSync(func() { s.win.Close() })
		case "show":
			application.InvokeSync(func() { s.show("") })
		case "js":
			st.js <- cmd.Arg
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
