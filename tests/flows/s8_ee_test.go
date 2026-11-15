//go:build e2e && ee

package flows

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alternayte/fettle/ee"
	"github.com/alternayte/fettle/ee/licensekey"
	"github.com/alternayte/fettle/internal/apptest"
	fakemcp "github.com/alternayte/fettle/internal/fakes/mcp"
	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
)

// eeLicensed sets a licence with every Enterprise feature, signed by a test issuer.
func eeLicensed(t *testing.T) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	ee.SetTestPublicKey(pub)
	t.Cleanup(func() { ee.SetTestPublicKey(nil) })
	key, err := licensekey.Sign(priv, licensekey.License{ID: "lic-acme", Customer: "Acme", Seats: 10, Features: []string{licensekey.FeatureAllInOne}, IssuedAt: time.Now().AddDate(0, -1, 0), Expires: time.Now().AddDate(1, 0, 0)})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FETTLE_LICENSE", key)
}

func TestSCN_EE_006_PolicyPackAndOrgBudgetInEverySpace(t *testing.T) {
	eeLicensed(t)
	acme := apptest.Start(t, "sqlite")
	var made struct {
		ID string `json:"id"`
	}
	acme.MustDo("POST", "/spaces", map[string]any{"name": "other", "time_zone": "UTC"}, &made)
	other := *acme
	other.Space = made.ID

	mail := &fakemcp.Mail{HeaderKey: "acme-mail-key-7702"}
	srv := httptest.NewServer(mail.Handler())
	t.Cleanup(srv.Close)
	mail.URL = srv.URL

	// Each space: a model, a fakemail connector granted to personal, and Never mode, so only
	// the pack can stop the send.
	for _, e := range []*apptest.Env{acme, &other} {
		e.SetupModel("openai-chat", "", 128000)
		var c, acc struct {
			ID string `json:"id"`
		}
		e.MustDo("POST", e.SP("/connectors"), map[string]any{"name": "fakemail", "url": srv.URL + "/mcp", "auth": "header", "header_name": "X-Api-Key"}, &c)
		e.MustDo("POST", e.SP("/connectors/%s/accounts", c.ID), map[string]any{"label": "work", "value": "acme-mail-key-7702"}, &acc)
		grantTo(e, "personal", acc.ID)
		var r cardReply
		e.MustDo("PUT", e.SP("/approval-mode"), map[string]any{"mode": "never"}, &r)
		e.MustDo("POST", e.SP("/cards/%s/answer", r.CardID), map[string]any{"action": "allow"}, nil)
	}

	acme.MustDo("PUT", "/admin/policy-pack", map[string]any{"rules": []map[string]any{{"tool": "send_email", "effect": "block"}}}, nil)
	for i, e := range []*apptest.Env{acme, &other} {
		marker := fmt.Sprintf("pack-send-%d", i)
		e.Fake.AddScript(fakemodel.Script{Name: marker, Match: fakemodel.Match{System: "[[script:" + marker + "]]"}, Steps: []fakemodel.Step{
			{ToolCalls: call("fakemail__send_email", map[string]any{"to": "team@acme.test", "subject": "notes", "body": "the notes"})},
			{Text: "I could not send it."},
		}})
		chief := e.Chief()
		var b map[string]any
		e.MustDo("GET", e.SP("/bots/%s", chief.ID), nil, &b)
		e.MustDo("PATCH", e.SP("/bots/%s", chief.ID), map[string]any{"name": b["name"], "job": b["job"], "instructions": "[[script:" + marker + "]]"}, nil)
		run, _ := e.Post(chief.ChatID, "Send the notes to the team.")
		r := e.WaitRun(run, "done", "failed", "waiting")
		res := strings.Join(toolResults(r, "fakemail__send_email"), " ")
		if r.Line.State != "done" || !strings.Contains(strings.ToLower(res), "block") {
			t.Fatalf("space %d: the pack did not refuse send_email: %s %s; result %q", i, r.Line.State, r.EventTypes(), res)
		}
	}
	for _, c := range mail.Calls() {
		if c.Tool == "send_email" {
			t.Fatal("an email went out despite the policy pack")
		}
	}

	// The organization budget stops calls in every space before they run.
	acme.MustDo("PUT", "/admin/org-budgets", map[string]any{"org_month_usd": 0.0001, "user_month_usd": 0}, nil)
	for i, e := range []*apptest.Env{acme, &other} {
		chief := e.Chief()
		e.MustDo("PATCH", e.SP("/bots/%s", chief.ID), map[string]any{"name": chief.Name, "job": "Routes work", "instructions": "Answer briefly."}, nil)
		run, _ := e.Post(chief.ChatID, "Write a plan for the week.")
		r := e.WaitRun(run, "escalated", "done", "failed")
		// The run makes no model call; the correction check of the last run (D-159) may.
		if r.Line.State != "escalated" || !r.HasEvent("budget_reached") || r.HasEvent("model_turn") {
			t.Fatalf("space %d: the organization budget did not stop the call: %s %s", i, r.Line.State, r.EventTypes())
		}
	}
}
