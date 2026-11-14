//go:build e2e

package flows

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/apptest"
	"github.com/alternayte/fettle/internal/computers"
	fakemcp "github.com/alternayte/fettle/internal/fakes/mcp"
	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
)

// grantTo gives a zone a connector account through its hard-ask card.
func grantTo(e *apptest.Env, zone, accountID string) {
	e.T.Helper()
	var r struct {
		CardID string `json:"card_id"`
	}
	e.MustDo("POST", e.SP("/zones/%s/grants", zoneID(e, zone)), map[string]any{"kind": "connector_account", "target_id": accountID}, &r)
	e.MustDo("POST", e.SP("/cards/%s/answer", r.CardID), map[string]any{"action": "allow"}, nil)
}

func TestSCN_CON_002_StdioConnectorRunsInTheComputer(t *testing.T) {
	e := dockerApp(t)
	ctx := context.Background()
	chief := e.Chief()
	bin, err := os.ReadFile("../../bin/linux/fettle-fakes")
	if err != nil {
		t.Fatalf("the Linux fakes binary is missing; run just e2e: %v", err)
	}
	// The stdio server is a program in the bot's home inside the computer.
	zone := uuid.MustParse(zoneID(e, "personal"))
	l, err := e.App.Computers.Acquire(ctx, zone)
	if err != nil {
		t.Fatal(err)
	}
	bot := uuid.MustParse(chief.ID)
	if err := e.App.Computers.EnsureBotHome(ctx, l, bot); err != nil {
		t.Fatal(err)
	}
	if err := l.Client.WriteFile(ctx, computers.BotHome(bot)+"/fettle-fakes", bin, 0o755); err != nil {
		t.Fatal(err)
	}
	l.Release()

	var c struct {
		ID string `json:"id"`
	}
	e.MustDo("POST", e.SP("/connectors"), map[string]any{"name": "localmail", "kind": "mcp_stdio", "command": []string{"./fettle-fakes", "fake", "mcp-stdio"}}, &c)
	var acc struct {
		ID string `json:"id"`
	}
	e.MustDo("POST", e.SP("/connectors/%s/accounts", c.ID), map[string]any{"label": "local"}, &acc)
	grantTo(e, "personal", acc.ID)
	e.Fake.AddScript(fakemodel.Script{Name: "stdio-inbox", Match: fakemodel.Match{User: "[[stdio-inbox]]"}, Steps: []fakemodel.Step{
		{ToolCalls: call("localmail__list_inbox", map[string]any{})}, {Text: "Read."},
	}})
	run, _ := e.Post(chief.ChatID, "[[stdio-inbox]] read the local inbox")
	r := e.WaitRun(run, "done", "failed")
	out := strings.Join(toolResults(r, "localmail__list_inbox"), "\n")
	cur, _ := e.App.Computers.Current(ctx, zone)
	cid := docker(t, "inspect", "-f", "{{.Config.Hostname}}", cur.ProviderRef)
	host, _ := os.Hostname()
	if !strings.Contains(out, "Served by host "+cid) || strings.Contains(out, host) {
		t.Fatalf("the stdio server did not run in the computer %s: %s", cid, out)
	}
}

func TestSCN_CON_003_AccountsFollowZoneGrants(t *testing.T) {
	e := dockerApp(t)
	mail := &fakemcp.Mail{HeaderKey: "acme-mail-key-9901"}
	srv := httptest.NewServer(mail.Handler())
	t.Cleanup(srv.Close)
	mail.URL = srv.URL
	var c struct {
		ID string `json:"id"`
	}
	e.MustDo("POST", e.SP("/connectors"), map[string]any{"name": "fakemail", "url": srv.URL + "/mcp", "auth": "header", "header_name": "X-Api-Key"}, &c)
	var work, home struct {
		ID string `json:"id"`
	}
	e.MustDo("POST", e.SP("/connectors/%s/accounts", c.ID), map[string]any{"label": "work", "value": "acme-mail-key-9901"}, &work)
	e.MustDo("POST", e.SP("/connectors/%s/accounts", c.ID), map[string]any{"label": "home", "value": "acme-mail-key-9901"}, &home)
	grantTo(e, "personal", work.ID)
	var inbox, research apptest.Bot
	e.MustDo("POST", e.SP("/bots"), map[string]any{"name": "Inbox", "job": "Triage mail", "zone_id": zoneID(e, "personal")}, &inbox)
	e.MustDo("POST", e.SP("/bots"), map[string]any{"name": "Research", "job": "Finds sources", "zone_id": zoneID(e, "browsing")}, &research)
	sp := uuid.MustParse(e.Space)
	tools := func(bot string) []string {
		ts, err := e.App.Agent.Dynamic(context.Background(), sp, uuid.MustParse(bot))
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, tl := range ts {
			names = append(names, tl.Name+" "+tl.Description)
		}
		return names
	}
	in := tools(inbox.ID)
	if len(in) != 3 || !strings.Contains(strings.Join(in, "|"), "account work") || strings.Contains(strings.Join(in, "|"), "account home") {
		t.Fatalf("Inbox sees %v; want the tools of the work account only", in)
	}
	if r := tools(research.ID); len(r) != 0 {
		t.Fatalf("Research in another zone sees %v", r)
	}
}
