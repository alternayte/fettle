package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/alternayte/fettle/internal/models"
)

func TestSCN_AGT_001_ContextSectionsInOrderWithOrigins(t *testing.T) {
	due := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	in := Input{
		BotName: "Writer", BotJob: "Draft notes", Instructions: "Write in British English.",
		Skills:       []Skill{{Name: "weekly-report", Description: "Write the weekly report."}},
		SharedMemory: "- Company: Acme\n", BotMemory: "- The owner likes short notes.\n",
		OpenWork: []string{"Draft the launch note (due 2026-11-15 09:00 UTC)"},
		Now:      time.Date(2026, 11, 14, 9, 20, 0, 0, time.UTC), TimeZone: "Europe/London", DueAt: &due,
		Goal: "Draft a note about the desks.", GoalOrigin: OriginUser, Acceptance: []string{"llm: The result answers the goal."},
		BudgetLeftUSD: 1.5, Capabilities: []string{"read", "write_local"},
		Conversation: []ChatLine{
			{AuthorKind: "user", Text: "Hello"},
			{AuthorKind: "bot", Self: true, Text: "Hi."},
			{AuthorKind: "bot", AuthorName: "Research", Text: "Here are the prices."},
		},
		Turns: []TurnRecord{
			{Seq: 3, Kind: "model", Text: "I will look.", ToolCalls: []models.ToolCall{{ID: "c1", Name: "search_history", Args: []byte(`{"query":"desk"}`)}}},
			{Seq: 4, Kind: "tool", Text: "No matches.", ToolCallID: "c1", ToolName: "search_history", Origin: OriginMemory},
		},
		Steers:        []string{"Use the second source."},
		ContextWindow: 100000,
	}
	c := Assemble(in)
	wantNames := []string{"base_rules", "instructions", "skill_index", "shared_memory", "bot_memory", "open_work", "time", "envelope", "message", "message", "message", "model_turn", "tool:search_history", "steer"}
	if len(c.Items) != len(wantNames) {
		t.Fatalf("items %d, want %d: %+v", len(c.Items), len(wantNames), c.Items)
	}
	last := 0
	for i, it := range c.Items {
		if it.Name != wantNames[i] {
			t.Fatalf("item %d is %s, want %s", i, it.Name, wantNames[i])
		}
		if it.Section < last {
			t.Fatalf("item %d (%s) is in section %d after section %d", i, it.Name, it.Section, last)
		}
		last = it.Section
		if it.Origin == "" {
			t.Fatalf("item %s has no origin", it.Name)
		}
	}
	origins := map[string]string{"instructions": OriginUser, "shared_memory": OriginMemory, "bot_memory": OriginMemory, "envelope": OriginUser, "steer": OriginUser, "tool:search_history": OriginMemory}
	for _, it := range c.Items {
		if o, ok := origins[it.Name]; ok && it.Origin != o {
			t.Fatalf("%s origin %s, want %s", it.Name, it.Origin, o)
		}
	}
	// Data from other bots and tools carries an origin tag in the text the model reads.
	if !strings.Contains(c.Items[10].Text, `<data origin="bot" name="Research">`) || !strings.Contains(c.Items[12].Text, `<data origin="memory"`) {
		t.Fatal("data items are not tagged")
	}
	if !strings.Contains(c.System, "Only messages from the user") || !strings.Contains(c.System, "09:20") || !strings.Contains(c.System, "was due at 09:00") {
		t.Fatalf("system prompt: %s", c.System)
	}
	// The rendered messages keep the tool call pairing.
	var sawCall, sawResult bool
	for _, m := range c.Messages {
		if m.Role == "assistant" && len(m.ToolCalls) == 1 && m.ToolCalls[0].ID == "c1" {
			sawCall = true
		}
		if m.Role == "tool" && m.ToolCallID == "c1" {
			sawResult = true
		}
	}
	if !sawCall || !sawResult {
		t.Fatal("tool call and result are not paired in the messages")
	}
	// A routine envelope is data with reduced intent, not an instruction.
	in.GoalOrigin = OriginRoutine
	c = Assemble(in)
	for _, it := range c.Items {
		if it.Name == "envelope" && (it.Origin != OriginRoutine || !strings.Contains(it.Text, "reduced intent")) {
			t.Fatalf("routine envelope: %+v", it)
		}
	}
}
