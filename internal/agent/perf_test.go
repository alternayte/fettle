//go:build !race

// The load and overhead scenarios measure time, so they run without the race detector.
// `just test` runs them in a separate step.

package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/agent"
	"github.com/alternayte/fettle/internal/app"
	"github.com/alternayte/fettle/internal/apptest"
	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/envelopes"
	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
	"github.com/alternayte/fettle/internal/runs"
	"github.com/alternayte/fettle/internal/testenv"
)

// hostComputers starts an app whose zones use the host provider, turned on.
func hostComputers(t *testing.T, dialect string, opts ...apptest.Option) *apptest.Env {
	t.Helper()
	if os.Getenv("FETTLE_AGENTD_BIN") == "" {
		bin := filepath.Join(t.TempDir(), "fettle-agentd")
		_, file, _, _ := goruntime.Caller(0)
		cmd := exec.Command("go", "build", "-o", bin, "./cmd/fettle-agentd")
		cmd.Dir = filepath.Join(filepath.Dir(file), "..", "..")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build agentd: %v\n%s", err, out)
		}
		t.Setenv("FETTLE_AGENTD_BIN", bin)
	}
	t.Setenv("FETTLE_DEFAULT_PROVIDER", "host")
	e := apptest.Start(t, dialect, opts...)
	if err := e.App.Computers.SetEnabled(context.Background(), audit.Actor{Kind: "system", ID: "test"}, nil, "host", true); err != nil {
		t.Fatal(err)
	}
	return e
}

func waitDone(t *testing.T, e *apptest.Env, ids []uuid.UUID, limit time.Duration) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(limit)
	for _, id := range ids {
		for {
			r, err := e.App.Runs.Find(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if runs.Terminal(r.Status) || r.Status == runs.StatusEscalated {
				if r.Status != runs.StatusDone {
					t.Fatalf("run %s is %s", id, r.Status)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("run %s is still %s", id, r.Status)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}

func TestSCN_RUN_009_TwoHundredConcurrentRuns(t *testing.T) {
	e := hostComputers(t, "postgres", func(c *app.Config) { c.Workers = 32 })
	e.SetupModel("openai-chat", "", 128000)
	e.Fake.AddScript(fakemodel.Script{Name: "echo2", Match: fakemodel.Match{User: "[[echo2]]"}, Steps: []fakemodel.Step{
		{ToolCalls: calls("exec", map[string]any{"command": "echo one"})}, {ToolCalls: calls("exec", map[string]any{"command": "echo two"})}, {Text: "Echoed."},
	}})
	ctx := context.Background()
	bot := uuid.MustParse(e.Chief().ID)
	sp := uuid.MustParse(e.Space)
	ids := make([]uuid.UUID, 200)
	var wg sync.WaitGroup
	errs := make(chan error, 200)
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := e.App.Agent.Submit(ctx, &envelopes.Envelope{SpaceID: sp, Kind: "message", Origin: "user", Requester: envelopes.Requester{Type: "user", ID: e.Owner}, OwnerBotID: &bot, Goal: fmt.Sprintf("[[echo2]] run %d", i), Capabilities: agent.UserCapabilities})
			if err != nil {
				errs <- err
				return
			}
			ids[i] = r.ID()
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	waitDone(t, e, ids, 4*time.Minute)
	for _, id := range ids {
		evs, err := e.App.Journal.Load(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		types := map[string]int{}
		for k, ev := range evs {
			if ev.Seq != int64(k+1) {
				t.Fatalf("run %s: event %d has seq %d", id, k+1, ev.Seq)
			}
			types[ev.Type]++
		}
		if types[runs.EvRunStarted] != 1 || types[runs.EvRunFinished] != 1 || types[runs.EvToolResult] != 2 || types[runs.EvModelTurn] != 3 {
			t.Fatalf("run %s has lost or duplicate events: %v", id, types)
		}
	}
}

func TestSCN_RUN_010_OverheadPerStepAndDirectReply(t *testing.T) {
	testenv.Each(t, func(t *testing.T, dialect string) {
		e := hostComputers(t, dialect)
		e.SetupModel("openai-chat", "", 4_000_000)
		e.MustDo("PUT", e.SP("/settings/agent.max_turns"), map[string]any{"value": 600}, nil)
		e.MustDo("PUT", e.SP("/settings/budget.bot_day_usd"), map[string]any{"value": 100000}, nil)
		e.MustDo("PUT", e.SP("/settings/budget.space_month_usd"), map[string]any{"value": 100000}, nil)
		var steps []fakemodel.Step
		for i := 0; i < 499; i++ {
			steps = append(steps, fakemodel.Step{ToolCalls: calls("read_file", map[string]any{"path": "MEMORY-probe.txt"})})
		}
		steps = append(steps, fakemodel.Step{Text: "All steps done."})
		chief := scripted(e, steps...)
		ctx := context.Background()
		bot := uuid.MustParse(chief.ID)
		sp := uuid.MustParse(e.Space)
		r, err := e.App.Agent.Submit(ctx, &envelopes.Envelope{SpaceID: sp, Kind: "message", Origin: "user", Requester: envelopes.Requester{Type: "user", ID: e.Owner}, OwnerBotID: &bot, Goal: "Do every step.", Capabilities: agent.UserCapabilities, Budget: envelopes.Budget{USD: 10_000, Tokens: 1 << 40, WallMS: 3_600_000, HopsRemaining: 4, MaxSubagents: 5}})
		if err != nil {
			t.Fatal(err)
		}
		waitDone(t, e, []uuid.UUID{r.ID()}, 5*time.Minute)
		evs, err := e.App.Journal.Load(ctx, r.ID())
		if err != nil {
			t.Fatal(err)
		}
		if len(evs) < 1000 {
			t.Fatalf("the run has %d steps, want 1,000", len(evs))
		}
		var over []time.Duration
		for k := 2; k < len(evs); k++ {
			var d struct {
				Duration int64 `json:"duration_ms"`
			}
			_ = json.Unmarshal([]byte(evs[k].Data), &d)
			o := evs[k].At.Sub(evs[k-1].At) - time.Duration(d.Duration)*time.Millisecond
			over = append(over, max(o, 0))
		}
		sort.Slice(over, func(i, j int) bool { return over[i] < over[j] })
		p95 := over[len(over)*95/100]
		t.Logf("%s: %d steps, overhead p50 %s p95 %s", dialect, len(evs), over[len(over)/2], p95)
		if p95 > 50*time.Millisecond {
			t.Fatalf("overhead per step p95 is %s, above 50 ms", p95)
		}

		direct := "direct"
		dr, err := e.App.Agent.Submit(ctx, &envelopes.Envelope{SpaceID: sp, Kind: "message", Origin: "user", Requester: envelopes.Requester{Type: "user", ID: e.Owner}, OwnerBotID: &bot, Goal: "Say hello.", Step: &direct, Capabilities: agent.UserCapabilities})
		if err != nil {
			t.Fatal(err)
		}
		waitDone(t, e, []uuid.UUID{dr.ID()}, time.Minute)
		devs, _ := e.App.Journal.Load(ctx, dr.ID())
		if len(devs) != 2 || devs[0].Type != runs.EvRunStarted || devs[1].Type != runs.EvRunFinished {
			t.Fatalf("a direct reply wrote %d journal events", len(devs))
		}
	})
}
