package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/agent"
	"github.com/alternayte/fettle/internal/apptest"
	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/chats"
	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
	"github.com/alternayte/fettle/internal/testenv"
	"github.com/alternayte/fettle/internal/usage"
)

func setInstructions(e *apptest.Env, botID, text string) {
	e.T.Helper()
	var b map[string]any
	e.MustDo("GET", e.SP("/bots/%s", botID), nil, &b)
	e.MustDo("PATCH", e.SP("/bots/%s", botID), map[string]any{"name": b["name"], "job": b["job"], "instructions": text}, nil)
}

func TestSCN_AGT_003_CompactionAtSeventyPercent(t *testing.T) {
	testenv.Each(t, func(t *testing.T, dialect string) {
		e := apptest.Start(t, dialect)
		e.SetupModel("openai-chat", "", 3000)
		long := strings.Repeat("The desk survey keeps going with more detail about prices and sizes. ", 30)
		e.Fake.AddScript(fakemodel.Script{Name: "compact", Match: fakemodel.Match{System: "[[script:compact]]"}, Steps: []fakemodel.Step{
			{Text: long, ToolCalls: []fakemodel.ScriptCall{{Name: "search_history", Args: map[string]any{"query": "desk"}}}, Repeat: true},
		}})
		e.MustDo("PUT", e.SP("/settings/agent.max_turns"), map[string]any{"value": 8}, nil)
		chief := e.Chief()
		setInstructions(e, chief.ID, "[[script:compact]]")
		run, _ := e.Post(chief.ChatID, "Survey the desks.")
		r := e.WaitRun(run, "escalated", "done", "failed")
		types := strings.Split(r.EventTypes(), ",")
		sumAt := -1
		for i, ty := range types {
			if ty == "summary" {
				sumAt = i
				break
			}
		}
		if sumAt < 0 {
			t.Fatalf("no summary event: %s", r.EventTypes())
		}
		after := 0
		for _, ty := range types[sumAt:] {
			if ty == "model_turn" {
				after++
			}
		}
		if after == 0 {
			t.Fatalf("the run did not continue after the summary: %s", r.EventTypes())
		}
		var sum struct {
			Through int64  `json:"through_seq"`
			Text    string `json:"text"`
		}
		_ = json.Unmarshal(r.Events[sumAt].Data, &sum)
		// The next request to the owner model has the summary instead of the older events.
		found := false
		for _, req := range e.Fake.Requests() {
			for _, m := range req.Messages {
				if strings.Contains(m.Text, "Summary of your earlier steps") && strings.Contains(m.Text, sum.Text[:20]) {
					found = true
				}
			}
		}
		if !found || sum.Through <= 0 {
			t.Fatalf("no request carried the summary (through %d)", sum.Through)
		}
	})
}

func TestSCN_AGT_005_TurnLimitEscalatesWithCard(t *testing.T) {
	testenv.Each(t, func(t *testing.T, dialect string) {
		e := apptest.Start(t, dialect)
		e.SetupModel("anthropic-messages", "", 128000)
		e.MustDo("PUT", e.SP("/settings/agent.max_turns"), map[string]any{"value": 4}, nil)
		chief := e.Chief()
		run, _ := e.Post(chief.ChatID, "[[never-ends]] keep going")
		r := e.WaitRun(run, "escalated", "done", "failed")
		if r.Line.State != "escalated" {
			t.Fatalf("state %s: %s", r.Line.State, r.EventTypes())
		}
		if n := strings.Count(r.EventTypes(), "model_turn"); n != 4 {
			t.Fatalf("model turns %d, want 4", n)
		}
		var card map[string]any
		for _, m := range e.Messages(chief.ChatID) {
			if c, ok := m["card"].(map[string]any); ok && c["kind"] == "turn_limit" {
				card = c
			}
		}
		if card == nil || card["status"] != "pending" {
			t.Fatalf("no pending turn_limit card")
		}
		e.MustDo("POST", e.SP("/cards/%s/answer", card["id"]), map[string]any{"action": "stop"}, nil)
		e.WaitRun(run, "cancelled")
	})
}

func TestSCN_BOT_007_SearchHistoryPages(t *testing.T) {
	testenv.Each(t, func(t *testing.T, dialect string) {
		e := apptest.Start(t, dialect)
		ctx := context.Background()
		chief := e.Chief()
		sp, chat, bot := uuid.MustParse(e.Space), uuid.MustParse(chief.ChatID), uuid.MustParse(chief.ID)
		for i := 0; i < 500; i++ {
			body := fmt.Sprintf("Note %d about the office plants.", i)
			if i%50 == 7 {
				body = fmt.Sprintf("Note %d: the zebra lamp arrives on Friday.", i)
			}
			m := &chats.Message{SpaceID: sp, ChatID: chat, AuthorKind: "user", AuthorID: e.Owner, Body: body, SentAt: time.Now().UTC().Add(time.Duration(i-1000) * time.Minute)}
			if err := e.App.Chats.Insert(ctx, m, &bot); err != nil {
				t.Fatal(err)
			}
		}
		tool, _ := e.App.Agent.Tools.Get("search_history")
		env := &agent.ToolEnv{SpaceID: sp, BotID: bot, Actor: audit.Actor{Kind: "bot", ID: chief.ID}}
		out, err := tool.Run(ctx, env, json.RawMessage(`{"query":"zebra lamp","limit":6}`))
		if err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(out.Text, "zebra lamp"); n != 6 || !strings.Contains(out.Text, "next_cursor:") {
			t.Fatalf("page 1 has %d matches: %s", n, out.Text)
		}
		var cursor int64
		_, after, _ := strings.Cut(out.Text, "next_cursor:")
		fmt.Sscanf(after, " %d", &cursor)
		out2, err := tool.Run(ctx, env, json.RawMessage(fmt.Sprintf(`{"query":"zebra lamp","limit":6,"cursor":%d}`, cursor)))
		if err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(out2.Text, "zebra lamp"); n != 4 || strings.Contains(out2.Text, "next_cursor:") {
			t.Fatalf("page 2 has %d matches: %s", n, out2.Text)
		}
		// Other bots do not see this bot's history.
		other := &agent.ToolEnv{SpaceID: sp, BotID: uuid.New()}
		out3, _ := tool.Run(ctx, other, json.RawMessage(`{"query":"zebra"}`))
		if !strings.Contains(out3.Text, "No matches") {
			t.Fatalf("another bot saw history: %s", out3.Text)
		}
	})
}

func TestSCN_MOD_004_BotOverrideForOwnerRole(t *testing.T) {
	testenv.Each(t, func(t *testing.T, dialect string) {
		e := apptest.Start(t, dialect)
		spaceProvider := e.SetupModel("openai-chat", "", 128000)
		var p apptest.Provider
		e.MustDo("POST", e.SP("/model-providers"), map[string]any{"name": "override", "api_type": "openai-responses", "base_url": e.FakeURL + "/v1", "key": "k-override-1234"}, &p)
		e.MustDo("POST", e.SP("/model-providers/%s/test", p.ID), map[string]any{"model": "fake/small"}, nil)
		chief := e.Chief()
		e.MustDo("PUT", e.SP("/model-roles/owner"), map[string]any{"bot_id": chief.ID, "targets": []map[string]any{{"provider_id": p.ID, "model": "fake/small", "context_window": 4000}}}, nil)
		e.Fake.Reset()
		run, _ := e.Post(chief.ChatID, "hello override")
		e.WaitRun(run, "done")
		reqs := e.Fake.Requests()
		if len(reqs) == 0 || reqs[0].Model != "fake/small" || reqs[0].API != "openai-responses" {
			t.Fatalf("owner request went to %+v", reqs)
		}
		ctx := context.Background()
		bot := uuid.MustParse(chief.ID)
		sum, err := e.App.Models.Resolve(ctx, uuid.MustParse(e.Space), &bot, "summarizer")
		if err != nil || sum[0].Target.ProviderID.String() != spaceProvider || sum[0].Target.Model != "fake/large" {
			t.Fatalf("summarizer resolves to %+v, %v", sum, err)
		}
		// Removing the override returns the bot to the space target.
		e.MustDo("PUT", e.SP("/model-roles/owner"), map[string]any{"bot_id": chief.ID, "targets": []map[string]any{}}, nil)
		own, _ := e.App.Models.Resolve(ctx, uuid.MustParse(e.Space), &bot, "owner")
		if own[0].Target.Model != "fake/large" {
			t.Fatalf("owner after override removal: %+v", own[0].Target)
		}
	})
}

func TestSCN_MOD_007_CostRecordedAndViewsSum(t *testing.T) {
	testenv.Each(t, func(t *testing.T, dialect string) {
		e := apptest.Start(t, dialect)
		e.SetupModel("openai-chat", "cost=0.0123", 128000)
		chief := e.Chief()
		run, _ := e.Post(chief.ChatID, "first call")
		e.WaitRun(run, "done")
		// The second call has no reported cost (Anthropic API), so cost = tokens x prices.
		var p apptest.Provider
		e.MustDo("POST", e.SP("/model-providers"), map[string]any{"name": "priced", "api_type": "anthropic-messages", "base_url": e.FakeURL + "/v1", "key": "k-priced-1234"}, &p)
		e.MustDo("POST", e.SP("/model-providers/%s/test", p.ID), map[string]any{"model": "fake/large"}, nil)
		e.MustDo("PUT", e.SP("/model-roles/owner"), map[string]any{"targets": []map[string]any{{"provider_id": p.ID, "model": "fake/large", "price_in": 3, "price_out": 15, "context_window": 128000}}}, nil)
		run2, _ := e.Post(chief.ChatID, "second call")
		e.WaitRun(run2, "done")

		ctx := context.Background()
		rows, err := e.App.DB.Engine.Query(ctx, "SELECT run_id, tokens_in, tokens_out, cost_usd, reported, role FROM usage_events WHERE space_id = $1", uuid.MustParse(e.Space))
		if err != nil {
			t.Fatal(err)
		}
		var total float64
		n := 0
		for rows.Next() {
			var rid uuid.UUID
			var in, out int64
			var cost float64
			var reported bool
			var role string
			if err := rows.Scan(&rid, &in, &out, &cost, &reported, &role); err != nil {
				t.Fatal(err)
			}
			total += cost
			if role != "owner" {
				continue // the router call of each run (§9.3)
			}
			n++
			switch rid.String() {
			case run:
				if !reported || cost != 0.0123 {
					t.Fatalf("reported cost row: %v %v", reported, cost)
				}
			case run2:
				want := (float64(in)*3 + float64(out)*15) / 1e6
				if reported || math.Abs(cost-want) > 1e-12 {
					t.Fatalf("computed cost %v, want %v", cost, want)
				}
			}
		}
		rows.Close()
		if n != 2 {
			t.Fatalf("usage rows %d", n)
		}
		for dim := range usage.Dimensions {
			var v struct{ Rows []usage.Row }
			e.MustDo("GET", e.SP("/usage?by=%s", dim), nil, &v)
			var sum float64
			for _, r := range v.Rows {
				sum += r.CostUSD
			}
			if math.Abs(sum-total) > 1e-9 {
				t.Fatalf("view %s sums to %v, rows sum to %v", dim, sum, total)
			}
		}
	})
}

func TestSCN_RUN_008_TimerWakesRunAndStatesLateStart(t *testing.T) {
	testenv.Each(t, func(t *testing.T, dialect string) {
		e := apptest.Start(t, dialect)
		e.SetupModel("openai-chat", "", 128000)
		due := time.Now().UTC().Add(1500 * time.Millisecond).Truncate(time.Second)
		e.Fake.AddScript(fakemodel.Script{Name: "sleep", Match: fakemodel.Match{User: "[[sleep-test]]"}, Steps: []fakemodel.Step{
			{ToolCalls: []fakemodel.ScriptCall{{Name: "sleep_until", Args: map[string]any{"time": due.Format(time.RFC3339), "reason": "the report is due"}}}},
			{Text: "I woke up and wrote the report."},
		}})
		chief := e.Chief()
		run, _ := e.Post(chief.ChatID, "[[sleep-test]] write the report at nine")
		r := e.WaitRun(run, "waiting")
		if !r.HasEvent("sleep_until") {
			t.Fatalf("events %s", r.EventTypes())
		}
		// The clock moves to 20 minutes after the due time, as after downtime.
		late := due.Add(20 * time.Minute)
		e.App.Agent.SetClock(func() time.Time { return late.Add(time.Since(due.Add(-time.Second))) })
		e.WaitRun(run, "done")
		var last fakemodel.Recorded
		for _, req := range e.Fake.Requests() {
			last = req
		}
		if !strings.Contains(last.System, "was due at "+due.In(time.UTC).Format("15:04")) || !strings.Contains(last.System, "started at") {
			t.Fatalf("context does not state the times: %s", last.System)
		}
	})
}
