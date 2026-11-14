package client

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/url"
	"time"
)

// BrowserContextRequest is the body of POST /v1/browser/contexts. A persistent context keeps
// its profile in the bot home; an ephemeral context gets a temporary profile.
type BrowserContextRequest struct {
	ID        string `json:"id"`
	Profile   string `json:"profile,omitempty"`
	Downloads string `json:"downloads,omitempty"`
	Ephemeral bool   `json:"ephemeral,omitempty"`
}

// Element is one interactive element of a snapshot.
type Element struct {
	Ref      string `json:"ref"`
	Role     string `json:"role"`
	Name     string `json:"name"`
	Value    string `json:"value,omitempty"`
	Masked   bool   `json:"masked,omitempty"`
	Checked  *bool  `json:"checked,omitempty"`
	Href     string `json:"href,omitempty"`
	Form     string `json:"form,omitempty"`
	Submits  bool   `json:"submits,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
}

// Snapshot is the reply of the snapshot and navigate endpoints (§10.7).
type Snapshot struct {
	URL       string    `json:"url"`
	Title     string    `json:"title"`
	Text      string    `json:"text"`
	Truncated bool      `json:"truncated"`
	Elements  []Element `json:"elements"`
	Dialogs   []string  `json:"dialogs,omitempty"`
}

// NavigateRequest is the body of POST /v1/browser/{ctx}/navigate.
type NavigateRequest struct {
	URL string `json:"url"`
}

// ActRequest is the body of POST /v1/browser/{ctx}/act. ExpectRole and ExpectName are the
// element that the server checked; agentd refuses when the element changed.
type ActRequest struct {
	Ref        string `json:"ref,omitempty"`
	Action     string `json:"action"`
	Value      string `json:"value,omitempty"`
	ExpectRole string `json:"expect_role,omitempty"`
	ExpectName string `json:"expect_name,omitempty"`
}

// ActResult reports what is confirmed and what is uncertain after an action. Refused is set
// when agentd did not act.
type ActResult struct {
	Refused   string    `json:"refused,omitempty"`
	Confirmed []string  `json:"confirmed,omitempty"`
	Uncertain []string  `json:"uncertain,omitempty"`
	Dialogs   []string  `json:"dialogs,omitempty"`
	Snapshot  *Snapshot `json:"snapshot,omitempty"`
}

// Screenshot is the reply of POST /v1/browser/{ctx}/screenshot. PNG is base64.
type Screenshot struct {
	URL string `json:"url"`
	PNG string `json:"png"`
}

// Download is one finished download. Path is relative to the home folder for persistent contexts.
type Download struct {
	Name    string    `json:"name"`
	Path    string    `json:"path,omitempty"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

// BrowserOpen creates a browser context, or returns the existing one with the same ID.
func (c *Client) BrowserOpen(ctx context.Context, req BrowserContextRequest) error {
	var out map[string]string
	return c.doJSON(ctx, http.MethodPost, "/v1/browser/contexts", nil, req, &out)
}

// BrowserClose ends a browser context.
func (c *Client) BrowserClose(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/v1/browser/contexts/"+url.PathEscape(id), nil, nil, "")
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// BrowserNavigate loads a URL and returns the snapshot of the page.
func (c *Client) BrowserNavigate(ctx context.Context, id, u string) (Snapshot, error) {
	var s Snapshot
	err := c.doJSON(ctx, http.MethodPost, "/v1/browser/"+url.PathEscape(id)+"/navigate", nil, NavigateRequest{URL: u}, &s)
	return s, err
}

// BrowserSnapshot returns the snapshot of the current page. agentd remembers it as what the
// bot saw.
func (c *Client) BrowserSnapshot(ctx context.Context, id string) (Snapshot, error) {
	var s Snapshot
	err := c.doJSON(ctx, http.MethodPost, "/v1/browser/"+url.PathEscape(id)+"/snapshot", nil, struct{}{}, &s)
	return s, err
}

// BrowserPeek returns the snapshot of the current page without changing what the bot saw.
func (c *Client) BrowserPeek(ctx context.Context, id string) (Snapshot, error) {
	var s Snapshot
	err := c.doJSON(ctx, http.MethodPost, "/v1/browser/"+url.PathEscape(id)+"/snapshot", url.Values{"peek": {"1"}}, struct{}{}, &s)
	return s, err
}

// BrowserAct runs one action.
func (c *Client) BrowserAct(ctx context.Context, id string, req ActRequest) (ActResult, error) {
	var r ActResult
	err := c.doJSON(ctx, http.MethodPost, "/v1/browser/"+url.PathEscape(id)+"/act", nil, req, &r)
	return r, err
}

// BrowserScreenshot returns a PNG of the viewport and the page URL.
func (c *Client) BrowserScreenshot(ctx context.Context, id string) ([]byte, string, error) {
	var s Screenshot
	if err := c.doJSON(ctx, http.MethodPost, "/v1/browser/"+url.PathEscape(id)+"/screenshot", nil, struct{}{}, &s); err != nil {
		return nil, "", err
	}
	png, err := base64.StdEncoding.DecodeString(s.PNG)
	return png, s.URL, err
}

// BrowserDownloads lists the finished downloads of a context, newest first.
func (c *Client) BrowserDownloads(ctx context.Context, id string) ([]Download, error) {
	var out []Download
	err := c.doJSON(ctx, http.MethodPost, "/v1/browser/"+url.PathEscape(id)+"/downloads", nil, struct{}{}, &out)
	return out, err
}
