package app_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alternayte/fettle/internal/agent"
	"github.com/alternayte/fettle/internal/apptest"
	"github.com/alternayte/fettle/internal/envelopes"
	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
)

type signalRow struct {
	Type   string   `json:"type"`
	RunID  string   `json:"run_id"`
	BotID  string   `json:"bot_id"`
	Step   string   `json:"step"`
	Skills []string `json:"skills"`
	Detail string   `json:"detail"`
}

func signalsOf(e *apptest.Env) []signalRow {
	var out struct {
		Signals []signalRow `json:"signals"`
	}
	e.MustDo("GET", e.SP("/learning/signals"), nil, &out)
	return out.Signals
}

func waitSignal(t *testing.T, e *apptest.Env, typ string) signalRow {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		for _, s := range signalsOf(e) {
			if s.Type == typ {
				return s
			}
		}
	}
	t.Fatalf("no %s signal: %+v", typ, signalsOf(e))
	return signalRow{}
}

// pendingCard waits for a pending card of a kind in a chat.
func pendingCard(t *testing.T, e *apptest.Env, chatID, kind string) map[string]any {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		for _, m := range e.Messages(chatID) {
			if c, ok := m["card"].(map[string]any); ok && c["kind"] == kind && c["status"] == "pending" {
				return c
			}
		}
	}
	t.Fatalf("no pending %s card", kind)
	return nil
}

// lastBotMessage returns the newest bot message of a chat with a body.
func lastBotMessage(e *apptest.Env, chatID string) map[string]any {
	msgs := e.Messages(chatID)
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i]["author_kind"] == "bot" && msgs[i]["body"] != "" {
			return msgs[i]
		}
	}
	return nil
}

func TestSCN_LRN_001_EachSignalSourceRecordsASignal(t *testing.T) {
	e := apptest.Start(t, "sqlite")
	e.SetupModel("openai-chat", "", 128000)
	chief := e.Chief()
	const skill = "onboarding-interview" // the Chief's skill from the template; /name loads it

	// send_note sends a note: a send-class action that asks first. The first call of the
	// [[crash]] note panics after its intent, like a server that stops mid-send.
	var mu sync.Mutex
	var sentBodies []string
	var crashed atomic.Bool
	e.App.Agent.Tools.Add(agent.Tool{
		Name: "send_note", Description: "Send a note to the team.", Class: envelopes.ClassSend,
		Schema: json.RawMessage(`{"type":"object","properties":{"to":{"type":"string"},"body":{"type":"string"}},"required":["to","body"]}`),
		Run: func(_ context.Context, _ *agent.ToolEnv, args json.RawMessage) (agent.ToolOutput, error) {
			var a struct{ Body string }
			_ = json.Unmarshal(args, &a)
			if strings.Contains(a.Body, "[[crash]]") && crashed.CompareAndSwap(false, true) {
				panic("the server stops while the note is in flight")
			}
			mu.Lock()
			sentBodies = append(sentBodies, a.Body)
			mu.Unlock()
			return agent.ToolOutput{Text: "Sent."}, nil
		},
	})
	note := func(marker, body string) {
		e.Fake.AddScript(fakemodel.Script{Name: marker, Match: fakemodel.Match{User: marker}, Steps: []fakemodel.Step{
			{ToolCalls: []fakemodel.ScriptCall{{Name: "send_note", Args: map[string]any{"to": "team", "body": body}}}}, {Text: "The note is handled."},
		}})
	}
	check := func(s signalRow, wantRun bool) {
		t.Helper()
		if s.BotID != chief.ID || (wantRun && s.RunID == "") || s.Step == "" {
			t.Fatalf("signal %+v lacks its run, bot or step", s)
		}
		if wantRun && (len(s.Skills) != 1 || s.Skills[0] != skill) {
			t.Fatalf("signal %+v lacks the skill of its run", s)
		}
	}

	// thumbs_down: a 👎 on a reply.
	e.Fake.AddScript(fakemodel.Script{Name: "plain", Match: fakemodel.Match{User: "[[plain]]"}, Steps: []fakemodel.Step{{Text: "Here are three desks."}}})
	run, _ := e.Post(chief.ChatID, "/"+skill+" [[plain]] Find three desks.")
	e.WaitRun(run, "done")
	reply := lastBotMessage(e, chief.ChatID)
	e.MustDo("PUT", e.SP("/chats/%s/messages/%s/reaction", chief.ChatID, reply["id"]), map[string]any{"emoji": "👎"}, nil)
	s := waitSignal(t, e, "thumbs_down")
	check(s, true)
	if s.RunID != run {
		t.Fatalf("thumbs_down is on run %s, want %s", s.RunID, run)
	}
	var score struct {
		Score *int `json:"score"`
	}
	e.MustDo("GET", e.SP("/runs/%s", run), nil, &score)
	if score.Score == nil || *score.Score != 70 {
		t.Fatalf("the score after a signal is %v, want 70", score.Score)
	}

	// rerun: the same work again.
	var again struct {
		RunID string `json:"run_id"`
	}
	e.MustDo("POST", e.SP("/runs/%s/rerun", run), nil, &again)
	e.WaitRun(again.RunID, "done")
	check(waitSignal(t, e, "rerun"), true)

	// correction: the next message says the result is wrong; the router role says so.
	e.Fake.AddScript(fakemodel.Script{Name: "is-correction", Match: fakemodel.Match{System: "decide whether it corrects", User: "That is wrong"}, Steps: []fakemodel.Step{{JSON: map[string]any{"correction": true, "reason": "The user says the result is wrong."}}}})
	e.Fake.AddScript(fakemodel.Script{Name: "after-correction", Match: fakemodel.Match{User: "That is wrong"}, Steps: []fakemodel.Step{{Text: "I look again."}}})
	run2, _ := e.Post(chief.ChatID, "That is wrong: the desks are too wide.")
	e.WaitRun(run2, "done")
	check(waitSignal(t, e, "correction"), true)

	// approval_denied and draft_edited: two notes that ask first.
	note("[[deny-note]]", "Lunch at noon.")
	run3, _ := e.Post(chief.ChatID, "/"+skill+" [[deny-note]] Tell the team about lunch.")
	card := pendingCard(t, e, chief.ChatID, "approval")
	e.MustDo("POST", e.SP("/cards/%s/answer", card["id"]), map[string]any{"action": "deny"}, nil)
	e.WaitRun(run3, "done")
	check(waitSignal(t, e, "approval_denied"), true)

	note("[[edit-note]]", "Standup at nine.")
	run4, _ := e.Post(chief.ChatID, "/"+skill+" [[edit-note]] Tell the team about standup.")
	card = pendingCard(t, e, chief.ChatID, "approval")
	e.MustDo("POST", e.SP("/cards/%s/answer", card["id"]), map[string]any{"action": "allow", "value": `{"to":"team","body":"Standup at half past nine."}`}, nil)
	e.WaitRun(run4, "done")
	check(waitSignal(t, e, "draft_edited"), true)
	mu.Lock()
	sent := append([]string(nil), sentBodies...)
	mu.Unlock()
	if len(sent) != 1 || sent[0] != "Standup at half past nine." {
		t.Fatalf("the tool sent %q, want the edited draft", sent)
	}

	// escalation_accepted_anyway: the verifier fails twice, then the person accepts.
	e.Fake.AddScript(fakemodel.Script{Name: "weak", Match: fakemodel.Match{User: "[[weak]]"}, Steps: []fakemodel.Step{{Text: "A short answer."}}})
	e.Fake.AddScript(fakemodel.Script{Name: "weak-verify", Match: fakemodel.Match{System: "You verify", User: "[[weak]]"}, Steps: []fakemodel.Step{{JSON: map[string]any{"pass": false, "reason": "The answer has no sources."}}}})
	run5, _ := e.Post(chief.ChatID, "/"+skill+" [[weak]] Summarize the desk market with sources.")
	card = pendingCard(t, e, chief.ChatID, "verification")
	e.MustDo("POST", e.SP("/cards/%s/answer", card["id"]), map[string]any{"action": "accept"}, nil)
	e.WaitRun(run5, "done")
	check(waitSignal(t, e, "escalation_accepted_anyway"), true)

	// uncertain_confirmed_wrong: a send that was in flight when the server stopped.
	note("[[crash]]", "[[crash]] The office is closed on Friday.")
	run6, _ := e.Post(chief.ChatID, "/"+skill+" [[crash]] Tell the team about Friday.")
	card = pendingCard(t, e, chief.ChatID, "approval")
	e.MustDo("POST", e.SP("/cards/%s/answer", card["id"]), map[string]any{"action": "allow"}, nil)
	card = pendingCard(t, e, chief.ChatID, "uncertain")
	e.MustDo("POST", e.SP("/cards/%s/answer", card["id"]), map[string]any{"action": "not_done"}, nil)
	e.WaitRun(run6, "done", "failed", "escalated")
	check(waitSignal(t, e, "uncertain_confirmed_wrong"), true)

	// card_declined: Chief proposes a bot and the person declines.
	e.Fake.AddScript(fakemodel.Script{Name: "propose", Match: fakemodel.Match{User: "[[propose]]"}, Steps: []fakemodel.Step{
		{ToolCalls: []fakemodel.ScriptCall{{Name: "propose_bot", Args: map[string]any{"name": "Scout", "job": "Finds desks", "instructions": "Find desks."}}}}, {Text: "I proposed Scout."},
	}})
	run7, _ := e.Post(chief.ChatID, "/"+skill+" [[propose]] We need help with desks.")
	e.WaitRun(run7, "done")
	card = pendingCard(t, e, chief.ChatID, "proposal")
	e.MustDo("POST", e.SP("/cards/%s/answer", card["id"]), map[string]any{"action": "decline"}, nil)
	check(waitSignal(t, e, "card_declined"), true)

	// unrequested_takeover: a person takes the screen when the bot did not ask.
	e.MustDo("POST", e.SP("/bots/%s/screen/control", chief.ID), nil, nil)
	check(waitSignal(t, e, "unrequested_takeover"), true)

	types := map[string]bool{}
	for _, s := range signalsOf(e) {
		types[s.Type] = true
	}
	if len(types) != 9 {
		t.Fatalf("signal types %v, want the 9 of Appendix F", types)
	}

	var rep struct {
		Runs    int `json:"runs"`
		BySkill []struct {
			Key  string `json:"key"`
			Runs int    `json:"runs"`
		} `json:"by_skill"`
	}
	e.MustDo("GET", e.SP("/learning/scores"), nil, &rep)
	if rep.Runs == 0 || len(rep.BySkill) == 0 || rep.BySkill[0].Key != skill {
		t.Fatalf("score report %+v", rep)
	}
}
