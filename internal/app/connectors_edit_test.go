//go:build contract

package app_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/agent"
	"github.com/alternayte/fettle/internal/apptest"
	"github.com/alternayte/fettle/internal/audit"
	fakemcp "github.com/alternayte/fettle/internal/fakes/mcp"
	fakeoauth "github.com/alternayte/fettle/internal/fakes/oauth"
	"github.com/alternayte/fettle/internal/testenv"
)

// hostApp starts an app whose zones use the host provider, so a stdio connector runs as a
// process of this host through a real agentd.
func hostApp(t *testing.T) *apptest.Env {
	t.Helper()
	if os.Getenv("FETTLE_AGENTD_BIN") == "" {
		bin := filepath.Join(t.TempDir(), "fettle-agentd")
		_, file, _, _ := runtime.Caller(0)
		cmd := exec.Command("go", "build", "-o", bin, "./cmd/fettle-agentd")
		cmd.Dir = filepath.Join(filepath.Dir(file), "..", "..")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build agentd: %v\n%s", err, out)
		}
		t.Setenv("FETTLE_AGENTD_BIN", bin)
	}
	t.Setenv("FETTLE_DEFAULT_PROVIDER", "host")
	e := apptest.Start(t, "sqlite")
	if err := e.App.Computers.SetEnabled(context.Background(), audit.System, nil, "host", true); err != nil {
		t.Fatal(err)
	}
	return e
}

// botTools returns the connector tools of the primary bot by name.
func botTools(e *apptest.Env) map[string]agent.Tool {
	e.T.Helper()
	ts, err := e.App.Agent.Dynamic(context.Background(), uuid.MustParse(e.Space), uuid.MustParse(e.Chief().ID))
	if err != nil {
		e.T.Fatal(err)
	}
	out := map[string]agent.Tool{}
	for _, tl := range ts {
		out[tl.Name] = tl
	}
	return out
}

// runTool runs a connector tool as the primary bot.
func runTool(e *apptest.Env, tl agent.Tool) string {
	e.T.Helper()
	out, err := tl.Run(context.Background(), &agent.ToolEnv{SpaceID: uuid.MustParse(e.Space), BotID: uuid.MustParse(e.Chief().ID)}, json.RawMessage(`{}`))
	if err != nil {
		e.T.Fatalf("%s: %v", tl.Name, err)
	}
	return out.Text
}

// mustNotLeak fails when a secret is in the connector list or the audit log of the space.
func mustNotLeak(e *apptest.Env, secrets ...string) {
	e.T.Helper()
	_, list := e.Do("GET", e.SP("/connectors"), nil, nil)
	_, log := e.Do("GET", e.SP("/audit"), nil, nil)
	for _, s := range secrets {
		if strings.Contains(list, s) || strings.Contains(log, s) {
			e.T.Fatalf("a secret value is in the API or the audit log: %s", s)
		}
	}
}

func TestSCN_CON_008_StdioAccountsRunWithTheirOwnSecrets(t *testing.T) {
	e := hostApp(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var c connectorOut
	e.MustDo("POST", e.SP("/connectors"), map[string]any{
		"name": "localmail", "kind": "mcp_stdio", "command": []string{self},
		"env":       map[string]string{"FETTLE_TEST_FAKEMAIL_STDIO": "1"},
		"variables": []map[string]any{{"name": "FAKEMAIL_TOKEN", "required": true, "secret": true}},
	}, &c)
	const anaTok, benTok = "acme-mail-token-ana-7781", "acme-mail-token-ben-4410"
	var ana, ben, none struct {
		ID        string   `json:"id"`
		SecretEnv []string `json:"secret_env"`
	}
	e.MustDo("POST", e.SP("/connectors/%s/accounts", c.ID), map[string]any{"label": "ana", "secret_env": map[string]string{"FAKEMAIL_TOKEN": anaTok}}, &ana)
	e.MustDo("POST", e.SP("/connectors/%s/accounts", c.ID), map[string]any{"label": "ben", "secret_env": map[string]string{"FAKEMAIL_TOKEN": benTok}}, &ben)
	e.MustDo("POST", e.SP("/connectors/%s/accounts", c.ID), map[string]any{"label": "none"}, &none)
	if len(ana.SecretEnv) != 1 || ana.SecretEnv[0] != "FAKEMAIL_TOKEN" {
		t.Fatalf("the account shows %v; want the name of the secret", ana.SecretEnv)
	}

	// Test names the missing variable.
	code, body := e.Do("POST", e.SP("/connectors/%s/test", c.ID), map[string]any{"account_id": none.ID}, nil)
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, "FAKEMAIL_TOKEN") {
		t.Fatalf("test without the variable = %d %s", code, body)
	}

	// Each account starts its own process with its own token: two tokens act as two users.
	grantAccount(e, ana.ID)
	grantAccount(e, ben.ID)
	tools := botTools(e)
	for label, tok := range map[string]string{"ana": anaTok, "ben": benTok} {
		tl, ok := tools["localmail_"+label+"__list_inbox"]
		if !ok {
			t.Fatalf("no list_inbox of %s in %v", label, tools)
		}
		out := runTool(e, tl)
		if !strings.Contains(out, "Signed in as "+fakemcp.UserOfToken(tok)+".") {
			t.Fatalf("the %s account ran as another user: %s", label, out)
		}
	}

	// Replace a secret: the next call uses the new value, and no response holds either.
	const anaNew = "acme-mail-token-ana-9902"
	_, patched := e.Do("PATCH", e.SP("/connectors/%s/accounts/%s", c.ID, ana.ID), map[string]any{"secret_env": map[string]string{"FAKEMAIL_TOKEN": anaNew}}, nil)
	if strings.Contains(patched, anaNew) || strings.Contains(patched, anaTok) {
		t.Fatalf("the edit reply holds a secret: %s", patched)
	}
	if out := runTool(e, botTools(e)["localmail_ana__list_inbox"]); !strings.Contains(out, fakemcp.UserOfToken(anaNew)) {
		t.Fatalf("the replaced token is not in use: %s", out)
	}
	mustNotLeak(e, anaTok, benTok, anaNew)
}

func TestSCN_CON_009_EditKeepsGrantsAndRenameRewritesRules(t *testing.T) {
	for _, d := range testenv.Dialects {
		t.Run(d, func(t *testing.T) { editKeepsGrants(t, d) })
	}
}

func editKeepsGrants(t *testing.T, dialect string) {
	e := apptest.Start(t, dialect)
	e.SetupModel("openai-chat", "", 128000)
	first := startMail(t, nil, "acme-mail-key-9901")
	second := startMail(t, nil, "acme-mail-key-4402")
	var c connectorOut
	e.MustDo("POST", e.SP("/connectors"), map[string]any{"name": "fakemail", "url": first.URL + "/mcp", "auth": "header", "header_name": "X-Api-Key"}, &c)
	var acc struct {
		ID string `json:"id"`
	}
	e.MustDo("POST", e.SP("/connectors/%s/accounts", c.ID), map[string]any{"label": "work", "value": "acme-mail-key-9901"}, &acc)
	grantAccount(e, acc.ID)
	e.MustDo("PUT", e.SP("/connectors/%s/tools/list_inbox", c.ID), map[string]any{"class": "write_external", "disabled": false}, nil)
	e.MustDo("PUT", e.SP("/connectors/%s/tools/delete_email", c.ID), map[string]any{"disabled": true}, nil)
	e.MustDo("PUT", e.SP("/connectors/%s/tools/archive_email", c.ID), map[string]any{"class": "read", "disabled": false}, nil)
	e.MustDo("POST", e.SP("/policy/rules"), map[string]any{"connector": "fakemail", "tool": "fakemail__send_email", "effect": "allow"}, nil)
	if _, ok := botTools(e)["fakemail__list_inbox"]; !ok {
		t.Fatal("the tools before the edit are missing")
	}

	// Rename, move to a new URL and replace the key.
	e.MustDo("PATCH", e.SP("/connectors/%s", c.ID), map[string]any{"name": "teammail", "url": second.URL + "/mcp"}, nil)
	_, patched := e.Do("PATCH", e.SP("/connectors/%s/accounts/%s", c.ID, acc.ID), map[string]any{"value": "acme-mail-key-4402"}, nil)
	if strings.Contains(patched, "acme-mail-key") {
		t.Fatalf("the edit reply holds a key: %s", patched)
	}

	tools := botTools(e)
	if tools["teammail__list_inbox"].Class != "write_external" {
		t.Fatalf("the class did not stay: %+v", tools["teammail__list_inbox"])
	}
	if _, ok := tools["teammail__delete_email"]; ok {
		t.Fatal("a turned-off tool is on again")
	}
	runTool(e, tools["teammail__list_inbox"])
	if len(second.Calls()) != 1 || len(first.Calls()) != 0 {
		t.Fatalf("calls went to the old URL: first %d, second %d", len(first.Calls()), len(second.Calls()))
	}

	var list struct {
		Connectors []struct {
			Name     string                     `json:"name"`
			Tools    map[string]json.RawMessage `json:"tools"`
			Accounts []struct {
				ID    string   `json:"id"`
				Zones []string `json:"zones"`
			} `json:"accounts"`
		} `json:"connectors"`
	}
	e.MustDo("GET", e.SP("/connectors"), nil, &list)
	got := list.Connectors[0]
	if got.Name != "teammail" || len(got.Accounts) != 1 || got.Accounts[0].ID != acc.ID || len(got.Accounts[0].Zones) != 1 {
		t.Fatalf("the account or its grant did not stay: %+v", got)
	}
	if _, ok := got.Tools["archive_email"]; ok {
		t.Fatalf("the setting of a tool that is gone stays: %v", got.Tools)
	}
	if _, ok := got.Tools["delete_email"]; !ok {
		t.Fatalf("the setting of a tool that exists is gone: %v", got.Tools)
	}

	var pol struct {
		Rules []struct{ Tool, Connector string } `json:"rules"`
	}
	e.MustDo("GET", e.SP("/policy"), nil, &pol)
	if len(pol.Rules) != 1 || pol.Rules[0].Connector != "teammail" || pol.Rules[0].Tool != "teammail__send_email" {
		t.Fatalf("the rule does not name the new connector: %+v", pol.Rules)
	}
	var log struct {
		Entries []struct{ Action string } `json:"entries"`
	}
	e.MustDo("GET", e.SP("/audit"), nil, &log)
	seen := map[string]bool{}
	for _, en := range log.Entries {
		seen[en.Action] = true
	}
	if !seen["connector.update"] || !seen["connector.account_update"] {
		t.Fatalf("the edits wrote no audit rows: %v", seen)
	}
	mustNotLeak(e, "acme-mail-key-9901", "acme-mail-key-4402")
}

func TestSCN_CON_010_NewOAuthURLSignsInAgain(t *testing.T) {
	for _, d := range testenv.Dialects {
		t.Run(d, func(t *testing.T) { newURLSignsInAgain(t, d) })
	}
}

func newURLSignsInAgain(t *testing.T, dialect string) {
	e := apptest.Start(t, dialect)
	as := fakeoauth.New()
	as.Confidential = true
	asrv := httptest.NewServer(as)
	t.Cleanup(asrv.Close)
	as.URL = asrv.URL
	first := startMail(t, as, "")
	second := startMail(t, as, "")
	var c connectorOut
	e.MustDo("POST", e.SP("/connectors"), map[string]any{"name": "fakemail", "url": first.URL + "/mcp", "auth": "oauth"}, &c)
	signIn := func(body map[string]any) {
		t.Helper()
		var start struct {
			URL string `json:"url"`
		}
		e.MustDo("POST", e.SP("/connectors/%s/authorize", c.ID), body, &start)
		resp, err := http.Get(start.URL)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if !strings.Contains(resp.Request.URL.String(), "connected=") {
			t.Fatalf("the sign-in ended at %s", resp.Request.URL)
		}
	}
	signIn(map[string]any{"label": "work"})
	var list struct {
		Connectors []struct {
			Accounts []struct {
				ID          string   `json:"id"`
				Zones       []string `json:"zones"`
				NeedsSignIn bool     `json:"needs_sign_in"`
			} `json:"accounts"`
		} `json:"connectors"`
	}
	e.MustDo("GET", e.SP("/connectors"), nil, &list)
	acc := list.Connectors[0].Accounts[0].ID
	grantAccount(e, acc)

	// The client secret is sealed in the database.
	oauthColumn := func() string {
		var raw string
		if err := e.App.DB.Engine.QueryRow(context.Background(), "SELECT oauth FROM connectors WHERE id = $1", uuid.MustParse(c.ID)).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	for _, s := range as.Secrets() {
		if strings.Contains(oauthColumn(), s) {
			t.Fatal("the OAuth client secret is stored in plain text")
		}
	}

	// A new URL registers again and clears the tokens; the account and its grant stay.
	e.MustDo("PATCH", e.SP("/connectors/%s", c.ID), map[string]any{"url": second.URL + "/mcp"}, nil)
	if as.Registered() != 2 {
		t.Fatalf("registrations: %d", as.Registered())
	}
	e.MustDo("GET", e.SP("/connectors"), nil, &list)
	a := list.Connectors[0].Accounts[0]
	if a.ID != acc || !a.NeedsSignIn || len(a.Zones) != 1 {
		t.Fatalf("after the URL change: %+v", a)
	}
	if code, body := e.Do("POST", e.SP("/connectors/%s/test", c.ID), map[string]any{"account_id": acc}, nil); code != http.StatusUnprocessableEntity || !strings.Contains(body, "sign in again") {
		t.Fatalf("test before the new sign-in = %d %s", code, body)
	}
	signIn(map[string]any{"label": "work", "account_id": acc})
	e.MustDo("GET", e.SP("/connectors"), nil, &list)
	if a := list.Connectors[0].Accounts; len(a) != 1 || a[0].ID != acc || a[0].NeedsSignIn || len(a[0].Zones) != 1 {
		t.Fatalf("after the new sign-in: %+v", a)
	}
	e.MustDo("POST", e.SP("/connectors/%s/test", c.ID), map[string]any{"account_id": acc}, nil)

	// A secret from before sealing, which the migration moves to client_secret_plain, is
	// sealed at start, and the sign-in still works with it.
	var oc map[string]any
	if err := json.Unmarshal([]byte(oauthColumn()), &oc); err != nil {
		t.Fatal(err)
	}
	plain := as.SecretOf(oc["client_id"].(string))
	if plain == "" || strings.Contains(oauthColumn(), plain) {
		t.Fatal("the new client secret is missing or stored in plain text")
	}
	delete(oc, "client_secret_sealed")
	oc["client_secret_plain"] = plain
	legacy, _ := json.Marshal(oc)
	set := "UPDATE connectors SET oauth = $1 WHERE id = $2"
	if dialect == "postgres" {
		set = "UPDATE connectors SET oauth = CAST($1 AS jsonb) WHERE id = $2"
	}
	if _, err := e.App.DB.Engine.Exec(context.Background(), set, string(legacy), uuid.MustParse(c.ID)); err != nil {
		t.Fatal(err)
	}
	if err := e.App.Connectors.SealClientSecrets(context.Background()); err != nil {
		t.Fatal(err)
	}
	if col := oauthColumn(); strings.Contains(col, plain) || !strings.Contains(col, "client_secret_sealed") {
		t.Fatalf("the start step did not seal the secret: %s", col)
	}
	signIn(map[string]any{"label": "work", "account_id": acc})
	mustNotLeak(e, as.Secrets()...)
}
