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

// mcpStartTimeout is how long agentd waits for the start message of a stdio MCP server.
const mcpStartTimeout = 30 * time.Second

// mcpStdio starts a stdio MCP server inside the computer and bridges its stdin and stdout
// over a websocket (Appendix B). The server runs as the computer user, never on the Fettle
// host (REQ-CON-002). The first text message is a client.MCPStdioRequest with the command,
// the folder and the variables: variables hold account secrets, and a URL query reaches
// logs. agentd answers with a client.MCPStdioReply, then bridges binary messages.
func (s *Server) mcpStdio(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	c.SetReadLimit(16 << 20)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	reply := func(rep client.MCPStdioReply) bool {
		b, _ := json.Marshal(rep)
		return c.Write(ctx, websocket.MessageText, b) == nil
	}
	refuse := func(err error) {
		status, msg := s.errorStatus("mcp", err)
		reply(client.MCPStdioReply{Status: status, Error: msg})
		_ = c.Close(websocket.StatusNormalClosure, "")
	}
	rctx, rcancel := context.WithTimeout(ctx, mcpStartTimeout)
	typ, data, err := c.Read(rctx)
	rcancel()
	if err != nil {
		_ = c.CloseNow()
		return
	}
	var req client.MCPStdioRequest
	if typ != websocket.MessageText || json.Unmarshal(data, &req) != nil || len(req.Argv) == 0 {
		refuse(badRequest("the first message must be JSON with argv, the program and its arguments"))
		return
	}
	cmd, _, err := s.buildCommand(client.ExecRequest{Argv: req.Argv, Cwd: req.Cwd, Env: req.Env})
	if err != nil {
		refuse(err)
		return
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		refuse(err)
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		refuse(err)
		return
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		refuse(badRequest("the MCP server did not start: %s", cleanMessage(err)))
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
	if !reply(client.MCPStdioReply{}) {
		return
	}
	conn := websocket.NetConn(ctx, c, websocket.MessageBinary)
	defer func() { _ = conn.Close() }()
	go func() {
		_, _ = io.Copy(stdin, conn)
		_ = stdin.Close()
		cancel()
	}()
	_, _ = io.Copy(conn, stdout)
}
