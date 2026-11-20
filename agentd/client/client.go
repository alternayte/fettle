// Package client is the Fettle server's client for agentd, the daemon inside
// each computer. It talks HTTPS and trusts only the pinned certificate of
// that computer.
package client

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// ErrUnauthorized matches an *Error with status 401.
var ErrUnauthorized = errors.New("agentd: unauthorized")

// ErrNotFound matches an *Error with status 404.
var ErrNotFound = errors.New("agentd: not found")

// Error is a non-2xx reply from agentd.
// Protocol is the agentd protocol of this build. Change it with every change to an endpoint
// that an older server or agentd cannot handle; the server refuses a computer that differs.
const Protocol = 2

type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("agentd: %d: %s", e.Status, e.Message)
}

// Is makes errors.Is match ErrUnauthorized and ErrNotFound by status.
func (e *Error) Is(target error) bool {
	switch target {
	case ErrUnauthorized:
		return e.Status == http.StatusUnauthorized
	case ErrNotFound:
		return e.Status == http.StatusNotFound
	case ErrTooLarge:
		return e.Status == http.StatusRequestEntityTooLarge
	}
	return false
}

// Client calls one agentd.
type Client struct {
	base  string
	token string
	http  *http.Client
}

// New returns a client for an agentd at an https base URL. It trusts only
// the certificate in pinnedCertPEM: the server's leaf certificate must equal
// it byte for byte. System roots are not used.
//
// dial, when given, opens the connection under TLS, for example a stream through a
// provider's API (kiln); the TLS and the pin stay end to end.
func New(baseURL, token string, pinnedCertPEM []byte, dial ...DialFunc) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("agentd client: base URL: %w", err)
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("agentd client: base URL must use https, got %q", u.Scheme)
	}
	block, _ := pem.Decode(pinnedCertPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("agentd client: pinned certificate is not a PEM CERTIFICATE block")
	}
	if _, err := x509.ParseCertificate(block.Bytes); err != nil {
		return nil, fmt.Errorf("agentd client: pinned certificate: %w", err)
	}
	pinned := block.Bytes
	tlsConf := &tls.Config{
		MinVersion: tls.VersionTLS13,
		// Chain and hostname checks are replaced by the exact pin below.
		InsecureSkipVerify: true, //nolint:gosec // the certificate is pinned in VerifyConnection
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("agentd client: server sent no certificate")
			}
			if !bytes.Equal(cs.PeerCertificates[0].Raw, pinned) {
				return errors.New("agentd client: server certificate does not match the pinned certificate")
			}
			return nil
		},
	}
	c := newClient(baseURL, token, tlsConf)
	if len(dial) > 0 && dial[0] != nil {
		c.http.Transport.(*http.Transport).DialContext = dial[0]
	}
	return c, nil
}

// DialFunc opens the raw connection to agentd.
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// NewInsecureForTest returns a client for a plain HTTP agentd. Only tests use it.
func NewInsecureForTest(baseURL, token string) *Client {
	return newClient(baseURL, token, nil)
}

func newClient(baseURL, token string, tlsConf *tls.Config) *Client {
	tr := &http.Transport{
		Proxy:               nil,
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig:     tlsConf,
		TLSHandshakeTimeout: 10 * time.Second,
		IdleConnTimeout:     90 * time.Second,
		MaxIdleConnsPerHost: 4,
		ForceAttemptHTTP2:   tlsConf != nil,
	}
	return &Client{base: strings.TrimRight(baseURL, "/"), token: token, http: &http.Client{Transport: tr}}
}

// Health returns the health of agentd.
func (c *Client) Health(ctx context.Context) (Health, error) {
	var h Health
	err := c.doJSON(ctx, http.MethodGet, "/v1/health", nil, nil, &h)
	return h, err
}

// Exec runs a command and calls onChunk for each piece of output in order.
// onChunk may be nil. stream is "stdout" or "stderr".
func (c *Client) Exec(ctx context.Context, req ExecRequest, onChunk func(stream, data string)) (ExecResult, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return ExecResult{}, err
	}
	resp, err := c.do(ctx, http.MethodPost, "/v1/exec", nil, bytes.NewReader(body), "application/json")
	if err != nil {
		return ExecResult{}, err
	}
	defer resp.Body.Close()
	br := bufio.NewReader(resp.Body)
	for {
		line, err := br.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var l ExecLine
			if jerr := json.Unmarshal(line, &l); jerr != nil {
				return ExecResult{}, fmt.Errorf("agentd exec: bad stream line: %w", jerr)
			}
			if l.ExecResult != nil {
				return *l.ExecResult, nil
			}
			if onChunk != nil {
				onChunk(l.Stream, l.Data)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return ExecResult{}, errors.New("agentd exec: stream ended without a result")
			}
			return ExecResult{}, fmt.Errorf("agentd exec: %w", err)
		}
	}
}

// ReadFile returns the content of a file relative to the home folder.
func (c *Client) ReadFile(ctx context.Context, path string) ([]byte, error) {
	resp, err := c.do(ctx, http.MethodGet, "/v1/files", url.Values{"path": {path}}, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// WriteFile writes a file atomically and creates its parent folders. A zero
// mode keeps the mode of an existing file, or uses 0644 for a new one.
func (c *Client) WriteFile(ctx context.Context, path string, data []byte, mode os.FileMode) error {
	q := url.Values{"path": {path}}
	if mode != 0 {
		q.Set("mode", fmt.Sprintf("%04o", uint32(mode.Perm())))
	}
	resp, err := c.do(ctx, http.MethodPut, "/v1/files", q, bytes.NewReader(data), "application/octet-stream")
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// DeleteFile deletes a file or an empty folder, or a whole tree when recursive is true.
func (c *Client) DeleteFile(ctx context.Context, path string, recursive bool) error {
	q := url.Values{"path": {path}}
	if recursive {
		q.Set("recursive", "1")
	}
	resp, err := c.do(ctx, http.MethodDelete, "/v1/files", q, nil, "")
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// List returns the entries of a folder relative to the home folder. An
// empty path lists the home folder.
func (c *Client) List(ctx context.Context, path string, recursive bool) ([]Entry, error) {
	q := url.Values{"path": {path}, "recursive": {"0"}}
	if recursive {
		q.Set("recursive", "1")
	}
	var r ListReply
	if err := c.doJSON(ctx, http.MethodGet, "/v1/files/list", q, nil, &r); err != nil {
		return nil, err
	}
	return r.Entries, nil
}

// Stat returns one entry, or an error that matches ErrNotFound.
func (c *Client) Stat(ctx context.Context, path string) (Entry, error) {
	var e Entry
	err := c.doJSON(ctx, http.MethodGet, "/v1/files/stat", url.Values{"path": {path}}, nil, &e)
	return e, err
}

// Export returns a tar stream compressed with zstd of a workspace folder.
// The caller closes it. exclude adds patterns to the default exclusions.
func (c *Client) Export(ctx context.Context, path string, exclude []string) (io.ReadCloser, error) {
	return c.ExportLimited(ctx, path, exclude, 0)
}

// ErrTooLarge matches an *Error with status 413: the workspace is over its limit.
var ErrTooLarge = errors.New("agentd: workspace over its limit")

// ExportLimited streams a workspace like Export, but refuses (ErrTooLarge) when its files
// hold more than maxBytes after exclusions.
func (c *Client) ExportLimited(ctx context.Context, path string, exclude []string, maxBytes int64) (io.ReadCloser, error) {
	body, err := json.Marshal(WorkspaceRequest{Path: path, Exclude: exclude, MaxBytes: maxBytes})
	if err != nil {
		return nil, err
	}
	resp, err := c.do(ctx, http.MethodPost, "/v1/workspace/export", nil, bytes.NewReader(body), "application/json")
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// Import replaces the contents of a workspace folder with a tar stream
// compressed with zstd.
func (c *Client) Import(ctx context.Context, path string, r io.Reader) error {
	resp, err := c.do(ctx, http.MethodPost, "/v1/workspace/import", url.Values{"path": {path}}, r, "application/zstd")
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// Manifest returns the SHA-256 hex of each file in a workspace folder, keyed
// by the path relative to that folder.
func (c *Client) Manifest(ctx context.Context, path string) (map[string]string, error) {
	var m Manifest
	if err := c.doJSON(ctx, http.MethodPost, "/v1/workspace/manifest", nil, WorkspaceRequest{Path: path}, &m); err != nil {
		return nil, err
	}
	return m.Files, nil
}

// Diff returns the files of a workspace folder that differ from base, a
// manifest from Manifest.
func (c *Client) Diff(ctx context.Context, path string, base map[string]string) (Diff, error) {
	if base == nil {
		base = map[string]string{}
	}
	var d Diff
	err := c.doJSON(ctx, http.MethodPost, "/v1/workspace/diff", nil, DiffRequest{Path: path, Base: base}, &d)
	return d, err
}

func (c *Client) doJSON(ctx context.Context, method, path string, q url.Values, in, out any) error {
	var body io.Reader
	ctype := ""
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
		ctype = "application/json"
	}
	resp, err := c.do(ctx, method, path, q, body, ctype)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("agentd %s %s: decode reply: %w", method, path, err)
	}
	return nil
}

// do sends a request and returns the response when the status is 2xx. On
// any other status it closes the body and returns an *Error.
func (c *Client) do(ctx context.Context, method, path string, q url.Values, body io.Reader, ctype string) (*http.Response, error) {
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("agentd %s %s: %w", method, path, err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	msg := http.StatusText(resp.StatusCode)
	var er ErrorReply
	if json.Unmarshal(raw, &er) == nil && er.Error != "" {
		msg = er.Error
	} else if s := strings.TrimSpace(string(raw)); s != "" {
		msg = s
	}
	return nil, &Error{Status: resp.StatusCode, Message: msg}
}

// dialError turns a refused websocket handshake into an Error with agentd's reason. The
// websocket library keeps the first 1024 bytes of the refusal body for this.
func dialError(resp *http.Response, err error) error {
	if resp == nil {
		return err
	}
	msg := err.Error()
	if resp.Body != nil {
		raw, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		var er ErrorReply
		if json.Unmarshal(raw, &er) == nil && er.Error != "" {
			msg = er.Error
		} else if s := strings.TrimSpace(string(raw)); s != "" {
			msg = s
		}
	}
	return &Error{Status: resp.StatusCode, Message: msg}
}
