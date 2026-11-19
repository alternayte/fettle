package agentd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
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
// logs. agentd answers with a client.MCPStdioReply, then bridges binary messages. When the
// server stops, agentd sends a client.MCPStdioExit and closes the websocket.
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
	stderr := &tailBuffer{max: mcpStderrTail}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		refuse(badRequest("the MCP server did not start: %s", cleanMessage(err)))
		return
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	stop := func() {
		_ = stdin.Close()
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			killGroup(cmd)
			<-exited
		}
	}
	if !reply(client.MCPStdioReply{}) {
		stop()
		return
	}
	conn := websocket.NetConn(ctx, c, websocket.MessageBinary)
	go func() {
		_, _ = io.Copy(stdin, conn)
		_ = stdin.Close()
		cancel()
	}()
	_, _ = io.Copy(conn, stdout)
	stop()
	// The server stopped. Tell the caller how, so that a server that fails at start shows
	// its own message instead of a closed stream.
	b, _ := json.Marshal(client.MCPStdioExit{ExitCode: cmd.ProcessState.ExitCode(), Stderr: stderr.String()})
	_ = c.Write(ctx, websocket.MessageText, b)
	_ = c.Close(websocket.StatusNormalClosure, "")
}

// mcpStderrTail is how much of the end of the error output of an MCP server agentd keeps.
const mcpStderrTail = 4 << 10

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if over := len(t.b) - t.max; over > 0 {
		t.b = append(t.b[:0], t.b[over:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.ToValidUTF8(string(t.b), "")
}
