package client

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/coder/websocket"
)

// Screen is a running X display of a bot (§10.8).
type Screen struct {
	ID      string `json:"id"`
	Display int    `json:"display"`
}

// ScreenStart starts the screen id, or returns the running one.
func (c *Client) ScreenStart(ctx context.Context, id string) (Screen, error) {
	var out Screen
	err := c.doJSON(ctx, http.MethodPost, "/v1/screen/"+url.PathEscape(id)+"/start", nil, nil, &out)
	return out, err
}

// ScreenStop stops the screen id.
func (c *Client) ScreenStop(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodPost, "/v1/screen/"+url.PathEscape(id)+"/stop", nil, nil, "")
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// ScreenDial opens the VNC stream of a screen: view-only, or with input when control is
// true. The stream carries RFB bytes.
func (c *Client) ScreenDial(ctx context.Context, id string, control bool) (net.Conn, error) {
	mode := "view"
	if control {
		mode = "control"
	}
	u := strings.Replace(c.base, "https://", "wss://", 1)
	u = strings.Replace(u, "http://", "ws://", 1) + "/v1/screen/" + url.PathEscape(id) + "/" + mode
	ws, resp, err := websocket.Dial(ctx, u, &websocket.DialOptions{HTTPClient: c.http, HTTPHeader: http.Header{"Authorization": {"Bearer " + c.token}}, Subprotocols: []string{"binary"}})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		if resp != nil {
			return nil, &Error{Status: resp.StatusCode, Message: err.Error()}
		}
		return nil, err
	}
	ws.SetReadLimit(4 << 20)
	return websocket.NetConn(context.WithoutCancel(ctx), ws, websocket.MessageBinary), nil
}
