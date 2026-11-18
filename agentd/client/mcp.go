package client

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/coder/websocket"
)

// MCPStdio starts a stdio MCP server inside the computer and returns its stdin and stdout
// as one stream. The command, the folder and the variables go in the first message, never
// in the URL. Closing the stream stops the server.
func (c *Client) MCPStdio(ctx context.Context, req MCPStdioRequest) (net.Conn, error) {
	start, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	u := strings.Replace(c.base, "https://", "wss://", 1)
	u = strings.Replace(u, "http://", "ws://", 1) + "/v1/mcp/stdio"
	ws, resp, err := websocket.Dial(ctx, u, &websocket.DialOptions{HTTPClient: c.http, HTTPHeader: http.Header{"Authorization": {"Bearer " + c.token}}})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close() // the websocket owns the connection; this body is the handshake's
	}
	if err != nil {
		if resp != nil {
			return nil, &Error{Status: resp.StatusCode, Message: err.Error()}
		}
		return nil, err
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
	return websocket.NetConn(context.WithoutCancel(ctx), ws, websocket.MessageBinary), nil
}
