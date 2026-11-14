//go:build e2e

package flows

import (
	"strings"
	"testing"
	"time"

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
	marker := "b" + strings.ReplaceAll(b.ID, "-", "")[:12] + "-" + strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")
	s.script(marker, append(steps, fakemodel.Step{Text: "Done."})...)
	s.instruct(b.ID, marker)
	run, _ := s.Post(b.ChatID, "Please do the steps.")
	return s.WaitRun(run, "done", "failed", "escalated", "waiting")
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
