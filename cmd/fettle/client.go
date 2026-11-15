package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alternayte/fettle/internal/app"
)

// apiClient talks to a Fettle server over its HTTP API: the local server with the local
// token, or a server with a personal access token (§4.1, REQ-UX-007).
type apiClient struct {
	base  string
	token string
	space string
	http  *http.Client
}

// clientFlags adds the connection flags. FETTLE_SERVER and FETTLE_TOKEN select a server;
// without them the client reads the address and the local token from the data folder.
type clientOpts struct {
	server, token, space, data string
}

func clientFlags(fs *flag.FlagSet) *clientOpts {
	o := &clientOpts{server: os.Getenv("FETTLE_SERVER"), token: os.Getenv("FETTLE_TOKEN"), space: os.Getenv("FETTLE_SPACE"), data: os.Getenv("FETTLE_DATA")}
	fs.StringVar(&o.server, "server", o.server, "server URL (default: the local server)")
	fs.StringVar(&o.token, "token", o.token, "personal access token for the server")
	fs.StringVar(&o.space, "space", o.space, "space ID or name (default: the first space)")
	fs.StringVar(&o.data, "data", o.data, "data folder of the local server")
	return o
}

func (o *clientOpts) connect(ctx context.Context) (*apiClient, error) {
	c := &apiClient{http: &http.Client{Timeout: 0}}
	if o.server != "" {
		u, err := url.Parse(o.server)
		if err != nil || u.Host == "" {
			return nil, fmt.Errorf("the server URL %q is not valid", o.server)
		}
		if o.token == "" {
			return nil, errors.New("a server needs a personal access token: set FETTLE_TOKEN or --token")
		}
		c.base, c.token = strings.TrimRight(o.server, "/"), o.token
	} else {
		data := o.data
		if data == "" {
			data = app.DefaultDataDir(app.ModeLocal)
		}
		addr := readFile(filepath.Join(data, "fettle.addr"))
		tok := readFile(filepath.Join(data, "local.token"))
		if addr == "" || tok == "" {
			return nil, fmt.Errorf("no local server runs for %s: run fettle up, or set FETTLE_SERVER", data)
		}
		c.base, c.token = "http://"+addr, tok
		if o.token != "" {
			c.token = o.token
		}
	}
	var me struct {
		Spaces []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"spaces"`
	}
	if err := c.do(ctx, http.MethodGet, "/me", nil, &me); err != nil {
		return nil, err
	}
	for _, s := range me.Spaces {
		if o.space == "" || o.space == s.ID || strings.EqualFold(o.space, s.Name) {
			c.space = s.ID
			break
		}
	}
	if c.space == "" {
		if o.space != "" {
			return nil, fmt.Errorf("you are not a member of a space %q", o.space)
		}
		return nil, errors.New("you are not a member of a space")
	}
	return c, nil
}

// apiError is an error answer of the API.
type apiError struct {
	Status int
	Detail string
}

func (e *apiError) Error() string { return e.Detail }

func (c *apiClient) request(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+app.APIPrefix+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", "fettle-cli/"+version)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (c *apiClient) do(ctx context.Context, method, path string, body, out any) error {
	req, err := c.request(ctx, method, path, body)
	if err != nil {
		return err
	}
	cl := *c.http
	cl.Timeout = 10 * time.Minute
	resp, err := cl.Do(req)
	if err != nil {
		return fmt.Errorf("the server at %s does not answer: %w", c.base, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var p struct {
			Detail string `json:"detail"`
			Errors []struct {
				Message  string `json:"message"`
				Location string `json:"location"`
			} `json:"errors"`
		}
		_ = json.Unmarshal(b, &p)
		msg := p.Detail
		for _, e := range p.Errors {
			msg += "; " + e.Location + ": " + e.Message
		}
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return &apiError{Status: resp.StatusCode, Detail: msg}
	}
	if out != nil && len(b) > 0 {
		return json.Unmarshal(b, out)
	}
	return nil
}

// sp returns a path in the client's space.
func (c *apiClient) sp(format string, a ...any) string {
	return "/spaces/" + c.space + fmt.Sprintf(format, a...)
}

// sseEvent is one server-sent event.
type sseEvent struct {
	Name string
	Data []byte
}

// stream reads server-sent events of a path until ctx ends. ready is closed when the
// server accepted the stream.
func (c *apiClient) stream(ctx context.Context, path string, ready chan<- struct{}) (<-chan sseEvent, <-chan error) {
	events := make(chan sseEvent, 64)
	errc := make(chan error, 1)
	go func() {
		defer close(events)
		req, err := c.request(ctx, http.MethodGet, path, nil)
		if err != nil {
			errc <- err
			return
		}
		req.Header.Set("Accept", "text/event-stream")
		resp, err := c.http.Do(req)
		if err != nil {
			errc <- err
			return
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			errc <- fmt.Errorf("the stream answered %s", resp.Status)
			return
		}
		close(ready)
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 64<<10), 8<<20)
		var ev sseEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if len(ev.Data) > 0 {
					// An event without a name is a "message" event (the SSE standard).
					if ev.Name == "" {
						ev.Name = "message"
					}
					select {
					case events <- ev:
					case <-ctx.Done():
						return
					}
				}
				ev = sseEvent{}
			case strings.HasPrefix(line, "event:"):
				ev.Name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				if len(ev.Data) > 0 {
					ev.Data = append(ev.Data, '\n')
				}
				ev.Data = append(ev.Data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")...)
			}
		}
		if err := sc.Err(); err != nil && ctx.Err() == nil {
			errc <- err
		}
	}()
	return events, errc
}

// botRef finds a bot by ID or by name, without case.
type botRow struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Job        string  `json:"job"`
	Status     string  `json:"status"`
	IsPrimary  bool    `json:"is_primary"`
	ZoneName   string  `json:"zone_name"`
	ChatID     string  `json:"chat_id"`
	Activity   string  `json:"activity"`
	ZoneID     *string `json:"zone_id"`
	PublicDesc string  `json:"public_desc"`
}

func (c *apiClient) bots(ctx context.Context) ([]botRow, error) {
	var out struct {
		Bots []botRow `json:"bots"`
	}
	err := c.do(ctx, http.MethodGet, c.sp("/bots?archived=true"), nil, &out)
	return out.Bots, err
}

func (c *apiClient) bot(ctx context.Context, ref string) (botRow, error) {
	list, err := c.bots(ctx)
	if err != nil {
		return botRow{}, err
	}
	for _, b := range list {
		if b.ID == ref || strings.EqualFold(b.Name, ref) {
			return b, nil
		}
	}
	return botRow{}, fmt.Errorf("no bot %q in this space", ref)
}

// parseArgs parses flags that may come after positional arguments, as in
// "fettle chat Inbox hello --watch".
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}
