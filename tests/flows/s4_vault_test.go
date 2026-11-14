//go:build e2e

package flows

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/app"
	"github.com/alternayte/fettle/internal/audit"
	fakemcp "github.com/alternayte/fettle/internal/fakes/mcp"
	fakeoauth "github.com/alternayte/fettle/internal/fakes/oauth"
	"github.com/alternayte/fettle/internal/objects"
	"github.com/alternayte/fettle/internal/runs"
	"github.com/alternayte/fettle/internal/vault"

	"github.com/alternayte/fettle/internal/apptest"
	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
	"github.com/alternayte/fettle/internal/fakes/web"
)

// zoneIDOf returns the ID of a zone of a server.
func (s *server) zoneIDOf(name string) string {
	s.T.Helper()
	var zs struct {
		Zones []struct{ ID, Name string } `json:"zones"`
	}
	s.MustDo("GET", s.SP("/zones"), nil, &zs)
	for _, z := range zs.Zones {
		if z.Name == name {
			return z.ID
		}
	}
	s.T.Fatalf("no zone %s", name)
	return ""
}

// botRun runs scripted steps for one bot and returns the run.
func (s *server) botRun(b apptest.Bot, steps ...fakemodel.Step) apptest.Run {
	s.T.Helper()
	_, r := s.botRunID(b, steps...)
	return r
}

// botRunID is botRun that also returns the run ID.
func (s *server) botRunID(b apptest.Bot, steps ...fakemodel.Step) (string, apptest.Run) {
	s.T.Helper()
	marker := "b" + strings.ReplaceAll(b.ID, "-", "")[:12] + "-" + strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")
	s.script(marker, append(steps, fakemodel.Step{Text: "Done."})...)
	s.instruct(b.ID, marker)
	run, _ := s.Post(b.ChatID, "Please do the steps.")
	return run, s.WaitRun(run, "done", "failed", "escalated", "waiting")
}

func TestSCN_VLT_003_BrokerFillsOnlyGrantedLoginsOnTheirSite(t *testing.T) {
	s := startComputers(t, "sqlite")
	s.neverMode()
	var item struct {
		ID string `json:"id"`
	}
	s.MustDo("POST", s.SP("/vault"), map[string]any{"name": "shop", "kind": "login", "site": "https://shop.test", "username": web.ShopUser, "value": web.ShopPassword}, &item)
	var r cardReply
	s.MustDo("POST", s.SP("/zones/%s/grants", s.zoneIDOf("personal")), map[string]any{"kind": "vault_item", "target_id": item.ID}, &r)
	s.MustDo("POST", s.SP("/cards/%s/answer", r.CardID), map[string]any{"action": "allow"}, nil)
	var inbox, research apptest.Bot
	s.MustDo("POST", s.SP("/bots"), map[string]any{"name": "Inbox", "job": "Triage mail", "zone_id": s.zoneIDOf("personal")}, &inbox)
	s.MustDo("POST", s.SP("/bots"), map[string]any{"name": "Research", "job": "Finds sources", "zone_id": s.zoneIDOf("browsing")}, &research)

	run := s.botRun(inbox,
		fakemodel.Step{ToolCalls: call("browser_navigate", map[string]any{"url": "http://shop.test/login"})},
		fakemodel.Step{ToolCalls: call("fill_login", map[string]any{"item": "shop", "username_ref": "e1", "password_ref": "e2"})},
		fakemodel.Step{ToolCalls: call("browser_act", map[string]any{"ref": "e3", "action": "click"})},
	)
	act := strings.Join(toolResults(run, "browser_act"), "\n")
	if !strings.Contains(act, "Signed in as "+web.ShopUser) {
		t.Fatalf("the sign-in did not succeed: %s\n%s", act, strings.Join(toolResults(run, "fill_login"), "\n"))
	}
	for _, ev := range run.Events {
		if strings.Contains(string(ev.Data), web.ShopPassword) {
			t.Fatalf("the password is in a journal event: %s", ev.Type)
		}
	}

	run = s.botRun(research, fakemodel.Step{ToolCalls: call("fill_login", map[string]any{"item": "shop", "password_ref": "e2"})})
	if out := strings.Join(toolResults(run, "fill_login"), "\n"); !strings.Contains(out, "no access to this vault item") {
		t.Fatalf("the broker gave a login to a zone without a grant: %s", out)
	}

	run = s.botRun(inbox,
		fakemodel.Step{ToolCalls: call("browser_navigate", map[string]any{"url": "http://evil.test/"})},
		fakemodel.Step{ToolCalls: call("fill_login", map[string]any{"item": "shop", "username_ref": "e1", "password_ref": "e2"})},
	)
	if out := strings.Join(toolResults(run, "fill_login"), "\n"); !strings.Contains(out, "not on the site of this login") {
		t.Fatalf("agentd filled a login on another site: %s", out)
	}
}

func TestSCN_VLT_004_SecretsAreRedactedEverywhere(t *testing.T) {
	s := startComputers(t, "sqlite", "FETTLE_LOG_LEVEL=debug")
	as := fakeoauth.New()
	asrv := httptest.NewServer(as)
	t.Cleanup(asrv.Close)
	as.URL = asrv.URL
	mail := &fakemcp.Mail{OAuth: as}
	msrv := httptest.NewServer(mail.Handler())
	t.Cleanup(msrv.Close)
	mail.URL = msrv.URL

	const secret = "acme-deploy-secret-4471"
	s.MustDo("POST", s.SP("/vault"), map[string]any{"name": "deploy key", "kind": "secret", "value": secret}, nil)
	var c struct {
		ID string `json:"id"`
	}
	s.MustDo("POST", s.SP("/connectors"), map[string]any{"name": "fakemail", "url": msrv.URL + "/mcp", "auth": "oauth"}, &c)
	var start struct {
		URL string `json:"url"`
	}
	s.MustDo("POST", s.SP("/connectors/%s/authorize", c.ID), map[string]any{"label": "work"}, &start)
	resp, err := http.Get(start.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	oauthToken := as.AccessTokens()[0]

	chief := s.Chief()
	s.runTool("exec", map[string]any{"command": "true"})
	zone := s.zoneIDOf("personal")
	env := docker(t, "ps", "-q", "--filter", "label=fettle.zone="+zone)
	computerToken := ""
	for _, kv := range strings.Split(docker(t, "inspect", "-f", "{{range .Config.Env}}{{println .}}{{end}}", strings.Fields(env)[0]), "\n") {
		if v, ok := strings.CutPrefix(kv, "FETTLE_AGENTD_TOKEN="); ok {
			computerToken = v
		}
	}
	if computerToken == "" {
		t.Fatal("no computer token in the container")
	}

	all := secret + " " + computerToken + " " + oauthToken
	marker := "leak"
	s.script(marker,
		fakemodel.Step{ToolCalls: call("exec", map[string]any{"command": "echo " + all})},
		fakemodel.Step{Text: "Here they are: " + all + "\n" + strings.Repeat("padding line for a large event\n", 400)})
	s.instruct(chief.ID, marker)
	runID, _ := s.Post(chief.ChatID, "Show me the values.")
	r := s.WaitRun(runID, "done", "failed")
	raw := []string{secret, computerToken, oauthToken}
	names := []string{"[secret:deploy key]", "[secret:computer token]", "[secret:fakemail token]"}

	// Events, as the API serves them.
	var events strings.Builder
	for _, ev := range r.Events {
		events.Write(ev.Data)
	}
	// Payloads, read back from the object store with the space data key.
	var payloads strings.Builder
	cfg := &app.Config{Data: s.data, DB: "sqlite", Mode: app.ModeLocal, ObjectStore: "disk"}
	_ = cfg.Normalize()
	db, err := app.OpenDatabase(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	t.Setenv("FETTLE_NO_KEYCHAIN", "1")
	master, _, err := vault.LoadMasterKey("", s.data, false)
	if err != nil {
		t.Fatal(err)
	}
	store, _ := objects.NewDisk(filepath.Join(s.data, "objects"))
	evs, err := runs.NewJournal(db.Engine, store, vault.New(db.Engine, audit.New(db.Engine), master)).Load(context.Background(), uuid.MustParse(runID))
	if err != nil {
		t.Fatal(err)
	}
	stored := 0
	for _, ev := range evs {
		if ev.PayloadKey != nil {
			stored++
			payloads.WriteString(ev.Data)
		}
	}
	if stored == 0 {
		t.Fatal("no event went to the object store")
	}
	var chat strings.Builder
	for _, m := range s.Messages(chief.ChatID) {
		b, _ := json.Marshal(m)
		chat.Write(b)
	}
	for where, text := range map[string]string{"events": events.String(), "payloads": payloads.String(), "chat": chat.String(), "logs": s.logs.String()} {
		for _, v := range raw {
			if strings.Contains(text, v) {
				t.Fatalf("a raw secret is in the %s", where)
			}
		}
		if where == "logs" {
			continue
		}
		for _, n := range names {
			if !strings.Contains(text, n) {
				t.Fatalf("the %s do not show %s", where, n)
			}
		}
	}
}

// containerOf returns the container of a zone's computer.
func containerOf(t *testing.T, zoneID string) string {
	t.Helper()
	ids := strings.Fields(docker(t, "ps", "-q", "--filter", "label=fettle.zone="+zoneID))
	if len(ids) == 0 {
		t.Fatalf("zone %s has no running computer", zoneID)
	}
	return ids[0]
}

func agentdToken(t *testing.T, container string) string {
	t.Helper()
	for _, kv := range strings.Split(docker(t, "inspect", "-f", "{{range .Config.Env}}{{println .}}{{end}}", container), "\n") {
		if v, ok := strings.CutPrefix(kv, "FETTLE_AGENTD_TOKEN="); ok {
			return v
		}
	}
	t.Fatal("no computer token")
	return ""
}

func TestSCN_CMP_002_ZonesAreIsolated(t *testing.T) {
	s := startComputers(t, "sqlite")
	s.neverMode()
	var item struct {
		ID string `json:"id"`
	}
	s.MustDo("POST", s.SP("/vault"), map[string]any{"name": "deploy key", "kind": "secret", "value": "acme-deploy-secret-4471"}, &item)
	var r cardReply
	s.MustDo("POST", s.SP("/zones/%s/grants", s.zoneIDOf("personal")), map[string]any{"kind": "vault_item", "target_id": item.ID}, &r)
	s.MustDo("POST", s.SP("/cards/%s/answer", r.CardID), map[string]any{"action": "allow"}, nil)
	var inbox, research apptest.Bot
	s.MustDo("POST", s.SP("/bots"), map[string]any{"name": "Inbox", "job": "Triage mail", "zone_id": s.zoneIDOf("personal")}, &inbox)
	s.MustDo("POST", s.SP("/bots"), map[string]any{"name": "Research", "job": "Finds sources", "zone_id": s.zoneIDOf("browsing")}, &research)
	s.botRun(inbox, fakemodel.Step{ToolCalls: call("write_file", map[string]any{"path": "notes.md", "content": "inbox only"})})

	home := "/home/fettle/bots/" + inbox.ID + "/notes.md"
	run := s.botRun(research,
		fakemodel.Step{ToolCalls: call("exec", map[string]any{"command": "cat " + home + " 2>&1 || echo not-here"})},
		fakemodel.Step{ToolCalls: call("read_file", map[string]any{"path": "bots/" + inbox.ID + "/notes.md"})},
		fakemodel.Step{ToolCalls: call("use_secret", map[string]any{"item": "deploy key", "env_name": "KEY", "command": "echo $KEY"})},
	)
	if out := strings.Join(toolResults(run, "exec"), ""); strings.Contains(out, "inbox only") || !strings.Contains(out, "not-here") {
		t.Fatalf("Research's computer has Inbox's home: %s", out)
	}
	if out := strings.Join(toolResults(run, "read_file"), ""); !strings.Contains(out, "home of another bot") {
		t.Fatalf("read_file reached another bot's home: %s", out)
	}
	if out := strings.Join(toolResults(run, "use_secret"), ""); !strings.Contains(out, "no access to this vault item") {
		t.Fatalf("Research used a personal grant: %s", out)
	}
	// agentd of browsing refuses the computer token of personal.
	personal := containerOf(t, s.zoneIDOf("personal"))
	browsing := containerOf(t, s.zoneIDOf("browsing"))
	port := strings.TrimSpace(strings.Split(docker(t, "port", browsing, "7421"), "\n")[0])
	port = port[strings.LastIndex(port, ":")+1:]
	cl := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}} //nolint:gosec // the check is the token, not TLS
	for name, tok := range map[string]string{"personal": agentdToken(t, personal), "browsing": agentdToken(t, browsing)} {
		req, _ := http.NewRequest("GET", "https://127.0.0.1:"+port+"/v1/health", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := cl.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		want := 200
		if name == "personal" {
			want = 401
		}
		if resp.StatusCode != want {
			t.Fatalf("browsing agentd with the %s token: %d, want %d", name, resp.StatusCode, want)
		}
	}
}
