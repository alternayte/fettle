package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/alternayte/fettle/internal/chats"
	"github.com/alternayte/fettle/internal/envelopes"
)

func (a *Agent) registerTeamTools() {
	a.Tools.Add(Tool{
		Name:        "list_bots",
		Description: "List the bots of the space with their jobs and zones, to choose a bot for a handoff.",
		Class:       envelopes.ClassRead,
		Schema:      schema(`{"type":"object","properties":{},"additionalProperties":false}`),
		Run: func(ctx context.Context, env *ToolEnv, _ json.RawMessage) (ToolOutput, error) {
			list, err := a.Bots.List(ctx, env.SpaceID, false)
			if err != nil {
				return ToolOutput{}, err
			}
			var b strings.Builder
			for _, o := range list {
				zone := "none"
				if o.ZoneID != nil {
					_ = a.E.QueryRow(ctx, "SELECT name FROM zones WHERE id = $1", *o.ZoneID).Scan(&zone)
				}
				role := ""
				if o.IsPrimary {
					role = " (primary)"
				}
				if o.ID() == env.BotID {
					role += " (you)"
				}
				fmt.Fprintf(&b, "- %s%s, zone %s: %s\n", o.Name, role, zone, o.PublicDesc)
			}
			return ToolOutput{Text: b.String(), Origin: OriginBot}, nil
		},
	})
	a.Tools.Add(Tool{
		Name:        "reply",
		Description: "Send a short message to the user's chat now, for example progress on long work. Your final answer still ends the work.",
		Class:       envelopes.ClassRead,
		NoSubagent:  true,
		Schema:      schema(`{"type":"object","properties":{"text":{"type":"string","maxLength":8000}},"required":["text"],"additionalProperties":false}`),
		Run: func(ctx context.Context, env *ToolEnv, args json.RawMessage) (ToolOutput, error) {
			in, err := decode[struct{ Text string }](args)
			if err != nil {
				return ToolOutput{}, err
			}
			if strings.TrimSpace(in.Text) == "" {
				return ToolOutput{}, errors.New("the text is empty")
			}
			if env.ChatID == nil {
				return ToolOutput{}, errors.New("this work has no chat; give the text in your final answer")
			}
			msg := &chats.Message{SpaceID: env.SpaceID, ChatID: *env.ChatID, AuthorKind: "bot", AuthorID: env.BotID.String(), Body: a.Vault.Redactor.String(ctx, env.SpaceID, in.Text), EnvelopeID: &env.EnvelopeID}
			botID := env.BotID
			if err := a.Chats.Insert(ctx, msg, &botID); err != nil {
				return ToolOutput{}, err
			}
			a.Chats.Publish(*env.ChatID, "message", a.MessageView(ctx, msg))
			return ToolOutput{Text: "Sent."}, nil
		},
	})
}
