package app_test

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/apptest"
	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
)

// toolResults returns the results and errors of the tool_call events of a run, by tool.
func toolResults(t *testing.T, r apptest.Run) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, ev := range r.Events {
		if ev.Type != "tool_call" {
			continue
		}
		var d struct{ Tool, Result, Error string }
		_ = json.Unmarshal(ev.Data, &d)
		out[d.Tool] = d.Result + d.Error
	}
	return out
}

func TestBotReadsTheDocsAndChecksItsOwnSetup(t *testing.T) {
	e := apptest.Start(t, "sqlite")
	e.SetupModel("openai-chat", "", 128000)
	chief := e.Chief()
	ctx := context.Background()

	e.Fake.AddScript(fakemodel.Script{Name: "docs", Match: fakemodel.Match{User: "How do I add an OpenRouter key"}, Steps: []fakemodel.Step{
		toolStep("fettle_docs", map[string]any{"query": "How do I add an OpenRouter key?"}),
		toolStep("fettle_docs", map[string]any{"page": "models.md"}),
		{Text: "Open /settings/models and add the key."},
	}})
	run, _ := e.Post(chief.ChatID, "How do I add an OpenRouter key?")
	r := e.WaitRun(run, "done")
	tools, system := lastRequest(e, "How do I add an OpenRouter key")
	for _, n := range []string{"fettle_docs", "fettle_status", "draft_issue"} {
		if !slices.Contains(tools, n) {
			t.Fatalf("the bot has no %s: %v", n, tools)
		}
	}
	if !strings.Contains(system, "use fettle_docs and fettle_status") {
		t.Fatal("the base rules do not name fettle_docs")
	}
	res := toolResults(t, r)
	if !strings.Contains(res["fettle_docs"], "/settings/models") {
		t.Fatalf("models.md has no /settings/models: %.300s", res["fettle_docs"])
	}

	// Give the zone a computer provider that does not exist: the status names the reason
	// and holds no key.
	if _, err := e.App.DB.Engine.Exec(ctx, "UPDATE zones SET provider_id = $1", uuid.New()); err != nil {
		t.Fatal(err)
	}
	e.Fake.AddScript(fakemodel.Script{Name: "status", Match: fakemodel.Match{User: "Why can you not use the browser"}, Steps: []fakemodel.Step{
		toolStep("fettle_status", map[string]any{}),
		{Text: "The computer provider is off."},
	}})
	run, _ = e.Post(chief.ChatID, "Why can you not use the browser?")
	st := toolResults(t, e.WaitRun(run, "done"))["fettle_status"]
	var status struct {
		Version string `json:"version"`
		Mode    string `json:"mode"`
		Bot     struct {
			Models        map[string][]string `json:"models"`
			ProviderError string              `json:"computer_provider_error"`
		} `json:"bot"`
		DoctorChecks []struct {
			Name string `json:"name"`
			Pass bool   `json:"pass"`
		} `json:"doctor_checks"`
		Note string `json:"note"`
	}
	if err := json.Unmarshal([]byte(st), &status); err != nil {
		t.Fatalf("status is not JSON: %v\n%s", err, st)
	}
	if !strings.Contains(status.Bot.ProviderError, "does not exist") || len(status.Bot.Models["owner"]) == 0 || len(status.DoctorChecks) == 0 || status.Mode == "" {
		t.Fatalf("status %s", st)
	}
	if strings.Contains(st, "sk-fake-test-key-123456") {
		t.Fatal("the status holds the model key")
	}

	// A person who is not an instance admin gets no instance checks.
	if _, err := e.App.DB.Engine.Exec(ctx, "UPDATE users SET is_admin = $1", false); err != nil {
		t.Fatal(err)
	}
	e.Fake.AddScript(fakemodel.Script{Name: "status-member", Match: fakemodel.Match{User: "Check your setup again"}, Steps: []fakemodel.Step{
		toolStep("fettle_status", map[string]any{}),
		{Text: "Done."},
	}})
	run, _ = e.Post(chief.ChatID, "Check your setup again")
	st = toolResults(t, e.WaitRun(run, "done"))["fettle_status"]
	status.DoctorChecks, status.Note = nil, ""
	if err := json.Unmarshal([]byte(st), &status); err != nil {
		t.Fatal(err)
	}
	if len(status.DoctorChecks) != 0 || status.Note == "" {
		t.Fatalf("a member got instance checks: %s", st)
	}
}

func TestDraftIssuePostsARedactedCardAndTheRunDoesNotWait(t *testing.T) {
	e := apptest.Start(t, "sqlite")
	e.SetupModel("openai-chat", "", 128000)
	chief := e.Chief()
	e.MustDo("PUT", "/admin/settings/fettle.issue_repo", map[string]any{"value": "acme/fettle-fork"}, nil)

	e.Fake.AddScript(fakemodel.Script{Name: "issue", Match: fakemodel.Match{User: "Report the browser bug"}, Steps: []fakemodel.Step{
		toolStep("draft_issue", map[string]any{
			"title":    "Browser fails for jo.bloggs@acme.example",
			"what":     "agentd at 10.1.2.3 refused, see /home/jbloggs/.local/share/fettle/fettle.log and git.corp.acme.internal",
			"expected": "The page opens.",
			"steps":    "1. Ask the bot to open a page.",
		}),
		{Text: "I drafted the issue. Read the card and open it on GitHub."},
	}})
	run, _ := e.Post(chief.ChatID, "Report the browser bug")
	r := e.WaitRun(run, "done")
	if r.HasEvent("card_posted") {
		t.Fatal("the run waited on the issue card")
	}
	cards := cardsOf(e, chief.ChatID, "issue")
	if len(cards) != 1 {
		t.Fatalf("issue cards: %d", len(cards))
	}
	c := cards[0]
	body, _ := c["detail"].(string)
	data, _ := c["data"].(map[string]any)
	link, _ := data["url"].(string)
	for _, gone := range []string{"jo.bloggs", "10.1.2.3", "jbloggs", "acme.internal"} {
		if strings.Contains(body, gone) || strings.Contains(link, url.QueryEscape(gone)) || strings.Contains(c["title"].(string), gone) {
			t.Fatalf("the card holds %q: %s", gone, body)
		}
	}
	if !strings.Contains(body, "- Version:") || !strings.Contains(body, "~/.local/share/fettle/fettle.log") {
		t.Fatalf("body %s", body)
	}
	u, err := url.Parse(link)
	if err != nil || u.Host != "github.com" || u.Path != "/acme/fettle-fork/issues/new" || !strings.Contains(u.Query().Get("body"), "Diagnostics") {
		t.Fatalf("url %s", link)
	}

	var m map[string]any
	e.MustDo("POST", e.SP("/cards/%s/answer", c["id"]), map[string]any{"action": "opened"}, &m)
	if got := m["card"].(map[string]any); got["status"] != "answered" || got["answer"] != "Opened on GitHub" {
		t.Fatalf("answered card %+v", got)
	}
}

func TestIssueDraftEndpointGivesRedactedDiagnostics(t *testing.T) {
	e := apptest.Start(t, "sqlite")
	e.SetupModel("openai-chat", "", 128000)
	var out struct {
		Repo        string `json:"repo"`
		Markdown    string `json:"markdown"`
		MaxURL      int    `json:"max_url"`
		Diagnostics struct {
			OS             string   `json:"os"`
			ModelProviders []string `json:"model_providers"`
		} `json:"diagnostics"`
	}
	e.MustDo("GET", e.SP("/issue-draft"), nil, &out)
	if out.Repo != "alternayte/fettle" || out.MaxURL != 7500 || !strings.Contains(out.Markdown, "### Diagnostics") || out.Diagnostics.OS == "" || len(out.Diagnostics.ModelProviders) == 0 {
		t.Fatalf("draft %+v", out)
	}
	if strings.Contains(out.Markdown, "127.0.0.1") {
		t.Fatalf("the diagnostics hold a local address: %s", out.Markdown)
	}
}
