//go:build e2e

package flows

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
	"github.com/alternayte/fettle/internal/testenv"
)

// sseEvents reads named events from a stream until ctx ends.
func sseEvents(ctx context.Context, t *testing.T, url, token string) <-chan [2]string {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req) //nolint:bodyclose // the reader goroutine closes it
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan [2]string, 1024)
	go func() {
		defer resp.Body.Close()
		defer close(ch)
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		ev := "message" // SSE default event name
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				ev = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				ch <- [2]string{ev, strings.TrimPrefix(line, "data: ")}
				ev = "message"
			}
		}
	}()
	return ch
}

func TestSCN_CORE_001_BinaryServesUIOpenAPIAndStream(t *testing.T) {
	s := startServer(t, "sqlite")
	resp, err := http.Get(s.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), `<div id="root">`) {
		t.Fatalf("/ = %d", resp.StatusCode)
	}
	resp, _ = http.Get(s.URL + "/api/v1/openapi.json")
	served, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	gen, err := exec.Command(binary(t), "openapi").Output()
	if err != nil {
		t.Fatal(err)
	}
	var a, b struct {
		Paths map[string]map[string]any `json:"paths"`
	}
	_ = json.Unmarshal(served, &a)
	_ = json.Unmarshal(gen, &b)
	if len(a.Paths) == 0 || len(a.Paths) != len(b.Paths) {
		t.Fatalf("openapi paths served %d, registered %d", len(a.Paths), len(b.Paths))
	}
	for _, p := range []string{"/api/v1/spaces/{space}/chats/{chat}/messages", "/api/v1/spaces/{space}/chats/{chat}/stream", "/api/v1/spaces/{space}/runs/{run}"} {
		if _, ok := a.Paths[p]; !ok {
			t.Fatalf("openapi lacks %s", p)
		}
	}

	s.SetupModel("openai-chat", "chunkms=5", 128000)
	chief := s.Chief()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	events := sseEvents(ctx, t, s.URL+"/api/v1"+s.SP("/chats/%s/stream", chief.ChatID), s.Token)
	s.Post(chief.ChatID, "stream a reply to me")
	var deltas []string
	for ev := range events {
		switch ev[0] {
		case "delta":
			var d struct{ Text string }
			_ = json.Unmarshal([]byte(ev[1]), &d)
			deltas = append(deltas, d.Text)
		case "message":
			var m struct {
				AuthorKind string `json:"author_kind"`
				Body       string `json:"body"`
				Streaming  bool   `json:"streaming"`
			}
			_ = json.Unmarshal([]byte(ev[1]), &m)
			if m.AuthorKind == "bot" && !m.Streaming {
				if len(deltas) < 3 || strings.Join(deltas, "") != m.Body || m.Body != "Fake reply: stream a reply to me" {
					t.Fatalf("deltas %q, final %q", deltas, m.Body)
				}
				return
			}
		}
	}
	t.Fatal("the stream ended before the final message")
}

func TestSCN_BOT_001_InstructionsHistoryAndRestore(t *testing.T) {
	testenv.Each(t, func(t *testing.T, dialect string) {
		s := startServer(t, dialect)
		var w struct {
			ID string `json:"id"`
		}
		s.MustDo("POST", s.SP("/bots"), map[string]any{"name": "Writer", "job": "Draft notes", "instructions": "Version one."}, &w)
		for _, text := range []string{"Version two.", "Version three."} {
			s.MustDo("PATCH", s.SP("/bots/%s", w.ID), map[string]any{"name": "Writer", "job": "Draft notes", "instructions": text}, nil)
		}
		s.MustDo("POST", s.SP("/bots/%s/instructions/1/restore", w.ID), nil, nil)
		var vs struct {
			Versions []struct {
				Rev          int    `json:"rev"`
				Instructions string `json:"instructions"`
				Current      bool   `json:"current"`
			} `json:"versions"`
		}
		s.MustDo("GET", s.SP("/bots/%s/instructions", w.ID), nil, &vs)
		if len(vs.Versions) != 3 {
			t.Fatalf("versions %d", len(vs.Versions))
		}
		var b struct{ Instructions string }
		s.MustDo("GET", s.SP("/bots/%s", w.ID), nil, &b)
		if b.Instructions != "Version one." {
			t.Fatalf("current %q", b.Instructions)
		}
		for _, v := range vs.Versions {
			if v.Current != (v.Rev == 1) {
				t.Fatalf("current flag on rev %d", v.Rev)
			}
		}
	})
}

func TestSCN_BOT_006_MemoryLimitAndSharedMemory(t *testing.T) {
	s := startServer(t, "postgres")
	s.SetupModel("openai-chat", "", 128000)
	var calls []fakemodel.ScriptCall
	for i := 0; i < 40; i++ {
		calls = append(calls, fakemodel.ScriptCall{Name: "remember", Args: map[string]any{"fact": strings.Repeat("note ", 24) + string(rune('A'+i%26)) + string(rune('a'+i/26))}})
	}
	s.Fake.AddScript(fakemodel.Script{Name: "notes", Match: fakemodel.Match{User: "[[write-notes]]"}, Steps: []fakemodel.Step{{ToolCalls: calls}, {Text: "Done writing notes."}}})
	chief := s.Chief()
	run, _ := s.Post(chief.ChatID, "[[write-notes]] save everything")
	r := s.WaitRun(run, "done", "failed")
	if r.Line.State != "done" {
		t.Fatalf("run %s", r.EventTypes())
	}
	var mem struct {
		Content string `json:"content"`
		Bytes   int    `json:"bytes"`
	}
	s.MustDo("GET", s.SP("/bots/%s/memory", chief.ID), nil, &mem)
	if mem.Bytes > 4096 || len(mem.Content) > 4096 || mem.Bytes < 3500 {
		t.Fatalf("memory has %d bytes", mem.Bytes)
	}
	full := false
	for _, ev := range r.Events {
		if ev.Type == "tool_result" && strings.Contains(string(ev.Data), "memory is full") {
			full = true
		}
	}
	if !full {
		t.Fatal("no tool result told the bot that memory is full")
	}
	if c, _ := s.Do("PUT", s.SP("/bots/%s/memory", chief.ID), map[string]any{"content": strings.Repeat("x", 5000)}, nil); c != 422 {
		t.Fatalf("a 5 KB memory edit got %d", c)
	}

	// A shared memory edit shows in every bot's next context.
	var w struct {
		ChatID string `json:"chat_id"`
	}
	s.MustDo("POST", s.SP("/bots"), map[string]any{"name": "Writer", "job": "Draft notes"}, &w)
	s.MustDo("PUT", s.SP("/memory"), map[string]any{"content": "- The company is Acme Pottery.\n"}, nil)
	s.Fake.Reset()
	r1, _ := s.Post(chief.ChatID, "who are we")
	r2, _ := s.Post(w.ChatID, "who are we")
	s.WaitRun(r1, "done")
	s.WaitRun(r2, "done")
	seen := 0
	for _, req := range s.Fake.Requests() {
		if strings.Contains(req.System, "The company is Acme Pottery.") {
			seen++
		}
	}
	if seen < 2 {
		t.Fatalf("shared memory reached %d contexts", seen)
	}
}

func TestSCN_VLT_001_PasswordNeverInModelRequestsOrLogs(t *testing.T) {
	s := startServer(t, "sqlite")
	const password = "hunter2-correct-horse-battery"
	s.MustDo("POST", s.SP("/vault"), map[string]any{"name": "shop", "kind": "login", "site": "https://shop.test", "username": "owner@acme.test", "value": password}, nil)
	s.SetupModel("anthropic-messages", "", 128000)
	chief := s.Chief()
	run, _ := s.Post(chief.ChatID, "Remember that the shop login is ready")
	s.WaitRun(run, "done")
	for _, req := range s.Fake.Requests() {
		blob, _ := json.Marshal(req)
		if strings.Contains(string(blob), password) {
			t.Fatal("a model request holds the password")
		}
	}
	_, body := s.Do("GET", s.SP("/vault"), nil, nil)
	if strings.Contains(body, password) || !strings.Contains(body, `"last4":"tery"`) {
		t.Fatalf("vault list: %s", body)
	}
	s.stop()
	if strings.Contains(s.logs.String(), password) {
		t.Fatal("the server log holds the password")
	}
}

func TestSCN_CORE_010_AuditRowsAndNoAuditWrites(t *testing.T) {
	s := startServer(t, "sqlite")
	s.SetupModel("openai-chat", "", 128000)
	s.MustDo("PUT", s.SP("/settings/agent.max_turns"), map[string]any{"value": 1}, nil)
	var it struct{ ID string }
	s.MustDo("POST", s.SP("/vault"), map[string]any{"name": "token", "kind": "secret", "value": "s3cr3t-value"}, &it)
	var b struct{ ID, ChatID string }
	s.MustDo("POST", s.SP("/bots"), map[string]any{"name": "Writer"}, &b)
	b2 := struct {
		ID     string `json:"id"`
		ChatID string `json:"chat_id"`
	}{}
	s.MustDo("GET", s.SP("/bots/%s", b.ID), nil, &b2)
	run, _ := s.Post(b2.ChatID, "[[never-ends]] go")
	s.WaitRun(run, "escalated")
	var card string
	for _, m := range s.Messages(b2.ChatID) {
		if c, ok := m["card"].(map[string]any); ok {
			card, _ = c["id"].(string)
		}
	}
	s.MustDo("POST", s.SP("/cards/%s/answer", card), map[string]any{"action": "stop"}, nil)
	// Member changes need server mode; TestSCN_AUTH_001 checks their audit rows.
	// Zone, grant and provider changes are hard asks: they apply when the card is allowed.
	allow := func(path string, body any) {
		var r cardReply
		s.MustDo("POST", s.SP("%s", path), body, &r)
		s.MustDo("POST", s.SP("/cards/%s/answer", r.CardID), map[string]any{"action": "allow"}, nil)
	}
	allow("/zones", map[string]any{"name": "audit-zone", "size": "small", "network": "open"})
	allow("/zones/"+s.zoneIDOf("personal")+"/grants", map[string]any{"kind": "vault_item", "target_id": it.ID})
	allow("/computer-providers/host/enable", nil)
	s.MustDo("POST", s.SP("/connectors"), map[string]any{"name": "fakemail", "url": "http://127.0.0.1:9/mcp", "auth": "header", "header_name": "X-Api-Key"}, nil)
	s.MustDo("POST", s.SP("/routines"), map[string]any{"bot_id": b.ID, "name": "Daily", "prompt": "Check the news.", "trigger": "cron", "cron": "0 9 * * *", "tz": "UTC", "enabled": false}, nil)
	s.MustDo("POST", s.SP("/policy/rules"), map[string]any{"tool": "exec", "effect": "ask"}, nil)
	want := []string{"settings.change", "vault.create", "model_provider.create", "model_provider.test", "model_role.set", "bot.create", "card.decide",
		"zone.create", "grant.add", "provider.enable", "connector.create", "routine.create", "policy.rule_add"}
	for _, a := range want {
		var out struct {
			Entries []struct{ Action string } `json:"entries"`
		}
		s.MustDo("GET", s.SP("/audit?action=%s", a), nil, &out)
		if len(out.Entries) == 0 {
			t.Fatalf("no audit row for %s", a)
		}
		for _, e := range out.Entries {
			if e.Action != a {
				t.Fatalf("filter %s returned %s", a, e.Action)
			}
		}
	}
	resp, _ := http.Get(s.URL + "/api/v1/openapi.json")
	var doc struct {
		Paths map[string]map[string]any `json:"paths"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&doc)
	resp.Body.Close()
	for p, ops := range doc.Paths {
		if strings.Contains(p, "audit") {
			for m := range ops {
				if m != "get" {
					t.Fatalf("audit route %s has method %s", p, m)
				}
			}
		}
	}
}
