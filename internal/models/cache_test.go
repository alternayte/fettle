package models

import (
	"encoding/json"
	"strings"
	"testing"
)

func cacheRequest() Request {
	return Request{
		Model:      "anthropic/claude",
		System:     "You are a bot. Stable rules.",
		SystemTail: "Current time: Monday 1 January 2026 09:00 (UTC).",
		Messages:   []Message{{Role: "user", Parts: []Part{TextPart("hello")}}},
		Tools: []ToolDef{
			{Name: "a", Description: "A", Schema: json.RawMessage(`{"type":"object"}`)},
			{Name: "b", Description: "B", Schema: json.RawMessage(`{"type":"object"}`)},
		},
	}
}

func TestCacheAnthropicMarksSystemAndLastTool(t *testing.T) {
	b := anthropic{}.body(cacheRequest())
	sys, ok := b["system"].([]map[string]any)
	if !ok || len(sys) != 2 {
		t.Fatalf("system is %#v, want two blocks", b["system"])
	}
	if sys[0]["cache_control"] == nil || sys[1]["cache_control"] != nil {
		t.Fatalf("the cache point must be on the stable block only: %#v", sys)
	}
	if !strings.Contains(sys[1]["text"].(string), "Current time") {
		t.Fatalf("the time is not after the cache point: %#v", sys)
	}
	tools := b["tools"].([]map[string]any)
	if tools[0]["cache_control"] != nil || tools[1]["cache_control"] == nil {
		t.Fatalf("the cache point must be on the last tool: %#v", tools)
	}
}

func TestCacheOpenRouterOnlyForAnthropicAndGoogle(t *testing.T) {
	or := Conn{BaseURL: "https://openrouter.ai/api/v1"}
	cases := []struct {
		c     Conn
		model string
		want  bool
	}{
		{or, "anthropic/claude-sonnet", true},
		{or, "google/gemini-pro", true},
		{or, "openai/gpt-5", false},
		{Conn{BaseURL: "https://api.openai.com/v1"}, "anthropic/claude-sonnet", false},
		{Conn{BaseURL: "http://localhost:11434/v1"}, "google/gemma", false},
	}
	for _, c := range cases {
		if got := openRouterCaches(c.c, c.model); got != c.want {
			t.Errorf("openRouterCaches(%s, %s) = %v, want %v", c.c.BaseURL, c.model, got, c.want)
		}
	}
	req := cacheRequest()
	with, _ := json.Marshal(openAIChat{}.body(req, true))
	if !strings.Contains(string(with), `"cache_control"`) {
		t.Fatalf("the OpenRouter request has no cache_control: %s", with)
	}
	without, _ := json.Marshal(openAIChat{}.body(req, false))
	if strings.Contains(string(without), "cache_control") {
		t.Fatalf("the OpenAI request has cache_control: %s", without)
	}
	if !strings.Contains(string(without), "Stable rules.\\n\\nCurrent time") {
		t.Fatalf("the system prompt without a cache point lost its tail: %s", without)
	}
}
