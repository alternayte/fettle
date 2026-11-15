package app_test

import (
	"strings"
	"testing"

	"github.com/alternayte/fettle/internal/apptest"
	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
)

func say(text string) []fakemodel.Step { return []fakemodel.Step{{Text: text}} }

func toolStep(name string, args map[string]any) fakemodel.Step {
	return fakemodel.Step{ToolCalls: []fakemodel.ScriptCall{{Name: name, Args: args}}}
}

// cardsOf returns the cards of one kind in a chat, oldest first.
func cardsOf(e *apptest.Env, chatID, kind string) []map[string]any {
	var out []map[string]any
	for _, m := range e.Messages(chatID) {
		if c, ok := m["card"].(map[string]any); ok && c["kind"] == kind {
			out = append(out, c)
		}
	}
	return out
}

func TestSCN_UX_004_OnboardingInterview(t *testing.T) {
	e := apptest.Start(t, "sqlite")
	e.SetupModel("openai-chat", "", 128000)
	chief := e.Chief()

	e.Fake.AddScript(fakemodel.Script{Name: "ob-start", Match: fakemodel.Match{User: "Start the onboarding interview"}, Steps: say("Hello. What is your work?")})
	e.Fake.AddScript(fakemodel.Script{Name: "ob-work", Match: fakemodel.Match{User: "I run a small shop"}, Steps: say("Which apps do you use for your work?")})
	e.Fake.AddScript(fakemodel.Script{Name: "ob-apps", Match: fakemodel.Match{User: "Gmail and Notion"}, Steps: say("What are your top 3 recurring chores?")})
	team := []map[string]any{
		{"name": "Mailroom", "job": "Answers customer mail", "instructions": "Draft replies to customer mail.", "first_task": "Draft replies to the three newest customer mails."},
		{"name": "Stock", "job": "Updates the stock sheet", "instructions": "Keep the stock sheet current.", "first_task": "List the items with less than five in stock."},
		{"name": "Reporter", "job": "Writes the weekly report", "instructions": "Write the weekly report.", "first_task": "Write a draft of this week's report."},
	}
	e.Fake.AddScript(fakemodel.Script{Name: "ob-chores", Match: fakemodel.Match{User: "customer mail"}, Steps: []fakemodel.Step{
		toolStep("suggest_connectors", map[string]any{"apps": []string{"Gmail", "Notion"}}),
		toolStep("propose_team", map[string]any{"bots": team}),
		{Text: "Press Create to make the team. I then suggest a first task for each bot."},
	}})

	// The wizard ends with the kickoff; a second call starts nothing.
	var ob struct {
		Started bool   `json:"started"`
		ChatID  string `json:"chat_id"`
		RunID   string `json:"run_id"`
	}
	e.MustDo("POST", e.SP("/onboarding/start"), nil, &ob)
	if !ob.Started || ob.ChatID != chief.ChatID || ob.RunID == "" {
		t.Fatalf("kickoff %+v", ob)
	}
	e.WaitRun(ob.RunID, "done")
	var again struct {
		RunID string `json:"run_id"`
	}
	e.MustDo("POST", e.SP("/onboarding/start"), nil, &again)
	if again.RunID != "" {
		t.Fatalf("the second kickoff started run %s", again.RunID)
	}
	// The kickoff run follows the interview skill.
	var skilled bool
	for _, r := range e.Fake.Requests() {
		if strings.Contains(r.LastUser, "Start the onboarding interview") && strings.Contains(r.System, "Call suggest_connectors with the apps that the user named") {
			skilled = true
		}
	}
	if !skilled {
		t.Fatal("the kickoff run did not load the skill onboarding-interview")
	}

	// The three questions: work, apps, chores.
	for _, answer := range []string{"I run a small shop.", "Gmail and Notion.", "Answer customer mail, update the stock sheet, write the weekly report."} {
		run, _ := e.Post(chief.ChatID, answer)
		e.WaitRun(run, "done")
	}
	var asked []string
	for _, m := range e.Messages(chief.ChatID) {
		if m["author_kind"] == "bot" {
			if b, _ := m["body"].(string); strings.HasSuffix(b, "?") {
				asked = append(asked, b)
			}
		}
	}
	if len(asked) != 3 || !strings.Contains(asked[0], "work") || !strings.Contains(asked[1], "apps") || !strings.Contains(asked[2], "chores") {
		t.Fatalf("questions %q", asked)
	}

	// One Connect card per app.
	connect := cardsOf(e, chief.ChatID, "connect")
	if len(connect) != 2 || connect[0]["title"] != "Connect Gmail" || connect[1]["title"] != "Connect Notion" {
		t.Fatalf("connect cards %v", connect)
	}
	var answered map[string]any
	e.MustDo("POST", e.SP("/cards/%s/answer", connect[1]["id"]), map[string]any{"action": "skip"}, &answered)

	// One propose_team card with three bots and their first tasks.
	props := cardsOf(e, chief.ChatID, "proposal")
	if len(props) != 1 || props[0]["title"] != "New team: Mailroom, Stock, Reporter" {
		t.Fatalf("proposal cards %v", props)
	}
	preview, _ := props[0]["preview"].(map[string]any)
	if line, _ := preview["Stock"].(string); !strings.Contains(line, "first task: List the items") {
		t.Fatalf("preview %v", preview)
	}
	e.MustDo("POST", e.SP("/cards/%s/answer", props[0]["id"]), map[string]any{"action": "create"}, nil)

	var list struct {
		Bots []apptest.Bot `json:"bots"`
	}
	e.MustDo("GET", e.SP("/bots"), nil, &list)
	made := map[string]bool{}
	for _, b := range list.Bots {
		made[b.Name] = true
	}
	for _, n := range []string{"Mailroom", "Stock", "Reporter"} {
		if !made[n] {
			t.Fatalf("Create did not make %s: %+v", n, list.Bots)
		}
	}

	// The Chief ends with one first task per created bot. Nothing runs by itself.
	msgs := e.Messages(chief.ChatID)
	last, _ := msgs[len(msgs)-1]["body"].(string)
	for _, task := range []string{"**Mailroom**: Draft replies", "**Stock**: List the items", "**Reporter**: Write a draft"} {
		if !strings.Contains(last, task) {
			t.Fatalf("first tasks %q", last)
		}
	}
	var rs struct {
		Runs []map[string]any `json:"runs"`
	}
	e.MustDo("GET", e.SP("/runs"), nil, &rs)
	for _, r := range rs.Runs {
		if r["bot_name"] != "Chief" {
			t.Fatalf("a first task ran by itself: %v", r)
		}
	}
}
