package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestActivityToolLabels(t *testing.T) {
	long := strings.Repeat("x", 100)
	cases := []struct {
		tool string
		args map[string]any
		want string
	}{
		{"exec", map[string]any{"command": "npm test"}, "Running npm test"},
		{"exec", map[string]any{"command": "npm test\ncurl -H 'Authorization: x'"}, "Running npm test …"},
		{"exec", map[string]any{"command": long}, "Running " + strings.Repeat("x", 59) + "…"},
		{"web_fetch", map[string]any{"url": "https://user:pw@example.com/a/b?token=1"}, "Reading example.com"},
		{"browser_navigate", map[string]any{"url": "https://acme.example/login"}, "Opening acme.example"},
		{"read_file", map[string]any{"path": "notes/plan.md"}, "Reading notes/plan.md"},
		{"edit_file", map[string]any{"path": "a.go", "old_text": "secret", "new_text": "x"}, "Editing a.go"},
		{"web_search", map[string]any{"query": "private words"}, "Searching the web"},
		{"some_connector_tool", map[string]any{"body": "hello"}, "Using some_connector_tool"},
	}
	for _, c := range cases {
		args, _ := json.Marshal(c.args)
		if got := toolLabel(c.tool, args); got != c.want {
			t.Errorf("toolLabel(%s, %s) = %q, want %q", c.tool, args, got, c.want)
		}
	}
}

func TestActivityWaitLabels(t *testing.T) {
	if waitActivity("computer") == "" || waitActivity("subagents") == "" {
		t.Fatal("a wait for a computer or helpers has no label")
	}
	if waitActivity("card") != "" || waitActivity("timer") != "" {
		t.Fatal("a card or timer wait must clear the line")
	}
}
