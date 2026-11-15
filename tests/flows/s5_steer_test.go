//go:build e2e

package flows

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/apptest"
	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
)

// slowFanout scripts the router and three helpers that each wait on slow.test (20 s), so the
// owner run waits for its sub-agents long enough to steer or stop it.
func slowFanout(e *apptest.Env, marker string) {
	e.Fake.AddScript(fakemodel.Script{Name: "owner-" + marker, Match: fakemodel.Match{System: "[[script:" + marker + "]]"}, Steps: []fakemodel.Step{{Text: "Here is what the helpers found."}}})
	var parts []map[string]any
	for i := 1; i <= 3; i++ {
		tag := fmt.Sprintf("[[part-%s-%d]]", marker, i)
		parts = append(parts, map[string]any{"goal": fmt.Sprintf("%s Read page %d of slow.test.", tag, i), "acceptance": "The page text has the page as source.", "needs": "read"})
		e.Fake.AddScript(fakemodel.Script{Name: fmt.Sprintf("part-%s-%d", marker, i), Match: fakemodel.Match{System: "You are a helper", User: tag}, Steps: []fakemodel.Step{
			{ToolCalls: call("browser_navigate", map[string]any{"url": fmt.Sprintf("http://slow.test/%d", i)})},
			{ToolCalls: call("report_result", map[string]any{"summary": "The page loaded.", "findings": []map[string]any{}, "open_items": []string{}})},
		}})
	}
	e.Fake.AddScript(fakemodel.Script{Name: "route-" + marker, Match: fakemodel.Match{System: "You route one piece of work", User: "[[" + marker + "]]"}, Steps: []fakemodel.Step{
		{JSON: map[string]any{"step": "fanout", "owner_bot_id": "", "parts": parts, "rationale": "three independent pages", "confidence": 0.9}},
	}})
}

// waitSubagents waits until a run waits for its sub-agents and returns its children.
func waitSubagents(t *testing.T, e *apptest.Env, runID string) []uuid.UUID {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		var r struct {
			Status     string `json:"status"`
			WaitReason string `json:"wait_reason"`
		}
		_ = e.App.DB.Engine.QueryRow(context.Background(), "SELECT status, wait_reason FROM runs WHERE id = $1", uuid.MustParse(runID)).Scan(&r.Status, &r.WaitReason)
		if r.Status == "waiting" && r.WaitReason == "subagents" {
			kids, err := e.App.Runs.Children(context.Background(), uuid.MustParse(runID))
			if err != nil {
				t.Fatal(err)
			}
			var ids []uuid.UUID
			for _, k := range kids {
				ids = append(ids, k.ID())
			}
			return ids
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("run %s does not wait for sub-agents: %s", runID, e.GetRun(runID).EventTypes())
	return nil
}

func TestSCN_RUN_006_SteerAndStatusQuestion(t *testing.T) {
	e := teamApp(t)

	// A message during a running run is a steer event in the next model turn.
	marker := "steer-run"
	e.Fake.AddScript(fakemodel.Script{Name: marker, Match: fakemodel.Match{System: "[[script:" + marker + "]]"}, Steps: []fakemodel.Step{
		{ToolCalls: call("exec", map[string]any{"command": "sleep 4"})},
		{Text: "I used the second source."},
	}})
	chief := e.Chief()
	var b map[string]any
	e.MustDo("GET", e.SP("/bots/%s", chief.ID), nil, &b)
	e.MustDo("PATCH", e.SP("/bots/%s", chief.ID), map[string]any{"name": b["name"], "job": b["job"], "instructions": "[[script:" + marker + "]]"}, nil)
	run, _ := e.Post(chief.ChatID, "Read the two sources and summarize them.")
	for deadline := time.Now().Add(90 * time.Second); !e.GetRun(run).HasEvent("tool_intent"); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the exec did not start: %s", e.GetRun(run).EventTypes())
		}
	}
	steeredRun, steered := e.Post(chief.ChatID, "use the second source")
	if !steered || steeredRun != run {
		t.Fatalf("the message did not steer the run: %v %s", steered, steeredRun)
	}
	r := e.WaitRun(run, "done", "failed")
	if !r.HasEvent("steer") {
		t.Fatalf("no steer event: %s", r.EventTypes())
	}
	got := false
	for _, req := range e.Fake.Requests() {
		if strings.Contains(req.System, "[[script:"+marker+"]]") {
			for _, m := range req.Messages {
				got = got || strings.Contains(fmt.Sprint(m), "use the second source")
			}
		}
	}
	if !got {
		t.Fatal("no model turn got the steer text")
	}

	// A status question during a sub-agent wait gets a direct reply; the run stays as it is.
	slowFanout(e, "status-fan")
	research := newBot(e, "Research", "Reads sources", "status-fan")
	run, _ = e.Post(research.ChatID, "[[status-fan]] Read the three slow pages.")
	waitSubagents(t, e, run)
	before := e.GetRun(run)
	requests := len(e.Fake.Requests())
	if _, steered := e.Post(research.ChatID, "how far are you?"); steered {
		t.Fatal("the status question became a steer event")
	}
	var reply string
	for _, m := range e.Messages(research.ChatID) {
		if m["author_kind"] == "bot" && strings.Contains(fmt.Sprint(m["body"]), "helpers") {
			reply = fmt.Sprint(m["body"])
		}
	}
	if !strings.Contains(reply, "0 of 3 are done") {
		t.Fatalf("no status reply from the run's events: %q", reply)
	}
	after := e.GetRun(run)
	if after.Line.State != "waiting" || len(after.Events) != len(before.Events) || after.HasEvent("steer") {
		t.Fatalf("the status question changed the run: %s -> %s %s", before.Line.State, after.Line.State, after.EventTypes())
	}
	for _, req := range e.Fake.Requests()[requests:] {
		if strings.Contains(req.LastUser, "how far are you") {
			t.Fatal("the status reply called a model")
		}
	}
	e.MustDo("POST", e.SP("/runs/%s/stop", run), nil, nil)
}

func TestSCN_RUN_007_StopCancelsRunAndSubagents(t *testing.T) {
	e := teamApp(t)
	slowFanout(e, "stop-fan")
	research := newBot(e, "Research", "Reads sources", "stop-fan")
	run, _ := e.Post(research.ChatID, "[[stop-fan]] Read the three slow pages.")
	kids := waitSubagents(t, e, run)
	if len(kids) != 3 {
		t.Fatalf("%d sub-agent runs, want 3", len(kids))
	}
	e.MustDo("POST", e.SP("/runs/%s/stop", run), nil, nil)
	e.WaitRun(run, "cancelled")
	for _, k := range kids {
		if r := e.WaitRun(k.String(), "cancelled", "done", "failed"); r.Line.State != "cancelled" {
			t.Fatalf("sub-agent run %s is %s, want cancelled", k, r.Line.State)
		}
	}
}
