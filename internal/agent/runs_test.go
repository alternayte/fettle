package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/agent"
	"github.com/alternayte/fettle/internal/apptest"
	"github.com/alternayte/fettle/internal/envelopes"
	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
	"github.com/alternayte/fettle/internal/runs"
	"github.com/alternayte/fettle/internal/testenv"
)

func calls(name string, args map[string]any) []fakemodel.ScriptCall {
	return []fakemodel.ScriptCall{{Name: name, Args: args}}
}

// scripted sets Chief's instructions to a new script and returns Chief.
func scripted(e *apptest.Env, steps ...fakemodel.Step) apptest.Bot {
	e.T.Helper()
	name := fmt.Sprintf("s%d", time.Now().UnixNano())
	e.Fake.AddScript(fakemodel.Script{Name: name, Match: fakemodel.Match{System: "[[script:" + name + "]]"}, Steps: steps})
	chief := e.Chief()
	setInstructions(e, chief.ID, "[[script:"+name+"]]")
	return chief
}

// requestsFor counts the fake model requests whose system prompt holds a marker.
func requestsFor(e *apptest.Env, marker string) []fakemodel.Recorded {
	var out []fakemodel.Recorded
	for _, r := range e.Fake.Requests() {
		if strings.Contains(r.System, marker) {
			out = append(out, r)
		}
	}
	return out
}

func TestSCN_RUN_001_ResumeAfterStepTwentyFromJournal(t *testing.T) {
	testenv.Each(t, func(t *testing.T, dialect string) {
		e := apptest.Start(t, dialect)
		e.SetupModel("openai-chat", "", 1_000_000)
		block := make(chan struct{})
		blocked := make(chan struct{})
		var calls9 atomic.Int32
		e.App.Agent.Tools.Add(agent.Tool{
			Name: "report", Description: "Read part n of the report.", Class: envelopes.ClassRead,
			Schema: json.RawMessage(`{"type":"object","properties":{"n":{"type":"integer"}},"required":["n"]}`),
			Run: func(ctx context.Context, _ *agent.ToolEnv, args json.RawMessage) (agent.ToolOutput, error) {
				var in struct{ N int }
				_ = json.Unmarshal(args, &in)
				if in.N == 9 && calls9.Add(1) == 1 {
					close(blocked)
					select { // the first attempt stops here, after step 20
					case <-block:
					case <-ctx.Done():
					}
				}
				if in.N == 5 {
					return agent.ToolOutput{Text: strings.Repeat("large report line\n", 3000)}, nil // 54 KB
				}
				return agent.ToolOutput{Text: fmt.Sprintf("part %d", in.N)}, nil
			},
		})
		var steps []fakemodel.Step
		for i := 0; i < 19; i++ {
			steps = append(steps, fakemodel.Step{ToolCalls: calls("report", map[string]any{"n": i})})
		}
		steps = append(steps, fakemodel.Step{Text: "The report is read."})
		chief := scripted(e, steps...)
		runID, _ := e.Post(chief.ChatID, "Read the report.")
		t.Cleanup(func() {
			select {
			case <-block:
			default:
				close(block)
			}
		})
		select {
		case <-blocked:
		case <-time.After(30 * time.Second):
			t.Fatalf("the run did not reach step 20: %s", e.GetRun(runID).EventTypes())
		}
		ctx := context.Background()
		id := uuid.MustParse(runID)
		// Another attempt takes the run, as River does after a rescue: the fence moves on.
		if _, err := e.App.DB.Engine.Exec(ctx, "UPDATE runs SET fence = fence + 1 WHERE id = $1", id); err != nil {
			t.Fatal(err)
		}
		before, _ := e.App.Journal.Load(ctx, id)
		if len(before) != 20 {
			t.Fatalf("the first attempt stopped after %d steps, want 20", len(before))
		}
		if err := e.App.Runs.Wake(ctx, id, "interactive"); err != nil {
			t.Fatal(err)
		}
		r := e.WaitRun(runID, "done", "failed")
		close(block) // the old attempt's append now fails on the stale fence
		block = make(chan struct{})
		if r.Line.State != "done" {
			t.Fatalf("state %s: %s", r.Line.State, r.EventTypes())
		}
		evs, err := e.App.Journal.Load(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if len(evs) != 41 {
			t.Fatalf("journal has %d steps, want 41: %s", len(evs), r.EventTypes())
		}
		var s21 struct {
			Tool string `json:"tool"`
			Args struct {
				N int `json:"n"`
			} `json:"args"`
		}
		_ = json.Unmarshal([]byte(evs[20].Data), &s21)
		if evs[20].Seq != 21 || evs[20].Type != runs.EvToolCall || s21.Tool != "report" || s21.Args.N != 9 {
			t.Fatalf("step 21 is %s %s", evs[20].Type, evs[20].Data)
		}
		// The fake model got 20 requests: one per model turn, none repeated.
		reqs := requestsFor(e, "[[script:")
		if len(reqs) != 20 {
			t.Fatalf("model requests %d, want 20", len(reqs))
		}
		seen := map[int]bool{}
		for _, q := range reqs {
			if seen[len(q.Messages)] {
				t.Fatalf("a model request repeated (%d messages)", len(q.Messages))
			}
			seen[len(q.Messages)] = true
		}
		// The large output is in the object store; the event keeps a preview.
		page, err := e.App.Journal.Page(ctx, id, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, ev := range page {
			if ev.PayloadKey != nil {
				found = true
				var st struct {
					Preview   string `json:"preview"`
					Truncated bool   `json:"truncated"`
				}
				_ = json.Unmarshal([]byte(ev.Data), &st)
				if !st.Truncated || len(st.Preview) > 500 {
					t.Fatalf("stored event keeps %d preview bytes", len(st.Preview))
				}
				rc, err := e.App.Objects.Get(ctx, *ev.PayloadKey)
				if err != nil {
					t.Fatalf("payload not in the object store: %v", err)
				}
				_ = rc.Close()
			}
		}
		if !found {
			t.Fatal("no event put its payload in the object store")
		}
	})
}

// jobsOf counts the River jobs of a run that are not finished.
func jobsOf(t *testing.T, e *apptest.Env, dialect string, runID string) int {
	t.Helper()
	q := "SELECT COUNT(*) FROM river_job WHERE CAST(args AS TEXT) LIKE $1 AND state IN ('available','running','scheduled','retryable','pending')"
	if dialect == "postgres" {
		q = "SELECT COUNT(*) FROM river_job WHERE args::text LIKE $1 AND state IN ('available','running','scheduled','retryable','pending')"
	}
	var n int
	if err := e.App.DB.Engine.QueryRow(context.Background(), q, "%"+runID+"%").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSCN_RUN_003_QueuesAndWaitsHoldNoWorker(t *testing.T) {
	testenv.Each(t, func(t *testing.T, dialect string) {
		e := apptest.Start(t, dialect)
		e.SetupModel("openai-chat", "", 128000)
		ctx := context.Background()
		release := make(chan struct{})
		var started sync.WaitGroup
		started.Add(2)
		e.App.Agent.Tools.Add(agent.Tool{
			Name: "long_read", Description: "Read slowly.", Class: envelopes.ClassRead, Schema: json.RawMessage(`{"type":"object"}`),
			Run: func(ctx context.Context, _ *agent.ToolEnv, _ json.RawMessage) (agent.ToolOutput, error) {
				started.Done()
				select {
				case <-release:
				case <-ctx.Done():
				}
				return agent.ToolOutput{Text: "read"}, nil
			},
		})
		e.App.Agent.Tools.Add(agent.Tool{
			Name: "notify_team", Description: "Send a note to the team.", Class: envelopes.ClassSend, Schema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`),
			Run: func(context.Context, *agent.ToolEnv, json.RawMessage) (agent.ToolOutput, error) {
				return agent.ToolOutput{Text: "sent"}, nil
			},
		})
		e.Fake.AddScript(fakemodel.Script{Name: "routine-slow", Match: fakemodel.Match{User: "[[routine-slow]]"}, Steps: []fakemodel.Step{{ToolCalls: calls("long_read", nil)}, {Text: "Routine done."}}})
		e.Fake.AddScript(fakemodel.Script{Name: "ask-send", Match: fakemodel.Match{User: "[[ask-send]]"}, Steps: []fakemodel.Step{{ToolCalls: calls("notify_team", map[string]any{"text": "hi"})}, {Text: "Sent."}}})
		chief := e.Chief()
		bot := uuid.MustParse(chief.ID)
		sp := uuid.MustParse(e.Space)
		// Two routine runs fill both background workers.
		var bg []*runs.Run
		for i := 0; i < 2; i++ {
			r, err := e.App.Agent.Submit(ctx, &envelopes.Envelope{SpaceID: sp, Kind: "routine", Origin: "routine", Requester: envelopes.Requester{Type: "routine", ID: "r1"}, OwnerBotID: &bot, Goal: "[[routine-slow]] tidy the notes", Capabilities: agent.UserCapabilities})
			if err != nil {
				t.Fatal(err)
			}
			bg = append(bg, r)
		}
		t.Cleanup(func() {
			select {
			case <-release:
			default:
				close(release)
			}
		})
		waited := make(chan struct{})
		go func() { started.Wait(); close(waited) }()
		select {
		case <-waited:
		case <-time.After(30 * time.Second):
			t.Fatal("the background runs did not start")
		}
		// An interactive message starts at once on the interactive queue.
		start := time.Now()
		run, _ := e.Post(chief.ChatID, "[[ask-send]] tell the team hello")
		r := e.WaitRun(run, "waiting")
		if time.Since(start) > 5*time.Second {
			t.Fatalf("the interactive run waited %s behind background work", time.Since(start))
		}
		for _, b := range bg {
			if got := e.GetRun(b.ID().String()).Line.State; got != "running" {
				t.Fatalf("background run is %s, want running", got)
			}
		}
		// A run that waits for approval has no job.
		if !r.HasEvent("approval_requested") {
			t.Fatalf("events %s", r.EventTypes())
		}
		// The attempt that parked the run ends its job; River marks it complete at once.
		for deadline := time.Now().Add(3 * time.Second); jobsOf(t, e, dialect, run) != 0; {
			if time.Now().After(deadline) {
				t.Fatalf("a waiting run has %d jobs", jobsOf(t, e, dialect, run))
			}
			time.Sleep(50 * time.Millisecond)
		}
		var card map[string]any
		for _, m := range e.Messages(chief.ChatID) {
			if c, ok := m["card"].(map[string]any); ok && c["kind"] == "approval" {
				card = c
			}
		}
		e.MustDo("POST", e.SP("/cards/%s/answer", card["id"]), map[string]any{"action": "allow"}, nil)
		var answered, queued int
		q := "SELECT COUNT(*) FROM river_job WHERE CAST(args AS TEXT) LIKE $1"
		if dialect == "postgres" {
			q = "SELECT COUNT(*) FROM river_job WHERE args::text LIKE $1"
		}
		_ = e.App.DB.Engine.QueryRow(ctx, q, "%"+run+"%").Scan(&queued)
		_ = e.App.DB.Engine.QueryRow(ctx, "SELECT COUNT(*) FROM approvals WHERE run_id = $1 AND status = 'allowed'", uuid.MustParse(run)).Scan(&answered)
		if answered != 1 || queued < 2 {
			t.Fatalf("after the answer: %d allowed approvals, %d jobs of the run", answered, queued)
		}
		e.WaitRun(run, "done")
		close(release)
		release = make(chan struct{})
		for _, b := range bg {
			e.WaitRun(b.ID().String(), "done")
		}
	})
}
