package client

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/coder/websocket"
)

// MCPStdio starts a stdio MCP server inside the computer and returns its stdin and stdout
// as one stream. Closing the stream stops the server.
func (c *Client) MCPStdio(ctx context.Context, argv []string, cwd string) (net.Conn, error) {
	cmd, err := json.Marshal(argv)
	if err != nil {
		return nil, err
	}
	u := strings.Replace(c.base, "https://", "wss://", 1)
	u = strings.Replace(u, "http://", "ws://", 1) + "/v1/mcp/stdio?" + url.Values{"cmd": {string(cmd)}, "cwd": {cwd}}.Encode()
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
	return websocket.NetConn(context.WithoutCancel(ctx), ws, websocket.MessageBinary), nil
}
