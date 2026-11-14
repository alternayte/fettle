package app_test

import (
	"strings"
	"testing"

	"github.com/alternayte/fettle/internal/apptest"
	"github.com/alternayte/fettle/internal/testenv"
)

func TestChatPathRepliesAndRemembers(t *testing.T) {
	testenv.Each(t, func(t *testing.T, dialect string) {
		e := apptest.Start(t, dialect)
		e.SetupModel("openai-chat", "", 128000)
		chief := e.Chief()
		run, _ := e.Post(chief.ChatID, "hello there")
		r := e.WaitRun(run, "done", "failed")
		if r.Line.State != "done" {
			t.Fatalf("run %s: %s", r.Line.State, r.EventTypes())
		}
		msgs := e.Messages(chief.ChatID)
		last := msgs[len(msgs)-1]
		if last["author_kind"] != "bot" || last["body"] != "Fake reply: hello there" {
			t.Fatalf("last message %v", last)
		}
		run, _ = e.Post(chief.ChatID, "Remember that my dog is called Rex")
		r = e.WaitRun(run, "done", "failed")
		if !strings.Contains(r.EventTypes(), "tool_intent,tool_result") {
			reqs := e.Fake.Requests()
			last := reqs[len(reqs)-1]
			t.Fatalf("events %s; fake saw last user %q tools %v; turn %s", r.EventTypes(), last.LastUser, last.Tools, r.Events[1].Data)
		}
		var mem struct{ Content string }
		e.MustDo("GET", e.SP("/bots/%s/memory", chief.ID), nil, &mem)
		if !strings.Contains(mem.Content, "my dog is called Rex") {
			t.Fatalf("memory %q", mem.Content)
		}
	})
}
