package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/bots"
	"github.com/alternayte/fettle/internal/envelopes"
)

func (a *Agent) registerMemoryTools() {
	a.Tools.Add(Tool{
		Name:        "sleep_until",
		Description: "Pause this work until a time, then continue. Use an RFC 3339 time with a zone, for example 2026-11-15T09:00:00+01:00.",
		Class:       envelopes.ClassWriteLocal,
		Schema:      schema(`{"type":"object","properties":{"time":{"type":"string"},"reason":{"type":"string"}},"required":["time"],"additionalProperties":false}`),
		// The run loop handles sleep_until, because it parks the run.
		Run: func(context.Context, *ToolEnv, json.RawMessage) (ToolOutput, error) {
			return ToolOutput{}, fmt.Errorf("sleep_until runs in the loop")
		},
	})
	a.Tools.Add(Tool{
		Name:        "remember",
		Description: "Save one fact in your MEMORY.md. Keep one fact per line. To change a fact, give the old fact in replaces.",
		Class:       envelopes.ClassWriteLocal,
		MemoryWrite: true,
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
		MemoryWrite: true,
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
	a.Tools.Add(Tool{
		Name:        "add_open_work",
		Description: "Add an item to your open work list: something you started and must finish. Give due as an RFC 3339 time to be woken then.",
		Class:       envelopes.ClassWriteLocal,
		Schema:      schema(`{"type":"object","properties":{"text":{"type":"string"},"due":{"type":"string"}},"required":["text"],"additionalProperties":false}`),
		Run: func(ctx context.Context, env *ToolEnv, args json.RawMessage) (ToolOutput, error) {
			in, err := decode[struct{ Text, Due string }](args)
			if err != nil {
				return ToolOutput{}, err
			}
			var due *time.Time
			if strings.TrimSpace(in.Due) != "" {
				t, err := time.Parse(time.RFC3339, strings.TrimSpace(in.Due))
				if err != nil {
					return ToolOutput{}, fmt.Errorf("due must be RFC 3339 with a zone, for example 2026-11-15T09:00:00+01:00")
				}
				due = &t
			}
			it, err := a.Bots.AddOpenWork(ctx, env.BotID, in.Text, due)
			if err != nil {
				return ToolOutput{}, err
			}
			return ToolOutput{Text: "Added open work [" + bots.ShortID(it.ID()) + "]."}, nil
		},
	})
	a.Tools.Add(Tool{
		Name:        "complete_open_work",
		Description: "Mark an item of your open work as done, by the ID in square brackets. A done item does not wake you.",
		Class:       envelopes.ClassWriteLocal,
		Schema:      schema(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`),
		Run: func(ctx context.Context, env *ToolEnv, args json.RawMessage) (ToolOutput, error) {
			in, err := decode[struct{ ID string }](args)
			if err != nil {
				return ToolOutput{}, err
			}
			it, err := a.Bots.CompleteOpenWork(ctx, env.BotID, in.ID, a.now())
			if err != nil {
				return ToolOutput{}, err
			}
			return ToolOutput{Text: "Done: " + it.Text}, nil
		},
	})
	a.Tools.Add(Tool{
		Name:        "load_skill",
		Description: "Load the full instructions of one of your skills by its name from the skill index. Its files go into .skills/<name>/ in your home.",
		Class:       envelopes.ClassRead,
		Schema:      schema(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"],"additionalProperties":false}`),
		Run: func(ctx context.Context, env *ToolEnv, args json.RawMessage) (ToolOutput, error) {
			in, err := decode[struct{ Name string }](args)
			if err != nil {
				return ToolOutput{}, err
			}
			if a.LoadSkill == nil {
				return ToolOutput{}, fmt.Errorf("skills are not available")
			}
			text, err := a.LoadSkill(ctx, env, in.Name)
			return ToolOutput{Text: text}, err
		},
	})
}
