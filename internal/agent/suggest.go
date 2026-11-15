package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/alternayte/drel"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/chats"
	"github.com/alternayte/fettle/internal/envelopes"
	"github.com/alternayte/fettle/internal/models"
	"github.com/alternayte/fettle/internal/runs"
)

func (a *Agent) registerSuggest() {
	a.Tools.Add(Tool{
		Name:        "suggest_connectors",
		Description: "Suggest connectors for the apps the user works with. Each app gets a Connect card that opens Settings, Connectors. The run does not wait.",
		Class:       envelopes.ClassRead,
		NoSubagent:  true,
		Schema:      schema(`{"type":"object","properties":{"apps":{"type":"array","minItems":1,"maxItems":5,"items":{"type":"string","maxLength":40}}},"required":["apps"],"additionalProperties":false}`),
		Run: func(context.Context, *ToolEnv, json.RawMessage) (ToolOutput, error) {
			return ToolOutput{}, errors.New("suggest_connectors runs in the loop")
		},
	})
}

// suggestConnectors posts one Connect card per app that has no connector in the space yet
// (§15.4). The cards have no run: the user connects in Settings in their own time.
func (x *exec) suggestConnectors(ctx context.Context, call models.ToolCall) error {
	a := x.a
	var in struct {
		Apps []string `json:"apps"`
	}
	if err := json.Unmarshal(call.Args, &in); err != nil || len(in.Apps) == 0 || len(in.Apps) > 5 {
		_, err := x.append(ctx, nil, runs.New{Type: runs.EvToolCall, Data: evTool{CallID: call.ID, Tool: call.Name, Args: call.Args, Error: "Give 1 to 5 app names."}})
		return err
	}
	chatID := x.env.ChatID
	if chatID == nil {
		c, err := a.Chats.DirectFor(ctx, x.run.SpaceID, x.run.BotID)
		if err != nil {
			return err
		}
		id := c.ID()
		chatID = &id
	}
	var posted []*chats.Message
	err := a.E.WithTx(ctx, func(ctx context.Context) error {
		have := map[string]bool{}
		rows, err := drel.MustFromContext(ctx).Query(ctx, "SELECT name FROM connectors WHERE space_id = $1", x.run.SpaceID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err != nil {
				rows.Close()
				return err
			}
			have[strings.ToLower(n)] = true
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		seen := map[string]bool{}
		var cards, skipped []string
		for _, app := range in.Apps {
			app = strings.TrimSpace(app)
			key := strings.ToLower(app)
			if app == "" || seen[key] {
				continue
			}
			seen[key] = true
			if have[key] {
				skipped = append(skipped, app)
				continue
			}
			card := chats.Card{ID: uuid.NewString(), Kind: "connect", Status: "pending", Title: "Connect " + app,
				Detail: "Add a connector for " + app + " in Settings, Connectors. A bot uses it only after you give its zone a grant.",
				Data:   map[string]string{"app": app}}
			msg := &chats.Message{SpaceID: x.run.SpaceID, ChatID: *chatID, AuthorKind: "bot", AuthorID: x.run.BotID.String(), Card: &card, EnvelopeID: ptr(x.env.ID())}
			if err := a.Chats.Insert(ctx, msg, nil); err != nil {
				return err
			}
			if err := a.Audit.Record(ctx, audit.Actor{Kind: "bot", ID: x.run.BotID.String()}, &x.run.SpaceID, "card.post", "card:"+card.ID, map[string]string{"kind": "connect"}); err != nil {
				return err
			}
			posted = append(posted, msg)
			cards = append(cards, app)
		}
		result := "No Connect cards: every app is connected."
		if len(cards) > 0 {
			result = "Connect cards are in the chat for: " + strings.Join(cards, ", ") + "."
		}
		if len(skipped) > 0 {
			result += " Connected already: " + strings.Join(skipped, ", ") + "."
		}
		_, err = x.append(ctx, nil, runs.New{Type: runs.EvToolCall, Data: evTool{CallID: call.ID, Tool: call.Name, Class: envelopes.ClassRead, Args: call.Args, Result: result, Origin: OriginTool}})
		return err
	})
	if err != nil {
		return err
	}
	for _, m := range posted {
		a.Chats.Publish(*chatID, "message", a.MessageView(ctx, m))
	}
	return nil
}
