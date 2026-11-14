package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/envelopes"
)

func (a *Agent) registerMemoryTools() {
	a.Tools.Add(Tool{
		Name:        "remember",
		Description: "Save one fact in your MEMORY.md. Keep one fact per line. To change a fact, give the old fact in replaces.",
		Class:       envelopes.ClassWriteLocal,
		Schema:      schema(`{"type":"object","properties":{"fact":{"type":"string","description":"The fact, in one line."},"replaces":{"type":"string","description":"Text of the old fact that this fact replaces."}},"required":["fact"],"additionalProperties":false}`),
		Run: func(ctx context.Context, env *ToolEnv, args json.RawMessage) (ToolOutput, error) {
			in, err := decode[struct{ Fact, Replaces string }](args)
			if err != nil {
				return ToolOutput{}, err
			}
			id := env.BotID
			m, err := a.Bots.Remember(ctx, audit.Actor{Kind: "bot", ID: id.String()}, env.SpaceID, &id, in.Fact, in.Replaces, env.Origin)
			if err != nil {
				return ToolOutput{}, err
			}
			return ToolOutput{Text: fmt.Sprintf("Saved. MEMORY.md now has %d bytes of 4096.", len(m.Content))}, nil
		},
	})
	a.Tools.Add(Tool{
		Name:        "forget",
		Description: "Remove one fact from your MEMORY.md. Give enough text to match one fact.",
		Class:       envelopes.ClassWriteLocal,
		Schema:      schema(`{"type":"object","properties":{"fact":{"type":"string"}},"required":["fact"],"additionalProperties":false}`),
		Run: func(ctx context.Context, env *ToolEnv, args json.RawMessage) (ToolOutput, error) {
			in, err := decode[struct{ Fact string }](args)
			if err != nil {
				return ToolOutput{}, err
			}
			id := env.BotID
			m, err := a.Bots.Forget(ctx, audit.Actor{Kind: "bot", ID: id.String()}, env.SpaceID, &id, in.Fact, env.Origin)
			if err != nil {
				return ToolOutput{}, err
			}
			return ToolOutput{Text: fmt.Sprintf("Removed. MEMORY.md now has %d bytes.", len(m.Content))}, nil
		},
	})
	a.Tools.Add(Tool{
		Name:        "search_history",
		Description: "Search your older chat messages and run events. Returns matches newest first and a cursor for the next page.",
		Class:       envelopes.ClassRead,
		Schema:      schema(`{"type":"object","properties":{"query":{"type":"string"},"cursor":{"type":"integer","description":"The next_cursor of the previous page."},"limit":{"type":"integer"}},"required":["query"],"additionalProperties":false}`),
		Run: func(ctx context.Context, env *ToolEnv, args json.RawMessage) (ToolOutput, error) {
			in, err := decode[struct {
				Query  string
				Cursor int64
				Limit  int
			}](args)
			if err != nil {
				return ToolOutput{}, err
			}
			hits, next, err := a.Chats.Search(ctx, env.BotID, in.Query, in.Cursor, in.Limit)
			if err != nil {
				return ToolOutput{}, err
			}
			var b strings.Builder
			if len(hits) == 0 {
				b.WriteString("No matches.")
			}
			for _, h := range hits {
				fmt.Fprintf(&b, "[%s %s] %s\n", h.At.Format("2006-01-02 15:04"), h.Kind, h.Text)
			}
			if next > 0 {
				fmt.Fprintf(&b, "next_cursor: %d", next)
			}
			return ToolOutput{Text: b.String(), Origin: OriginMemory}, nil
		},
	})
}
