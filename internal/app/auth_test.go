package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/alternayte/fettle/internal/app"
	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/testenv"
)

// serverApp starts an app in server mode with one owner account.
func serverApp(t *testing.T, dialect, baseURL string) (*app.App, string) {
	t.Helper()
	t.Setenv("FETTLE_NO_KEYCHAIN", "1")
	cfg := testenv.Config(t, dialect)
	cfg.Mode, cfg.BaseURL, cfg.Version = app.ModeServer, baseURL, "test"
	ctx := context.Background()
	a, err := app.New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(ctx, a.Handler()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Shutdown(context.Background()) })
	u, err := a.SignIn.CreateUser(ctx, "owner@acme.test", "Owner", "acme-owner-pass-4410", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.CreateSpace(ctx, audit.System, "acme", "UTC", u.ID()); err != nil {
		t.Fatal(err)
	}
	return a, "http://" + a.Addr()
}

type client struct {
	t      *testing.T
	url    string
	cookie string
	origin string
}

func (c *client) do(method, path string, body any, bearer string) (int, http.Header, string) {
	c.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.url+path, r)
	req.Header.Set("Content-Type", "application/json")
	if c.origin != "" {
		req.Header.Set("Origin", c.origin)
	}
	if c.cookie != "" {
		req.Header.Set("Cookie", c.cookie)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(b)
}

// signIn signs in and keeps the session cookie.
func (c *client) signIn(email, password string) int {
	c.t.Helper()
	code, h, _ := c.do("POST", "/api/auth/sign-in/email", map[string]string{"email": email, "password": password}, "")
	for _, sc := range h.Values("Set-Cookie") {
		if strings.HasPrefix(sc, "fettle_session=") {
			c.cookie = strings.SplitN(sc, ";", 2)[0]
		}
	}
	return code
}

func TestSCN_AUTH_003_PersonalAccessTokens(t *testing.T) {
	testenv.Each(t, func(t *testing.T, dialect string) {
		_, url := serverApp(t, dialect, "http://fettle.test")
		c := &client{t: t, url: url, origin: "http://fettle.test"}
		if code := c.signIn("owner@acme.test", "acme-owner-pass-4410"); code != 200 {
			t.Fatalf("sign-in: %d", code)
		}
		code, _, body := c.do("POST", "/api/auth/api-keys", map[string]any{"name": "cli"}, "")
		if code/100 != 2 {
			t.Fatalf("create token: %d %s", code, body)
		}
		var out struct {
			Key struct {
				ID string `json:"id"`
			} `json:"key"`
			Plaintext string `json:"plaintext"`
		}
		_ = json.Unmarshal([]byte(body), &out)
		key := struct{ ID, Key string }{out.Key.ID, out.Plaintext}
		if !strings.HasPrefix(key.Key, "fet_") {
			t.Fatalf("token %s", body)
		}
		api := &client{t: t, url: url}
		code, _, body = api.do("GET", "/api/v1/me", nil, key.Key)
		if code != 200 || !strings.Contains(body, "owner@acme.test") {
			t.Fatalf("GET /me with the token: %d %s", code, body)
		}
		if code, _, body = c.do("POST", "/api/auth/api-keys/"+key.ID+"/revoke", map[string]any{}, ""); code/100 != 2 {
			t.Fatalf("revoke: %d %s", code, body)
		}
		if code, _, _ = api.do("GET", "/api/v1/me", nil, key.Key); code != 401 {
			t.Fatalf("GET /me with a revoked token: %d", code)
		}
	})
}
