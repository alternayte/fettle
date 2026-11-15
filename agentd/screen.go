package agentd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/alternayte/fettle/agentd/client"
)

// A screen is one X display of a bot (§10.8): Xvfb, a view-only VNC server and a VNC
// server that takes input. Both VNC servers listen only on the loopback; agentd bridges
// them to the Fettle server over websockets.
type screen struct {
	id      string
	display int
	procs   []*exec.Cmd
	view    int // view-only VNC port
	control int // VNC port with input
}

type screens struct {
	mu   sync.Mutex
	m    map[string]*screen
	next int
}

func newScreens() *screens { return &screens{m: map[string]*screen{}, next: 10} }

var screenID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// errNoScreens means the computer has no X server or VNC server.
var errNoScreens = &httpError{status: http.StatusNotImplemented, msg: "this computer has no screens; use the desktop size"}

func (s *screen) stop() {
	for _, p := range s.procs {
		killGroup(p)
		_ = p.Wait()
	}
	_ = os.Remove(fmt.Sprintf("/tmp/.X11-unix/X%d", s.display))
	_ = os.Remove(fmt.Sprintf("/tmp/.X%d-lock", s.display))
}

func (sc *screens) stopAll() {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	for id, s := range sc.m {
		s.stop()
		delete(sc.m, id)
	}
}

// startScreen starts the display of id, or returns the running one.
func (s *Server) startScreen(id string) (*screen, error) {
	if !screenID.MatchString(id) {
		return nil, badRequest("screen id %q is not valid", id)
	}
	sc := s.screens
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if x, ok := sc.m[id]; ok {
		return x, nil
	}
	for _, bin := range []string{"Xvfb", "x11vnc"} {
		if _, err := exec.LookPath(bin); err != nil {
			return nil, errNoScreens
		}
	}
	n := sc.next
	for ; n < 100; n++ {
		if _, err := os.Stat(fmt.Sprintf("/tmp/.X11-unix/X%d", n)); errors.Is(err, os.ErrNotExist) {
			break
		}
	}
	if n >= 100 {
		return nil, &httpError{status: http.StatusServiceUnavailable, msg: "no free display"}
	}
	sc.next = n + 1
	x := &screen{id: id, display: n, view: 6900 + n, control: 5900 + n}
	start := func(argv ...string) error {
		cmd, _, err := s.buildCommand(client.ExecRequest{Argv: argv})
		if err != nil {
			return err
		}
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
		if err := cmd.Start(); err != nil {
			return err
		}
		x.procs = append(x.procs, cmd)
		return nil
	}
	disp := ":" + strconv.Itoa(n)
	if err := start("Xvfb", disp, "-screen", "0", "1280x800x24", "-nolisten", "tcp"); err != nil {
		x.stop()
		return nil, err
	}
	if err := waitFor(func() bool {
		_, err := os.Stat(fmt.Sprintf("/tmp/.X11-unix/X%d", n))
		return err == nil
	}); err != nil {
		x.stop()
		return nil, &httpError{status: http.StatusServiceUnavailable, msg: "the display did not start"}
	}
	vnc := func(port int, extra ...string) error {
		return start(append([]string{"x11vnc", "-display", disp, "-rfbport", strconv.Itoa(port), "-localhost", "-forever", "-shared", "-nopw", "-quiet", "-noxdamage"}, extra...)...)
	}
	if err := vnc(x.view, "-viewonly"); err != nil {
		x.stop()
		return nil, err
	}
	if err := vnc(x.control); err != nil {
		x.stop()
		return nil, err
	}
	for _, p := range []int{x.view, x.control} {
		if err := waitFor(func() bool {
			c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p), time.Second)
			if err == nil {
				_ = c.Close()
			}
			return err == nil
		}); err != nil {
			x.stop()
			return nil, &httpError{status: http.StatusServiceUnavailable, msg: "the VNC server did not start"}
		}
	}
	sc.m[id] = x
	return x, nil
}

func waitFor(ok func() bool) error {
	deadline := time.Now().Add(15 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			return errors.New("timeout")
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil
}

func (s *Server) screenStart(w http.ResponseWriter, r *http.Request) {
	x, err := s.startScreen(r.PathValue("id"))
	if err != nil {
		s.fail(w, "screen", err)
		return
	}
	writeJSON(w, http.StatusOK, client.Screen{ID: x.id, Display: x.display})
}

func (s *Server) screenStop(w http.ResponseWriter, r *http.Request) {
	sc := s.screens
	sc.mu.Lock()
	x, ok := sc.m[r.PathValue("id")]
	delete(sc.m, r.PathValue("id"))
	sc.mu.Unlock()
	if ok {
		x.stop()
	}
	w.WriteHeader(http.StatusNoContent)
}

// screenStream bridges a websocket to the view-only or the control VNC server of a screen.
func (s *Server) screenStream(control bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.screens.mu.Lock()
		x, ok := s.screens.m[r.PathValue("id")]
		s.screens.mu.Unlock()
		if !ok {
			s.fail(w, "screen", &httpError{status: http.StatusNotFound, msg: "the screen is not running"})
			return
		}
		port := x.view
		if control {
			port = x.control
		}
		vnc, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 5*time.Second)
		if err != nil {
			s.fail(w, "screen", &httpError{status: http.StatusServiceUnavailable, msg: "the VNC server does not answer"})
			return
		}
		defer func() { _ = vnc.Close() }()
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"binary"}})
		if err != nil {
			return
		}
		c.SetReadLimit(4 << 20)
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		conn := websocket.NetConn(ctx, c, websocket.MessageBinary)
		defer func() { _ = conn.Close() }()
		go func() {
			_, _ = io.Copy(vnc, conn)
			cancel()
			_ = vnc.Close()
		}()
		_, _ = io.Copy(conn, vnc)
	}
}
