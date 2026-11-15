package agent_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/alternayte/fettle/internal/apptest"
	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
)

const routerSystem = "You route one piece of work"

// routeTo scripts the router's answer for messages that hold a marker.
func routeTo(e *apptest.Env, marker string, step fakemodel.Step) {
	e.Fake.AddScript(fakemodel.Script{Name: "route-" + marker, Match: fakemodel.Match{System: routerSystem, User: marker}, Steps: []fakemodel.Step{step}})
}

type routeEvent struct {
	Step  string   `json:"step"`
	Rules []string `json:"rules"`
	Parts []struct {
		Goal string `json:"goal"`
	} `json:"parts"`
}

func routeOf(t *testing.T, r apptest.Run) routeEvent {
	t.Helper()
	for _, ev := range r.Events {
		if ev.Type == "route_decision" {
			var d routeEvent
			if err := json.Unmarshal(ev.Data, &d); err != nil {
				t.Fatal(err)
			}
			return d
		}
	}
	t.Fatalf("no route_decision: %s", r.EventTypes())
	return routeEvent{}
}

func part(goal, needs string) map[string]any {
	return map[string]any{"goal": goal, "acceptance": "The answer has sources.", "needs": needs}
}

func route(step string, parts ...map[string]any) fakemodel.Step {
	if parts == nil {
		parts = []map[string]any{}
	}
	return fakemodel.Step{JSON: map[string]any{"step": step, "owner_bot_id": "", "parts": parts, "rationale": "test", "confidence": 0.9}}
}

func TestSCN_TEAM_002_RouterRulesAndCostCap(t *testing.T) {
	e := apptest.Start(t, "sqlite")
	e.SetupModel("openai-chat", "", 128000)
	chief := e.Chief()
	routeTo(e, "[[greet]]", route("direct"))
	routeTo(e, "[[three]]", route("fanout", part("Find the price of the Acme desk.", "read"), part("Find the price of the Acme chair.", "read"), part("Find the price of the Acme lamp.", "read")))
	routeTo(e, "[[mail]]", route("fanout", part("Find the Q3 numbers.", "read"), part("Email the numbers to Ana.", "send")))
	routeTo(e, "[[bad]]", fakemodel.Step{Text: "fanout, I think", Plain: true})

	cases := []struct {
		msg, step string
		rules     []string
	}{
		{"[[greet]] Hello!", "direct", nil},
		{"[[three]] Compare the prices of the Acme desk, chair and lamp.", "fanout", nil},
		{"[[mail]] Get the Q3 numbers and email them to Ana.", "single", []string{"R1"}},
		{"[[bad]] Plan the week.", "single", []string{"R7"}},
	}
	for _, c := range cases {
		run, _ := e.Post(chief.ChatID, c.msg)
		r := e.WaitRun(run, "done", "failed", "escalated")
		d := routeOf(t, r)
		if d.Step != c.step {
			t.Fatalf("%s: step %s, want %s (rules %v)", c.msg, d.Step, c.step, d.Rules)
		}
		for _, rule := range c.rules {
			if !slices.Contains(d.Rules, rule) {
				t.Fatalf("%s: rules %v, want %s", c.msg, d.Rules, rule)
			}
		}
		if c.step == "fanout" {
			// The fanout result gets a citations check; the fake reply cites nothing, so the
			// verifier may escalate it. This test is about the route.
			if len(d.Parts) != 3 {
				t.Fatalf("fanout run %s with %d parts: %s", r.Line.State, len(d.Parts), r.EventTypes())
			}
			if !strings.Contains(r.EventTypes(), "subagents_started") || strings.Count(r.EventTypes(), "subagent_result") != 3 {
				t.Fatalf("fanout events %s", r.EventTypes())
			}
		}
	}

	// A router call above 1 % of the envelope budget is not made (§9.3, step 4).
	e.MustDo("PUT", e.SP("/settings/budget.envelope_usd"), map[string]any{"value": 0.0001}, nil)
	before := len(e.Fake.Requests())
	run, _ := e.Post(chief.ChatID, "[[three]] Compare again.")
	r := e.WaitRun(run, "done", "failed", "escalated")
	if d := routeOf(t, r); d.Step != "single" || !slices.Contains(d.Rules, "cost_cap") {
		t.Fatalf("cost cap: %+v", d)
	}
	for _, q := range e.Fake.Requests()[before:] {
		if strings.HasPrefix(q.System, routerSystem) {
			t.Fatal("the router was called above the cost cap")
		}
	}
}

func TestSCN_TEAM_009_ProposalsCreateOnlyOnCreate(t *testing.T) {
	e := apptest.Start(t, "sqlite")
	e.SetupModel("openai-chat", "", 128000)
	proposal := map[string]any{
		"name": "Writer", "job": "Writes the weekly note", "instructions": "Write short notes.", "zone": "personal",
		"routines": []map[string]any{{"name": "Weekly note", "cron": "0 9 * * 1", "prompt": "Write the weekly note."}},
	}
	chief := scripted(e, fakemodel.Step{ToolCalls: calls("propose_bot", proposal)}, fakemodel.Step{Text: "I proposed Writer."})
	run, _ := e.Post(chief.ChatID, "We need someone for the weekly note.")
	if r := e.WaitRun(run, "done", "failed", "escalated"); r.Line.State != "done" {
		t.Fatalf("run %s: %s", r.Line.State, r.EventTypes())
	}
	var bots struct{ Bots []apptest.Bot }
	e.MustDo("GET", e.SP("/bots"), nil, &bots)
	if len(bots.Bots) != 1 {
		t.Fatalf("a bot exists before Create: %+v", bots.Bots)
	}
	var cardID string
	for _, m := range e.Messages(chief.ChatID) {
		if c, ok := m["card"].(map[string]any); ok && c["kind"] == "proposal" && c["status"] == "pending" {
			cardID = c["id"].(string)
		}
	}
	if cardID == "" {
		t.Fatal("no proposal card")
	}
	e.MustDo("POST", e.SP("/cards/%s/answer", cardID), map[string]any{"action": "create"}, nil)
	e.MustDo("GET", e.SP("/bots"), nil, &bots)
	var writer apptest.Bot
	for _, b := range bots.Bots {
		if b.Name == "Writer" {
			writer = b
		}
	}
	if writer.ID == "" {
		t.Fatalf("Create made no Writer: %+v", bots.Bots)
	}
	var rs struct {
		Routines []struct {
			BotID   string `json:"bot_id"`
			Enabled bool   `json:"enabled"`
		} `json:"routines"`
	}
	e.MustDo("GET", e.SP("/routines"), nil, &rs)
	var mine int
	for _, r := range rs.Routines {
		if r.BotID == writer.ID {
			mine++
			if r.Enabled {
				t.Fatalf("the routine of Writer is not paused: %+v", rs.Routines)
			}
		}
	}
	if mine != 1 {
		t.Fatalf("routines %+v", rs.Routines)
	}

	// A bot that is not primary cannot propose bots.
	marker := "writer-proposes"
	e.Fake.AddScript(fakemodel.Script{Name: marker, Match: fakemodel.Match{System: "[[script:" + marker + "]]"}, Steps: []fakemodel.Step{
		{ToolCalls: calls("propose_bot", map[string]any{"name": "Helper", "job": "Helps", "instructions": "Help."})}, {Text: "Tried."},
	}})
	setInstructions(e, writer.ID, "[[script:"+marker+"]]")
	e.MustDo("GET", e.SP("/bots/%s", writer.ID), nil, &writer)
	run, _ = e.Post(writer.ChatID, "Hire a helper.")
	r := e.WaitRun(run, "done", "failed", "escalated")
	var res string
	for _, ev := range r.Events {
		if ev.Type == "tool_call" && strings.Contains(string(ev.Data), "propose_bot") {
			res = string(ev.Data)
		}
	}
	if !strings.Contains(res, "Only the primary bot proposes bots") {
		t.Fatalf("Writer's proposal: %s", res)
	}
}

func TestAppendixETeamTools(t *testing.T) {
	e := apptest.Start(t, "sqlite")
	e.SetupModel("openai-chat", "", 128000)
	chief := scripted(e,
		fakemodel.Step{ToolCalls: calls("list_bots", map[string]any{})},
		fakemodel.Step{ToolCalls: calls("reply", map[string]any{"text": "Working on it."})},
		fakemodel.Step{ToolCalls: calls("ask_user", map[string]any{"question": "Which desk?", "options": []string{"Alpha", "Beta"}})},
		fakemodel.Step{ToolCalls: calls("create_routine", map[string]any{"name": "Desk check", "cron": "0 9 * * 1", "prompt": "Check the desks."})},
		fakemodel.Step{Text: "Done."})
	run, _ := e.Post(chief.ChatID, "Plan the desk work.")
	r := e.WaitRun(run, "waiting")
	var question map[string]any
	for _, m := range e.Messages(chief.ChatID) {
		if c, ok := m["card"].(map[string]any); ok && c["kind"] == "question" {
			question = c
		}
		if m["body"] == "Working on it." && m["author_kind"] != "bot" {
			t.Fatal("reply came from the wrong author")
		}
	}
	if question == nil || question["title"] != "Which desk?" {
		t.Fatalf("no question card: %s", r.EventTypes())
	}
	e.MustDo("POST", e.SP("/cards/%s/answer", question["id"]), map[string]any{"action": "answer", "value": "Beta"}, nil)
	r = e.WaitRun(run, "waiting", "done")
	var approval map[string]any
	for _, m := range e.Messages(chief.ChatID) {
		if c, ok := m["card"].(map[string]any); ok && c["kind"] == "approval" && c["status"] == "pending" {
			approval = c
		}
	}
	if approval == nil || approval["tool"] != "create_routine" {
		t.Fatalf("create_routine did not ask: %s", r.EventTypes())
	}
	e.MustDo("POST", e.SP("/cards/%s/answer", approval["id"]), map[string]any{"action": "allow"}, nil)
	r = e.WaitRun(run, "done", "failed", "escalated")
	var results []string
	for _, ev := range r.Events {
		if ev.Type == "tool_call" || ev.Type == "tool_result" {
			results = append(results, string(ev.Data))
		}
	}
	all := strings.Join(results, "\n")
	for _, want := range []string{"Chief (primary) (you)", "Sent.", "The user answered: Beta", "Made the routine Desk check"} {
		if !strings.Contains(all, want) {
			t.Fatalf("missing %q in\n%s", want, all)
		}
	}
	sent := false
	for _, m := range e.Messages(chief.ChatID) {
		sent = sent || (m["body"] == "Working on it." && m["author_kind"] == "bot")
	}
	if !sent {
		t.Fatal("reply did not reach the chat")
	}
}
