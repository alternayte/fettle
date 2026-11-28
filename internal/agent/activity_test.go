package agent_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alternayte/fettle/internal/agent"
	"github.com/alternayte/fettle/internal/apptest"
	"github.com/alternayte/fettle/internal/chats"
	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
	"github.com/alternayte/fettle/internal/testenv"
)

// streamUntil collects the chat stream events until stop returns true or 150 s pass.
func streamUntil(t *testing.T, ch <-chan chats.Event, stop func([]chats.Event) bool) []chats.Event {
	t.Helper()
	var got []chats.Event
	timeout := time.After(150 * time.Second)
	for !stop(got) {
		select {
		case ev := <-ch:
			got = append(got, ev)
		case <-timeout:
			t.Fatalf("the stream did not finish; events %v", eventTypes(got))
		}
	}
	return got
}

func eventTypes(evs []chats.Event) []string {
	var out []string
	for _, ev := range evs {
		if ev.Type == "activity" {
			var a agent.Activity
			_ = json.Unmarshal(ev.Data, &a)
			out = append(out, "activity:"+a.Label)
			continue
		}
		out = append(out, ev.Type)
	}
	return out
}

func finalMessage(evs []chats.Event) bool {
	for _, ev := range evs {
		if ev.Type != "message" {
			continue
		}
		var m agent.MessageView
		_ = json.Unmarshal(ev.Data, &m)
		if m.AuthorKind == "bot" && !m.Streaming {
			return true
		}
	}
	return false
}

func TestActivityThinkingBeforeTheFirstToken(t *testing.T) {
	testenv.Each(t, func(t *testing.T, dialect string) {
		e := apptest.Start(t, dialect)
		e.SetupModel("openai-chat", "slow=400", 128000)
		chief := scripted(e, fakemodel.Step{Text: "Here is the answer."})
		ch, cancel := e.App.Hub.Subscribe(chats.ChatTopic(chief.ChatID))
		defer cancel()
		e.Post(chief.ChatID, "Tell me something.")
		evs := streamUntil(t, ch, finalMessage)
		types := eventTypes(evs)
		thinking := slices.Index(types, "activity:Thinking")
		delta := slices.Index(types, "delta")
		if thinking < 0 || delta < 0 || thinking > delta {
			t.Fatalf("want activity Thinking before the first delta, got %v", types)
		}
		// The final reply clears the line, and the last run line has no activity.
		var last agent.RunLine
		more := streamUntil(t, ch, func(more []chats.Event) bool {
			if n := len(more); n > 0 && more[n-1].Type == "run" {
				_ = json.Unmarshal(more[n-1].Data, &last)
				return last.State == "done"
			}
			return false
		})
		if !slices.Contains(eventTypes(more), "activity:") {
			t.Fatalf("no empty activity after the reply, got %v", eventTypes(more))
		}
		if last.Activity != nil {
			t.Fatalf("the last run line still has activity %q", last.Activity.Label)
		}
	})
}

func TestActivityNamesTheToolAndTheRunLineFollowsIt(t *testing.T) {
	testenv.Each(t, func(t *testing.T, dialect string) {
		// web_fetch needs no computer; the label of exec has its own unit test.
		e := apptest.Start(t, dialect)
		e.SetupModel("openai-chat", "", 128000)
		chief := scripted(e,
			fakemodel.Step{ToolCalls: calls("web_fetch", map[string]any{"url": "http://127.0.0.1:9/private?token=abc"})},
			fakemodel.Step{Text: "The page did not open."},
		)
		ch, cancel := e.App.Hub.Subscribe(chats.ChatTopic(chief.ChatID))
		defer cancel()
		runID, _ := e.Post(chief.ChatID, "Read the page.")
		evs := streamUntil(t, ch, finalMessage)
		r := e.WaitRun(runID, "done")
		types := eventTypes(evs)
		label := slices.IndexFunc(types, func(s string) bool { return s == "activity:Reading 127.0.0.1" })
		if label < 0 {
			t.Fatalf("no activity for the command, got %v", types)
		}
		// A run line comes after the label and before the next model turn's text: it is
		// sent after the tool result.
		runAfter := slices.Index(types[label:], "run")
		delta := slices.Index(types[label:], "delta")
		if runAfter < 0 || (delta >= 0 && runAfter > delta) {
			t.Fatalf("want a run line after the tool, got %v", types)
		}
		var out string
		for _, ev := range r.Events {
			if ev.Type == "tool_result" || ev.Type == "tool_call" {
				out = string(ev.Data)
			}
		}
		if !strings.Contains(out, `"tool":"web_fetch"`) {
			t.Fatalf("the tool did not run: %s", out)
		}
	})
}

func TestActivityRouterTimeLimitStartsTheReply(t *testing.T) {
	testenv.Each(t, func(t *testing.T, dialect string) {
		e := apptest.Start(t, dialect)
		fast := e.SetupModel("openai-chat", "", 128000)
		slow := e.SetupModel("openai-chat", "slow=3000", 128000)
		e.MustDo("PUT", e.SP("/model-roles/owner"), map[string]any{"targets": []map[string]any{{"provider_id": fast, "model": "fake/large", "context_window": 128000}}}, nil)
		e.MustDo("PUT", e.SP("/model-roles/router"), map[string]any{"targets": []map[string]any{{"provider_id": slow, "model": "fake/large", "context_window": 128000}}}, nil)
		e.MustDo("PUT", e.SP("/settings/agent.router_timeout"), map[string]any{"value": "300ms"}, nil)
		chief := scripted(e, fakemodel.Step{Text: "A quick reply."})
		start := time.Now()
		runID, _ := e.Post(chief.ChatID, "Hello there.")
		r := e.WaitRun(runID, "done")
		if d := time.Since(start); d > 2500*time.Millisecond {
			t.Fatalf("the reply took %s; the router held it", d)
		}
		if r.Line.Step != "single" {
			t.Fatalf("step %q, want single", r.Line.Step)
		}
		var route struct {
			Rules []string `json:"rules"`
		}
		var started struct {
			RouterMS int64 `json:"router_ms"`
		}
		for _, ev := range r.Events {
			switch ev.Type {
			case "route_decision":
				_ = json.Unmarshal(ev.Data, &route)
			case "run_started":
				_ = json.Unmarshal(ev.Data, &started)
			}
		}
		if !slices.Contains(route.Rules, "router_timeout") {
			t.Fatalf("route rules %v, want router_timeout", route.Rules)
		}
		if started.RouterMS < 250 || started.RouterMS > 2500 {
			t.Fatalf("router_ms %d, want about the time limit", started.RouterMS)
		}
	})
}

func TestActivityTimingsInTheJournal(t *testing.T) {
	testenv.Each(t, func(t *testing.T, dialect string) {
		e := apptest.Start(t, dialect)
		e.SetupModel("openai-chat", "slow=150", 128000)
		chief := scripted(e,
			fakemodel.Step{ToolCalls: calls("list_bots", map[string]any{})},
			fakemodel.Step{Text: "Done with the list."},
		)
		runID, _ := e.Post(chief.ChatID, "List the bots.")
		r := e.WaitRun(runID, "done")
		var started map[string]any
		var firstToken float64
		for _, ev := range r.Events {
			var d map[string]any
			_ = json.Unmarshal(ev.Data, &d)
			switch ev.Type {
			case "run_started":
				started = d
			case "model_turn":
				if v, ok := d["first_token_ms"].(float64); ok && firstToken == 0 {
					firstToken = v
				}
			}
		}
		if started == nil {
			t.Fatal("no run_started event")
		}
		// queue_ms is left out when it is 0, so only its type is checked.
		if v, ok := started["queue_ms"]; ok {
			if _, num := v.(float64); !num {
				t.Fatalf("queue_ms is %v", v)
			}
		}
		if firstToken < 100 {
			t.Fatalf("first_token_ms %v, want the fake delay of 150 ms or more", firstToken)
		}
	})
}
