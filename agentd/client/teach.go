package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// TeachStart attaches a teach recorder to a browser context and sets the folder of the
// terminal (§13.3).
type TeachStart struct {
	Browser string `json:"browser"`
	Cwd     string `json:"cwd"`
}

// Secret kinds of a teach step. A step with a secret kind has no value.
const (
	SecretPassword    = "password"
	SecretCard        = "card"
	SecretOneTimeCode = "one_time_code"
	SecretEchoOff     = "echo_off"
)

// TeachStep is one action that a person did while teaching: a browser step or a command
// step. Secret field values and terminal input typed while echo is off are never in it.
type TeachStep struct {
	Seq  int       `json:"seq"`
	At   time.Time `json:"at"`
	Kind string    `json:"kind"` // browser or command
	// Browser steps.
	URL    string `json:"url,omitempty"`
	Action string `json:"action,omitempty"` // navigate, click, type, select, check, press
	Role   string `json:"role,omitempty"`
	Name   string `json:"name,omitempty"`
	Value  string `json:"value,omitempty"`
	// Secret is the kind of a secret field or input; the step then has no value.
	Secret string `json:"secret,omitempty"`
	// Screenshot is a JPEG of the page when the step happened, base64.
	Screenshot string `json:"screenshot,omitempty"`
	// Command steps. Cwd is relative to the computer home.
	Command  string `json:"command,omitempty"`
	Cwd      string `json:"cwd,omitempty"`
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output,omitempty"`
}

// TeachStepsReply is a list of recorded steps.
type TeachStepsReply struct {
	Steps []TeachStep `json:"steps"`
}

// TeachStart starts the teach recorder id, or keeps the running one. It reports whether it
// made a new recorder, whose steps count from 1 again.
func (c *Client) TeachStart(ctx context.Context, id string, req TeachStart) (bool, error) {
	var out struct {
		Created bool `json:"created"`
	}
	err := c.doJSON(ctx, http.MethodPost, "/v1/teach/"+url.PathEscape(id)+"/start", nil, req, &out)
	return out.Created, err
}

// TeachSteps returns the steps after seq after. agentd forgets the steps up to after.
func (c *Client) TeachSteps(ctx context.Context, id string, after int) ([]TeachStep, error) {
	var out TeachStepsReply
	err := c.doJSON(ctx, http.MethodGet, "/v1/teach/"+url.PathEscape(id)+"/steps", url.Values{"after": {strconv.Itoa(after)}}, nil, &out)
	return out.Steps, err
}

// TeachStop stops the recorder and returns the steps after seq after.
func (c *Client) TeachStop(ctx context.Context, id string, after int) ([]TeachStep, error) {
	var out TeachStepsReply
	err := c.doJSON(ctx, http.MethodPost, "/v1/teach/"+url.PathEscape(id)+"/stop", url.Values{"after": {strconv.Itoa(after)}}, nil, &out)
	return out.Steps, err
}

// TeachPTY opens the recorded terminal of a teach session. Binary messages carry terminal
// bytes both ways; a text message {"cols":n,"rows":n} resizes the terminal.
func (c *Client) TeachPTY(ctx context.Context, id string, cols, rows int) (*websocket.Conn, error) {
	u := strings.Replace(c.base, "https://", "wss://", 1)
	u = strings.Replace(u, "http://", "ws://", 1) + "/v1/teach/" + url.PathEscape(id) + "/pty?" + url.Values{"cols": {strconv.Itoa(cols)}, "rows": {strconv.Itoa(rows)}}.Encode()
	ws, resp, err := websocket.Dial(ctx, u, &websocket.DialOptions{HTTPClient: c.http, HTTPHeader: http.Header{"Authorization": {"Bearer " + c.token}}})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		if resp != nil {
			return nil, &Error{Status: resp.StatusCode, Message: err.Error()}
		}
		return nil, err
	}
	ws.SetReadLimit(1 << 20)
	return ws, nil
}
