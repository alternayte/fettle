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
