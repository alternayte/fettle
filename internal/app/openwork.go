package app

import (
	"context"
	"fmt"

	"github.com/alternayte/fettle/internal/agent"
	"github.com/alternayte/fettle/internal/bots"
	"github.com/alternayte/fettle/internal/envelopes"
)

// WakeOpenWork wakes each bot whose open work item is due and not done (§6.3). An item
// wakes its bot once; a done item causes no message.
func (a *App) WakeOpenWork(ctx context.Context) error {
	items, err := a.Bots.ClaimDue(ctx, a.Agent.Now())
	if err != nil {
		return err
	}
	for _, it := range items {
		b, err := a.Bots.Find(ctx, it.BotID)
		if err != nil {
			return err
		}
		chat, err := a.Chats.DirectFor(ctx, b.SpaceID, b.ID())
		if err != nil {
			return err
		}
		bot, chatID, due := b.ID(), chat.ID(), *it.DueAt
		env := &envelopes.Envelope{SpaceID: b.SpaceID, Kind: "routine", Origin: "bot", Requester: envelopes.Requester{Type: "bot", ID: bot.String()},
			OwnerBotID: &bot, ChatID: &chatID, Capabilities: agent.UserCapabilities, Deadline: &due,
			Goal:           fmt.Sprintf("An item of your open work is due: %s [%s]. Do it, or tell the user why you cannot. Then mark it done.", it.Text, bots.ShortID(it.ID())),
			IdempotencyKey: "open_work:" + it.ID().String()}
		if _, err := a.Agent.Submit(ctx, env); err != nil {
			return err
		}
	}
	return nil
}
