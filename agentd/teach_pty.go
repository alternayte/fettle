//go:build linux || darwin

package agentd

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"syscall"

	"github.com/coder/websocket"
	"github.com/creack/pty"
	"golang.org/x/sys/unix"

	"github.com/alternayte/fettle/agentd/client"
)

// teachRC is the rc file of the teach terminal. It keeps the person's own .bashrc and adds
// shell integration: a mark when a command starts (PS0, or a DEBUG trap on bash before
// 4.4) with the command from the history, and a mark at each prompt with the exit code
// and the folder. The marks are OSC sequences that a terminal ignores.
const teachRC = `[ -f "$HOME/.bashrc" ] && . "$HOME/.bashrc"
unset HISTFILE
HISTCONTROL=
HISTIGNORE=
__fettle_b64() { printf %s "$1" | base64 | tr -d '\n'; }
__fettle_start() {
  local c
  c=$(HISTTIMEFORMAT= history 1)
  c="${c#"${c%%[![:space:]]*}"}"
  c="${c#*[[:space:]]}"
  c="${c#"${c%%[![:space:]]*}"}"
  printf '\033]6973;C;%s\007' "$(__fettle_b64 "$c")"
}
__fettle_prompt() {
  local ec=$?
  __fettle_in=
  printf '\033]6973;D;%s;%s\007' "$ec" "$(__fettle_b64 "$PWD")"
}
PROMPT_COMMAND="__fettle_prompt${PROMPT_COMMAND:+;$PROMPT_COMMAND}"
if [ "${BASH_VERSINFO[0]}" -gt 4 ] || { [ "${BASH_VERSINFO[0]}" -eq 4 ] && [ "${BASH_VERSINFO[1]}" -ge 4 ]; }; then
  PS0='$(__fettle_start)'
else
  trap '[ -z "$__fettle_in" ] && [ "${BASH_COMMAND#__fettle}" = "$BASH_COMMAND" ] && { __fettle_in=1; __fettle_start; }' DEBUG
fi
`

// secretInput reports whether the terminal reads input that it does not show: echo off in
// canonical mode, as in a password prompt. A shell line editor turns echo off too, but it
// also leaves canonical mode, so it does not count.
func secretInput(tty *os.File) bool {
	t, err := unix.IoctlGetTermios(int(tty.Fd()), ioctlGetTermios)
	if err != nil {
		return false
	}
	return t.Lflag&unix.ECHO == 0 && t.Lflag&unix.ICANON != 0
}

// teachPTY runs a recorded shell in the teach folder and bridges it over a websocket.
func (s *Server) teachPTY(w http.ResponseWriter, r *http.Request) {
	t, ok := s.teachOf(w, r)
	if !ok {
		return
	}
	cols, _ := strconv.Atoi(r.URL.Query().Get("cols"))
	rows, _ := strconv.Atoi(r.URL.Query().Get("rows"))
	if cols < 20 || cols > 500 {
		cols = 100
	}
	if rows < 5 || rows > 200 {
		rows = 30
	}
	rc, err := os.CreateTemp("", "fettle-teach-*.rc")
	if err != nil {
		s.fail(w, "teach", err)
		return
	}
	defer func() { _ = os.Remove(rc.Name()) }()
	if _, err := rc.WriteString(teachRC); err != nil {
		_ = rc.Close()
		s.fail(w, "teach", err)
		return
	}
	_ = rc.Close()
	cmd, _, err := s.buildCommand(client.ExecRequest{Argv: []string{"bash", "--rcfile", rc.Name(), "-i"}, Cwd: t.cwd})
	if err != nil {
		s.fail(w, "teach", err)
		return
	}
	cmd.Env = append(cmd.Env, "TERM=xterm-256color")
	ptmx, tty, err := pty.Open()
	if err != nil {
		s.fail(w, "teach", err)
		return
	}
	defer func() { _ = ptmx.Close(); _ = tty.Close() }()
	_ = pty.Setsize(ptmx, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		s.fail(w, "teach", badRequest("the terminal did not start: %s", cleanMessage(err)))
		return
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		killGroup(cmd)
		<-exited
		return
	}
	c.SetReadLimit(1 << 20)
	ctx, cancel := context.WithCancel(r.Context())
	t.mu.Lock()
	t.ptyNext++
	key := t.ptyNext
	t.ptys[key] = cancel
	t.mu.Unlock()
	defer func() {
		cancel()
		killGroup(cmd)
		<-exited
		t.mu.Lock()
		delete(t.ptys, key)
		t.mu.Unlock()
		_ = c.CloseNow()
	}()
	rec := &ptyRecorder{s: s, t: t, cwd: t.cwd}
	// The person's input: binary messages go to the shell, text messages resize it.
	go func() {
		defer cancel()
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if typ == websocket.MessageText {
				var sz struct {
					Cols int `json:"cols"`
					Rows int `json:"rows"`
				}
				if json.Unmarshal(data, &sz) == nil && sz.Cols >= 20 && sz.Cols <= 500 && sz.Rows >= 5 && sz.Rows <= 200 {
					_ = pty.Setsize(ptmx, &pty.Winsize{Cols: uint16(sz.Cols), Rows: uint16(sz.Rows)})
				}
				continue
			}
			if secretInput(tty) {
				rec.secret()
			}
			if _, err := ptmx.Write(data); err != nil {
				return
			}
		}
	}()
	go func() {
		<-exited
		cancel()
	}()
	buf := make([]byte, 32<<10)
	out := make(chan []byte, 16)
	go func() {
		defer close(out)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				b := append([]byte(nil), buf[:n]...)
				rec.feed(b)
				select {
				case out <- b:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			_ = c.Close(websocket.StatusNormalClosure, "the terminal ended")
			return
		case b, ok := <-out:
			if !ok {
				_ = c.Close(websocket.StatusNormalClosure, "the terminal ended")
				return
			}
			if err := c.Write(ctx, websocket.MessageBinary, b); err != nil {
				return
			}
		}
	}
}
