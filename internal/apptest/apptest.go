// Package apptest starts a full Fettle app with the fake model for integration and end-to-end tests.
package apptest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alternayte/fettle/internal/app"
	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
	"github.com/alternayte/fettle/internal/models"
	"github.com/alternayte/fettle/internal/testenv"
)

// Env is a running app and its fakes.
type Env struct {
	T       testing.TB
	App     *app.App
	URL     string
	Token   string
	Space   string
	Owner   string
	Fake    *fakemodel.Server
	FakeURL string
	Cfg     *app.Config
}

// Option changes the config before start.
type Option func(*app.Config)

// Start runs an app on dialect with the fake model and returns its environment.
func Start(t testing.TB, dialect string, opts ...Option) *Env {
	t.Helper()
	t.Setenv("FETTLE_NO_KEYCHAIN", "1")
	// Tests pick their provider; the host's default (D-15) must not change their result.
	if os.Getenv("FETTLE_DEFAULT_PROVIDER") == "" {
		t.Setenv("FETTLE_DEFAULT_PROVIDER", "docker")
	}
	models.RetryBackoff = func(int) time.Duration { return time.Millisecond }
	fake, err := fakemodel.New()
	if err != nil {
		t.Fatal(err)
	}
	fs := httptest.NewServer(fake)
	t.Cleanup(fs.Close)
	cfg := testenv.Config(t, dialect)
	cfg.Version = "test"
	for _, o := range opts {
		o(cfg)
	}
	ctx := context.Background()
	a, err := app.New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(ctx, a.Handler()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = a.Shutdown(sctx)
	})
	e := &Env{T: t, App: a, URL: a.URL(), Token: a.LocalToken, Fake: fake, FakeURL: fs.URL, Cfg: cfg}
	e.loadMe()
	return e
}

// Attach returns an environment for a server that runs in another process.
func Attach(t testing.TB, url, token string, fake *fakemodel.Server, fakeURL string) *Env {
	t.Helper()
	models.RetryBackoff = func(int) time.Duration { return time.Millisecond }
	e := &Env{T: t, URL: url, Token: token, Fake: fake, FakeURL: fakeURL}
	e.loadMe()
	return e
}

func (e *Env) loadMe() {
	var me struct {
		ID     string `json:"id"`
		Spaces []struct {
			ID string `json:"id"`
		} `json:"spaces"`
	}
	e.MustDo("GET", "/me", nil, &me)
	e.Space, e.Owner = me.Spaces[0].ID, me.ID
}

// Do sends an API request with the local token. path starts after /api/v1.
func (e *Env) Do(method, path string, body, out any) (int, string) {
	e.T.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.URL+app.APIPrefix+path, r)
	req.Header.Set("Authorization", "Bearer "+e.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.T.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if out != nil && resp.StatusCode/100 == 2 && len(b) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			e.T.Fatalf("%s %s: decode: %v: %s", method, path, err, b)
		}
	}
	return resp.StatusCode, string(b)
}

// MustDo fails the test on a non-2xx status.
func (e *Env) MustDo(method, path string, body, out any) {
	e.T.Helper()
	code, b := e.Do(method, path, body, out)
	if code/100 != 2 {
		e.T.Fatalf("%s %s = %d: %s", method, path, code, b)
	}
}

// SP returns a path inside the test space.
func (e *Env) SP(format string, a ...any) string {
	return "/spaces/" + e.Space + fmt.Sprintf(format, a...)
}

// Provider is the created fake provider.
type Provider struct {
	ID string `json:"id"`
}

// SetupModel adds a fake provider of apiType with URL flags, tests it and sets the owner and summarizer roles.
func (e *Env) SetupModel(apiType, flags string, contextWindow int) string {
	e.T.Helper()
	base := e.FakeURL + "/v1"
	if flags != "" {
		base = e.FakeURL + "/mode/" + flags + "/v1"
	}
	var p Provider
	e.MustDo("POST", e.SP("/model-providers"), map[string]any{"name": "fake " + apiType + flags, "api_type": apiType, "base_url": base, "auth_style": "bearer", "key": "sk-fake-test-key-123456"}, &p)
	e.MustDo("POST", e.SP("/model-providers/%s/test", p.ID), map[string]any{"model": "fake/large"}, nil)
	for _, role := range []string{"owner", "summarizer"} {
		e.MustDo("PUT", e.SP("/model-roles/%s", role), map[string]any{"targets": []map[string]any{{"provider_id": p.ID, "model": "fake/large", "price_in": 3.0, "price_out": 15.0, "context_window": contextWindow}}}, nil)
	}
	return p.ID
}

// Bot is a bot from the API.
type Bot struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ChatID    string `json:"chat_id"`
	IsPrimary bool   `json:"is_primary"`
}

// Chief returns the primary bot.
func (e *Env) Chief() Bot {
	e.T.Helper()
	var out struct {
		Bots []Bot `json:"bots"`
	}
	e.MustDo("GET", e.SP("/bots"), nil, &out)
	for _, b := range out.Bots {
		if b.IsPrimary {
			return b
		}
	}
	e.T.Fatal("no primary bot")
	return Bot{}
}

// Post sends a user message and returns the run ID.
func (e *Env) Post(chatID, text string) (runID string, steered bool) {
	e.T.Helper()
	var out struct {
		RunID   string `json:"run_id"`
		Steered bool   `json:"steered"`
	}
	e.MustDo("POST", e.SP("/chats/%s/messages", chatID), map[string]any{"body": text}, &out)
	return out.RunID, out.Steered
}

// Run is a run from the API.
type Run struct {
	Line struct {
		State   string  `json:"state"`
		CostUSD float64 `json:"cost_usd"`
		Step    string  `json:"step"`
	} `json:"line"`
	Events []struct {
		Seq  int64           `json:"seq"`
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	} `json:"events"`
}

// GetRun returns a run with its events.
func (e *Env) GetRun(id string) Run {
	e.T.Helper()
	var r Run
	e.MustDo("GET", e.SP("/runs/%s", id), nil, &r)
	return r
}

// WaitRun waits until a run has one of the states.
func (e *Env) WaitRun(id string, states ...string) Run {
	e.T.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var r Run
	for time.Now().Before(deadline) {
		r = e.GetRun(id)
		for _, s := range states {
			if r.Line.State == s {
				return r
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	e.T.Fatalf("run %s is %q, want one of %v; events %s", id, r.Line.State, states, r.EventTypes())
	return r
}

// Messages returns the messages of a chat.
func (e *Env) Messages(chatID string) []map[string]any {
	e.T.Helper()
	var out struct {
		Messages []map[string]any `json:"messages"`
	}
	e.MustDo("GET", e.SP("/chats/%s/messages?limit=200", chatID), nil, &out)
	return out.Messages
}

// HasEvent reports whether a run has an event type.
func (r Run) HasEvent(typ string) bool {
	for _, ev := range r.Events {
		if ev.Type == typ {
			return true
		}
	}
	return false
}

// EventTypes lists the event types in order.
func (r Run) EventTypes() string {
	var s []string
	for _, ev := range r.Events {
		s = append(s, ev.Type)
	}
	return strings.Join(s, ",")
}

// Peer starts a second app on the same database and data folder, as a second server process
// on one PostgreSQL database. It shares the fake model of e.
func Peer(t testing.TB, e *Env, opts ...Option) *Env {
	t.Helper()
	cfg := *e.Cfg
	cfg.Addr = "127.0.0.1:0"
	for _, o := range opts {
		o(&cfg)
	}
	ctx := context.Background()
	a, err := app.New(ctx, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(ctx, a.Handler()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = a.Shutdown(sctx)
	})
	p := &Env{T: t, App: a, URL: a.URL(), Token: a.LocalToken, Fake: e.Fake, FakeURL: e.FakeURL, Cfg: &cfg}
	p.loadMe()
	return p
}
