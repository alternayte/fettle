//go:build contract

package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/apptest"
	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
)

// doAs sends an API request with another bearer token and an extension origin.
func doAs(e *apptest.Env, token, method, path string, body any) (int, string) {
	e.T.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.URL+"/api/v1"+path, r)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "chrome-extension://acmeextensionid")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.T.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

// A person pairs the extension, and the bot gets the sign-in that the extension shares:
// the token reaches only the share routes, a waiting takeover continues, the bot page lists
// the site, Sign out removes it, and no value reaches the API, the events or the audit log.
func TestShareSignInIntoBotProfile(t *testing.T) {
	e := hostApp(t)
	e.SetupModel("openai-chat", "", 128000)
	ctx := context.Background()
	const cookieVal, storageVal = "acme-cookie-5521-session", "acme-storage-7731-token"
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		title := "Acme signed out"
		if k, err := r.Cookie("acme_session"); err == nil && k.Value == cookieVal {
			title = "Acme signed in"
		}
		_, _ = fmt.Fprintf(w, `<!doctype html><title>%s</title><p id=t></p><script>document.getElementById('t').textContent = 'token ' + (localStorage.getItem('acme_token') ? 'set' : 'none')</script>`, title)
	}))
	defer site.Close()

	chief := e.Chief()
	var zs struct {
		Zones []struct{ ID, Name string } `json:"zones"`
	}
	e.MustDo("GET", e.SP("/zones"), nil, &zs)
	var bot map[string]any
	e.MustDo("GET", e.SP("/bots/%s", chief.ID), nil, &bot)
	zone := bot["zone_id"].(string)
	for _, z := range zs.Zones {
		if z.ID == zone {
			e.MustDo("PATCH", e.SP("/zones/%s", z.ID), map[string]any{"name": z.Name, "network": "open", "size": "browser", "idle_minutes": 30}, nil)
		}
	}

	// Pairing: the code works once and gives a token with the share scope.
	var pair struct{ Code, URL, Server string }
	e.MustDo("POST", "/extension/pairings", nil, &pair)
	if !strings.HasSuffix(pair.URL, "/pair#code="+pair.Code) {
		t.Fatalf("pairing %+v", pair)
	}
	var claim struct{ Token, Server, Account string }
	code, body := doAs(e, "none", "POST", "/extension/claim", map[string]any{"code": strings.ToLower(pair.Code)})
	if code != http.StatusOK || json.Unmarshal([]byte(body), &claim) != nil || !strings.HasPrefix(claim.Token, "fext_") {
		t.Fatalf("claim = %d %s", code, body)
	}
	if code, _ := doAs(e, "none", "POST", "/extension/claim", map[string]any{"code": pair.Code}); code != http.StatusUnprocessableEntity {
		t.Fatalf("a second claim got %d", code)
	}
	tok := claim.Token
	if code, body := doAs(e, tok, "GET", "/extension/targets", nil); code != http.StatusOK || !strings.Contains(body, chief.ID) {
		t.Fatalf("targets = %d %s", code, body)
	}
	for _, p := range []string{e.SP("/bots"), e.SP("/bots/%s/sign-ins", chief.ID), "/extension/tokens", "/me"} {
		if code, _ := doAs(e, tok, "GET", p, nil); code != http.StatusForbidden {
			t.Fatalf("GET %s with the extension token got %d", p, code)
		}
	}

	// A run waits on a takeover request for the site.
	host := strings.TrimPrefix(site.URL, "http://")
	e.Fake.AddScript(fakemodel.Script{Name: "share", Match: fakemodel.Match{System: "[[script:share]]"}, Steps: []fakemodel.Step{
		{ToolCalls: []fakemodel.ScriptCall{{Name: "request_takeover", Args: map[string]any{"reason": "Sign me in to Acme.", "site": host}}}},
		{Text: "Signed in."},
	}})
	e.MustDo("PATCH", e.SP("/bots/%s", chief.ID), map[string]any{"name": bot["name"], "job": bot["job"], "instructions": "[[script:share]]"}, nil)
	run, _ := e.Post(chief.ChatID, "Read the Acme report.")
	e.WaitRun(run, "waiting")
	if _, body := doAs(e, tok, "GET", "/extension/targets?url="+site.URL+"/report", nil); !strings.Contains(body, `"requests":["127.0.0.1"]`) || !strings.Contains(body, `"site":"127.0.0.1"`) {
		t.Fatalf("targets show no request: %s", body)
	}

	share := map[string]any{
		"url":     site.URL + "/report",
		"cookies": []map[string]any{{"name": "acme_session", "value": cookieVal, "domain": "127.0.0.1", "path": "/", "host_only": true, "http_only": true, "same_site": "lax"}},
		"storage": map[string]string{"acme_token": storageVal},
	}
	bad := map[string]any{"url": site.URL, "cookies": []map[string]any{{"name": "x", "value": "y", "domain": "evil.test", "path": "/"}}}
	if code, body := doAs(e, tok, "POST", e.SP("/bots/%s/sign-ins", chief.ID), bad); code != http.StatusUnprocessableEntity {
		t.Fatalf("a cookie of another site got %d %s", code, body)
	}
	code, body = doAs(e, tok, "POST", e.SP("/bots/%s/sign-ins", chief.ID), share)
	if code != http.StatusOK || !strings.Contains(body, `"continued":true`) {
		t.Fatalf("share = %d %s", code, body)
	}
	r := e.WaitRun(run, "done")
	found := false
	for _, ev := range r.Events {
		if strings.Contains(string(ev.Data), "shared my sign-in for 127.0.0.1") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the run did not get the share: %s", r.EventTypes())
	}

	// The bot's browser is signed in.
	l, err := e.App.Computers.Acquire(ctx, uuid.MustParse(zone))
	if err != nil {
		t.Fatal(err)
	}
	snap, err := l.Client.BrowserNavigate(ctx, "bot-"+chief.ID, site.URL+"/report")
	l.Release()
	if err != nil || snap.Title != "Acme signed in" || !strings.Contains(snap.Text, "token set") {
		t.Fatalf("bot page %q %q %v", snap.Title, snap.Text, err)
	}

	var sites struct{ Sites []string }
	e.MustDo("GET", e.SP("/bots/%s/sign-ins", chief.ID), nil, &sites)
	if !slices.Contains(sites.Sites, "127.0.0.1") {
		t.Fatalf("sites %v", sites.Sites)
	}
	e.MustDo("DELETE", e.SP("/bots/%s/sign-ins/127.0.0.1", chief.ID), nil, nil)
	e.MustDo("GET", e.SP("/bots/%s/sign-ins", chief.ID), nil, &sites)
	if slices.Contains(sites.Sites, "127.0.0.1") {
		t.Fatalf("sites after sign-out %v", sites.Sites)
	}

	_, events := e.Do("GET", e.SP("/runs/%s", run), nil, nil)
	_, audit := e.Do("GET", e.SP("/audit"), nil, nil)
	_, msgs := e.Do("GET", e.SP("/chats/%s/messages", chief.ChatID), nil, nil)
	for _, v := range []string{cookieVal, storageVal, tok} {
		if strings.Contains(events+audit+msgs, v) {
			t.Fatalf("a shared value is in the API: %s", v)
		}
	}
	if !strings.Contains(audit, "signin.share") || !strings.Contains(audit, "signin.remove") {
		t.Fatal("the audit log has no share or sign-out")
	}

	// Revoking the token stops the next share.
	var list struct {
		Tokens []struct{ ID string } `json:"tokens"`
	}
	e.MustDo("GET", "/extension/tokens", nil, &list)
	if len(list.Tokens) != 1 {
		t.Fatalf("tokens %+v", list)
	}
	e.MustDo("DELETE", "/extension/tokens/"+list.Tokens[0].ID, nil, nil)
	if code, _ := doAs(e, tok, "POST", e.SP("/bots/%s/sign-ins", chief.ID), share); code != http.StatusUnauthorized {
		t.Fatalf("a revoked token got %d", code)
	}
}
