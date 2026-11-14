package agent

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/chats"
	"github.com/alternayte/fettle/internal/runs"
)

// pushKind maps a card to a push type (§15.2).
var pushKind = map[string]string{"approval": "approval", "question": "question", "uncertain": "question", "takeover": "takeover", "budget": "budget", "turn_limit": "failed"}

// notifyCard tells the members about a card that waits for them. The link opens the card.
func (x *exec) notifyCard(ctx context.Context, card chats.Card, msg *chats.Message) {
	a := x.a
	kind, ok := pushKind[card.Kind]
	if a.Notify == nil || !ok || x.run.Kind != "owner" {
		return
	}
	url := "/runs/" + x.run.ID().String()
	if msg != nil {
		url = fmt.Sprintf("/chat/%s?card=%s", msg.ChatID, card.ID)
	}
	a.Notify(ctx, x.run.SpaceID, kind, x.bot.Name+": "+card.Title, truncate(card.Detail, 200), url)
}

// notifyFinished tells the members that a run ended. Failures always notify; finished work
// notifies when the space setting asks for it. A routine's own setting decides for its runs
// (§13.2).
func (x *exec) notifyFinished(ctx context.Context, status, summary string) {
	a := x.a
	if a.Notify == nil || x.run.Kind != "owner" || (status != runs.StatusDone && status != runs.StatusFailed) {
		return
	}
	failed := status == runs.StatusFailed
	send := failed
	if !failed {
		send = a.Settings.Bool(ctx, &x.run.SpaceID, "push.finished")
	}
	if x.env.Requester.Type == "routine" && a.RoutineNotify != nil {
		if id, err := uuid.Parse(x.env.Requester.ID); err == nil {
			switch a.RoutineNotify(ctx, id) {
			case "always":
				send = true
			case "never":
				send = false
			default: // on_failure
				send = failed
			}
		}
	}
	if !send {
		return
	}
	kind, title := "finished", x.bot.Name+" finished"
	if failed {
		kind, title = "failed", x.bot.Name+" could not finish"
	}
	url := "/runs/" + x.run.ID().String()
	if x.env.ChatID != nil {
		url = fmt.Sprintf("/chat/%s?run=%s", *x.env.ChatID, x.run.ID())
	}
	a.Notify(ctx, x.run.SpaceID, kind, title, truncate(summary, 200), url)
}
