package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
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
	// Like apptest: the host's default provider (D-15) must not run in tests.
	if os.Getenv("FETTLE_DEFAULT_PROVIDER") == "" {
		t.Setenv("FETTLE_DEFAULT_PROVIDER", "docker")
	}
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

// In server mode the Chrome extension gets an access token that can only share a sign-in,
// and Account lists it with that scope; a token from before scopes keeps every route.
func TestExtensionPairingGivesAShareOnlyKey(t *testing.T) {
	testenv.Each(t, func(t *testing.T, dialect string) {
		_, url := serverApp(t, dialect, "http://fettle.test")
		c := &client{t: t, url: url, origin: "http://fettle.test"}
		if code := c.signIn("owner@acme.test", "acme-owner-pass-4410"); code != 200 {
			t.Fatalf("sign-in: %d", code)
		}
		code, _, body := c.do("POST", "/api/v1/extension/pairings", nil, "")
		var pair struct{ Code, URL string }
		if code != 200 || json.Unmarshal([]byte(body), &pair) != nil || pair.URL != "http://fettle.test/pair#code="+pair.Code {
			t.Fatalf("pairing: %d %s", code, body)
		}
		ext := &client{t: t, url: url, origin: "chrome-extension://acmeextensionid"}
		code, h, body := ext.do("POST", "/api/v1/extension/claim", map[string]string{"code": pair.Code}, "")
		var claim struct{ Token, Server, Account string }
		if code != 200 || json.Unmarshal([]byte(body), &claim) != nil || !strings.HasPrefix(claim.Token, "fet_") || claim.Server != "http://fettle.test" {
			t.Fatalf("claim: %d %s", code, body)
		}
		if h.Get("Access-Control-Allow-Origin") != "chrome-extension://acmeextensionid" {
			t.Fatalf("claim CORS %v", h)
		}
		if code, _, body := ext.do("GET", "/api/v1/extension/targets", nil, claim.Token); code != 200 || !strings.Contains(body, "acme") {
			t.Fatalf("targets: %d %s", code, body)
		}
		if code, _, _ := ext.do("GET", "/api/v1/me", nil, claim.Token); code != 403 {
			t.Fatalf("GET /me with the extension key: %d", code)
		}
		_, _, keys := c.do("GET", "/api/auth/api-keys", nil, "")
		if !strings.Contains(keys, "share-sign-in") || !strings.Contains(keys, "Chrome extension") {
			t.Fatalf("the keys do not show the extension key with its scope: %s", keys)
		}
	})
}
