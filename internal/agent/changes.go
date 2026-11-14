package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/chats"
)

// Change applies one hard-ask change after a person allows its card (§11.2). Payload is the
// JSON that PostChange stored.
type Change func(ctx context.Context, actor audit.Actor, spaceID uuid.UUID, payload json.RawMessage) error

// ErrUnknownChange means no Change is registered for a card.
var ErrUnknownChange = errors.New("this change is not known")

// RegisterChange adds the applier of a change kind, such as zone.create.
func (a *Agent) RegisterChange(kind string, fn Change) {
	if a.changes == nil {
		a.changes = map[string]Change{}
	}
	a.changes[kind] = fn
}

// PostChange posts a hard-ask card for a change in the primary bot's direct chat. The change
// happens only when a person allows the card. No mode, rule or bot can skip it (SI-07), and
// the card never offers "Always allow".
func (a *Agent) PostChange(ctx context.Context, actor audit.Actor, spaceID uuid.UUID, kind, title, detail string, preview map[string]string, payload any) (*chats.Message, error) {
	if _, ok := a.changes[kind]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownChange, kind)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	primary, err := a.Bots.Primary(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	chat, err := a.Chats.DirectFor(ctx, spaceID, primary.ID())
	if err != nil {
		return nil, err
	}
	card := chats.Card{ID: uuid.NewString(), Kind: "change", Status: "pending", Title: title, Detail: detail, Preview: preview, HardAsk: true,
		Data: map[string]string{"change": kind, "payload": string(raw), "requested_by": actor.ID}}
	msg := &chats.Message{SpaceID: spaceID, ChatID: chat.ID(), AuthorKind: "system", AuthorID: "fettle", Card: &card}
	err = a.E.WithTx(ctx, func(ctx context.Context) error {
		if err := a.Chats.Insert(ctx, msg, nil); err != nil {
			return err
		}
		return a.Audit.Record(ctx, actor, &spaceID, "card.post", "card:"+card.ID, map[string]string{"kind": "change", "change": kind})
	})
	if err != nil {
		return nil, err
	}
	if a.Metrics != nil {
		a.Metrics.Card("change")
	}
	a.Chats.Publish(chat.ID(), "message", a.MessageView(ctx, msg))
	return msg, nil
}

// applyChange runs the change of an allowed card in the transaction in ctx.
func (a *Agent) applyChange(ctx context.Context, actor audit.Actor, spaceID uuid.UUID, card chats.Card) error {
	fn, ok := a.changes[card.Data["change"]]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownChange, card.Data["change"])
	}
	return fn(ctx, actor, spaceID, json.RawMessage(card.Data["payload"]))
}
