package agentd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/alternayte/fettle/agentd/client"
)

// mcpStdio starts a stdio MCP server inside the computer and bridges its stdin and stdout
// over a websocket (Appendix B). The server runs as the computer user, never on the Fettle
// host (REQ-CON-002). The command is the JSON argv in ?cmd=, the folder is ?cwd=.
func (s *Server) mcpStdio(w http.ResponseWriter, r *http.Request) {
	var argv []string
	if err := json.Unmarshal([]byte(r.URL.Query().Get("cmd")), &argv); err != nil || len(argv) == 0 {
		s.fail(w, "mcp", badRequest("cmd must be a JSON list with the program and its arguments"))
		return
	}
	cmd, _, err := s.buildCommand(client.ExecRequest{Argv: argv, Cwd: r.URL.Query().Get("cwd")})
	if err != nil {
		s.fail(w, "mcp", err)
		return
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		s.fail(w, "mcp", err)
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		s.fail(w, "mcp", err)
		return
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		s.fail(w, "mcp", badRequest("the MCP server did not start: %s", cleanMessage(err)))
		return
	}
	defer func() {
		_ = stdin.Close()
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			killGroup(cmd)
			<-done
		}
	}()
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	c.SetReadLimit(16 << 20)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	conn := websocket.NetConn(ctx, c, websocket.MessageBinary)
	defer func() { _ = conn.Close() }()
	go func() {
		_, _ = io.Copy(stdin, conn)
		_ = stdin.Close()
		cancel()
	}()
	_, _ = io.Copy(conn, stdout)
}
