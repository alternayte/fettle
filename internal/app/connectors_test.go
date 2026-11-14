//go:build contract

package app_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/apptest"
	"github.com/alternayte/fettle/internal/audit"
	fakemcp "github.com/alternayte/fettle/internal/fakes/mcp"
	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
	fakeoauth "github.com/alternayte/fettle/internal/fakes/oauth"
)

type connectorOut struct {
	ID       string `json:"id"`
	Accounts []struct {
		ID    string `json:"id"`
		Label string `json:"label"`
	} `json:"accounts"`
}

// startMail runs fakemail behind OAuth or a header key.
func startMail(t *testing.T, as *fakeoauth.Server, key string) *fakemcp.Mail {
	t.Helper()
	m := &fakemcp.Mail{OAuth: as, HeaderKey: key}
	srv := httptest.NewServer(m.Handler())
	t.Cleanup(srv.Close)
	m.URL = srv.URL
	return m
}

// grantAccount gives the personal zone an account through its hard-ask card.
func grantAccount(e *apptest.Env, accountID string) {
	e.T.Helper()
	var zs struct {
		Zones []struct{ ID, Name string } `json:"zones"`
	}
	e.MustDo("GET", e.SP("/zones"), nil, &zs)
	for _, z := range zs.Zones {
		if z.Name == "personal" {
			var r struct {
				CardID string `json:"card_id"`
			}
			e.MustDo("POST", e.SP("/zones/%s/grants", z.ID), map[string]any{"kind": "connector_account", "target_id": accountID}, &r)
			e.MustDo("POST", e.SP("/cards/%s/answer", r.CardID), map[string]any{"action": "allow"}, nil)
		}
	}
}

func TestSCN_CON_001_OAuthPKCEAndHeaderConnectors(t *testing.T) {
	e := apptest.Start(t, "sqlite")
	e.SetupModel("openai-chat", "", 128000)
	as := fakeoauth.New()
	asrv := httptest.NewServer(as)
	t.Cleanup(asrv.Close)
	as.URL = asrv.URL
	mail := startMail(t, as, "")

	var c connectorOut
	e.MustDo("POST", e.SP("/connectors"), map[string]any{"name": "fakemail", "url": mail.URL + "/mcp", "auth": "oauth"}, &c)
	if as.Registered() != 1 {
		t.Fatalf("dynamic registration: %d clients", as.Registered())
	}
	var start struct {
		URL string `json:"url"`
	}
	e.MustDo("POST", e.SP("/connectors/%s/authorize", c.ID), map[string]any{"label": "work"}, &start)
	if !strings.Contains(start.URL, "code_challenge_method=S256") || !strings.Contains(start.URL, "code_challenge=") {
		t.Fatalf("the authorize URL has no PKCE: %s", start.URL)
	}
	// The person signs in; the server redirects back to Fettle's callback.
	resp, err := http.Get(start.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !strings.Contains(resp.Request.URL.String(), "/settings/connectors?connected=") {
		t.Fatalf("the sign-in ended at %s", resp.Request.URL)
	}
	if as.PKCEUsed != 1 {
		t.Fatal("the code was not redeemed with a PKCE verifier")
	}
	e.MustDo("GET", e.SP("/connectors"), nil, &struct{}{})
	var list struct {
		Connectors []connectorOut `json:"connectors"`
	}
	e.MustDo("GET", e.SP("/connectors"), nil, &list)
	acc := list.Connectors[0].Accounts[0].ID
	var stored string
	if err := e.App.DB.Engine.QueryRow(context.Background(), "SELECT token_ciphertext FROM connector_accounts WHERE id = $1", uuid.MustParse(acc)).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "at-") || strings.Contains(stored, "rt-") || stored == "" {
		t.Fatal("the tokens are not stored encrypted")
	}

	// A bot in the zone that holds the account calls a tool with the token.
	grantAccount(e, acc)
	e.Fake.AddScript(fakemodel.Script{Name: "inbox", Match: fakemodel.Match{User: "[[inbox]]"}, Steps: []fakemodel.Step{
		{ToolCalls: []fakemodel.ScriptCall{{Name: "fakemail__list_inbox", Args: map[string]any{"limit": 3}}}}, {Text: "Read."},
	}})
	chief := e.Chief()
	run, _ := e.Post(chief.ChatID, "[[inbox]] what is new?")
	r := e.WaitRun(run, "done", "failed")
	calls := mail.Calls()
	if r.Line.State != "done" || len(calls) != 1 || calls[0].Account != "owner@acme.test" {
		t.Fatalf("tool call %s %+v: %s", r.Line.State, calls, r.EventTypes())
	}

	// A connector with a header key works too.
	hmail := startMail(t, nil, "acme-mail-key-9901")
	var hc connectorOut
	e.MustDo("POST", e.SP("/connectors"), map[string]any{"name": "teammail", "url": hmail.URL + "/mcp", "auth": "header", "header_name": "X-Api-Key"}, &hc)
	var hacc struct {
		ID string `json:"id"`
	}
	e.MustDo("POST", e.SP("/connectors/%s/accounts", hc.ID), map[string]any{"label": "team", "value": "acme-mail-key-9901"}, &hacc)
	var tools struct {
		Tools []struct{ Name, Class string } `json:"tools"`
	}
	e.MustDo("POST", e.SP("/connectors/%s/test", hc.ID), map[string]any{"account_id": hacc.ID}, &tools)
	if len(tools.Tools) != 3 {
		t.Fatalf("header connector tools %+v", tools.Tools)
	}
}

func TestSCN_CON_004_ToolClassesAndOwnerChanges(t *testing.T) {
	e := apptest.Start(t, "sqlite")
	e.SetupModel("openai-chat", "", 128000)
	mail := startMail(t, nil, "acme-mail-key-9901")
	var c connectorOut
	e.MustDo("POST", e.SP("/connectors"), map[string]any{"name": "fakemail", "url": mail.URL + "/mcp", "auth": "header", "header_name": "X-Api-Key"}, &c)
	var acc struct {
		ID string `json:"id"`
	}
	e.MustDo("POST", e.SP("/connectors/%s/accounts", c.ID), map[string]any{"label": "work", "value": "acme-mail-key-9901"}, &acc)
	grantAccount(e, acc.ID)
	classes := func() map[string]string {
		tools, err := e.App.Agent.Dynamic(context.Background(), uuid.MustParse(e.Space), uuid.MustParse(e.Chief().ID))
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]string{}
		for _, tl := range tools {
			m[tl.Name] = tl.Class
		}
		return m
	}
	got := classes()
	want := map[string]string{"fakemail__list_inbox": "read", "fakemail__send_email": "send", "fakemail__delete_email": "write_external"}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("classes %v, want %v", got, want)
		}
	}
	// An owner changes a class and turns a tool off; the next run sees both.
	e.MustDo("PUT", e.SP("/connectors/%s/tools/list_inbox", c.ID), map[string]any{"class": "write_external", "disabled": false}, nil)
	e.MustDo("PUT", e.SP("/connectors/%s/tools/delete_email", c.ID), map[string]any{"disabled": true}, nil)
	got = classes()
	if got["fakemail__list_inbox"] != "write_external" {
		t.Fatalf("the class change did not apply: %v", got)
	}
	if _, ok := got["fakemail__delete_email"]; ok {
		t.Fatalf("a disabled tool is still offered: %v", got)
	}
}

func TestSCN_CON_006_ComposioKeyAddsItsEndpoint(t *testing.T) {
	e := apptest.Start(t, "sqlite")
	composio := startMail(t, nil, "acme-composio-key-551")
	if _, err := e.App.Settings.Set(context.Background(), audit.System, nil, "connectors.composio_url", composio.URL+"/mcp"); err != nil {
		t.Fatal(err)
	}
	var c connectorOut
	e.MustDo("POST", e.SP("/connectors/composio"), map[string]any{"key": "acme-composio-key-551"}, &c)
	var list struct {
		Connectors []struct {
			Name     string
			Kind     string
			Accounts []struct{ ID string } `json:"accounts"`
		} `json:"connectors"`
	}
	e.MustDo("GET", e.SP("/connectors"), nil, &list)
	if len(list.Connectors) != 1 || list.Connectors[0].Kind != "composio" || len(list.Connectors[0].Accounts) != 1 {
		t.Fatalf("connectors %+v", list.Connectors)
	}
	var tools struct {
		Tools []struct{ Name, Class string } `json:"tools"`
	}
	e.MustDo("POST", e.SP("/connectors/%s/test", c.ID), map[string]any{"account_id": list.Connectors[0].Accounts[0].ID}, &tools)
	if len(tools.Tools) != 3 {
		t.Fatalf("Composio tools %+v", tools.Tools)
	}
}
