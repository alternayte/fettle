package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/coder/websocket"
)

// MCPStdio starts a stdio MCP server inside the computer and returns its stdin and stdout
// as one stream. The command, the folder and the variables go in the first message, never
// in the URL. Closing the stream stops the server. When the server stops by itself, Read
// returns an *MCPExitError with its exit code and the end of its error output.
func (c *Client) MCPStdio(ctx context.Context, req MCPStdioRequest) (io.ReadWriteCloser, error) {
	start, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	u := strings.Replace(c.base, "https://", "wss://", 1)
	u = strings.Replace(u, "http://", "ws://", 1) + "/v1/mcp/stdio"
	ws, resp, err := websocket.Dial(ctx, u, &websocket.DialOptions{HTTPClient: c.http, HTTPHeader: http.Header{"Authorization": {"Bearer " + c.token}}})
	if err != nil {
		return nil, dialError(resp, err)
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close() // the websocket owns the connection; this body is the handshake's
	}
	ws.SetReadLimit(16 << 20)
	if err := ws.Write(ctx, websocket.MessageText, start); err != nil {
		_ = ws.CloseNow()
		return nil, err
	}
	typ, data, err := ws.Read(ctx)
	if err != nil {
		_ = ws.CloseNow()
		return nil, err
	}
	var rep MCPStdioReply
	if typ != websocket.MessageText || json.Unmarshal(data, &rep) != nil {
		_ = ws.CloseNow()
		return nil, errors.New("agentd: the MCP start reply is not valid")
	}
	if rep.Error != "" {
		_ = ws.Close(websocket.StatusNormalClosure, "")
		return nil, &Error{Status: rep.Status, Message: rep.Error}
	}
	return &mcpStream{ws: ws, ctx: context.WithoutCancel(ctx)}, nil
}

// MCPExitError is the end of a stdio MCP server that stopped by itself.
type MCPExitError struct {
	ExitCode int
	Stderr   string
}

func (e *MCPExitError) Error() string {
	msg := fmt.Sprintf("the MCP server stopped with exit code %d", e.ExitCode)
	if s := strings.TrimSpace(e.Stderr); s != "" {
		msg += ". Its error output ends with:\n" + s
	} else {
		msg += " and wrote no error output"
	}
	return msg
}

// mcpStream carries stdin and stdout as binary messages. The text message at the end holds
// the exit of the server.
type mcpStream struct {
	ws  *websocket.Conn
	ctx context.Context
	r   io.Reader // the binary message that Read is in
	err error     // the error of every Read after the end
}

func (s *mcpStream) Read(p []byte) (int, error) {
	for {
		if s.r != nil {
			n, err := s.r.Read(p)
			if errors.Is(err, io.EOF) {
				s.r = nil
				if n == 0 {
					continue
				}
				err = nil
			}
			return n, err
		}
		if s.err != nil {
			return 0, s.err
		}
		typ, r, err := s.ws.Reader(s.ctx)
		switch {
		case websocket.CloseStatus(err) == websocket.StatusNormalClosure:
			s.err = io.EOF
		case err != nil:
			s.err = err
		case typ == websocket.MessageText:
			var ex MCPStdioExit
			b, _ := io.ReadAll(io.LimitReader(r, 64<<10))
			if json.Unmarshal(b, &ex) != nil {
				s.err = errors.New("agentd: the MCP exit message is not valid")
			} else {
				s.err = &MCPExitError{ExitCode: ex.ExitCode, Stderr: ex.Stderr}
			}
		default:
			s.r = r
		}
	}
}

func (s *mcpStream) Write(p []byte) (int, error) {
	if err := s.ws.Write(s.ctx, websocket.MessageBinary, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (s *mcpStream) Close() error {
	return s.ws.Close(websocket.StatusNormalClosure, "")
}
