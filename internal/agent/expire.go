package agent

import (
	"context"
	"time"

	"github.com/alternayte/drel"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/runs"
	"github.com/alternayte/fettle/internal/settings"
)

func init() {
	settings.Register(settings.Def{Key: "cards.expire_minutes", Scope: "space", Default: 30, Doc: "Minutes after which a card of work that no person started expires."})
}

// ExpireCards expires the approval cards of runs that no person started (routines,
// webhooks, open work) after their time, and escalates those runs (§11.2). Cards in chats
// that a person started wait.
func (a *Agent) ExpireCards(ctx context.Context) (int, error) {
	rows, err := a.E.Query(ctx, `SELECT ap.card_id, ap.run_id, ap.space_id, ap.created_at FROM approvals ap
		JOIN runs r ON r.id = ap.run_id JOIN envelopes e ON e.id = r.envelope_id
		WHERE ap.status = 'pending' AND e.origin <> 'user'`)
	if err != nil {
		return 0, err
	}
	type due struct {
		card       string
		run, space uuid.UUID
		created    time.Time
	}
	var all []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.card, &d.run, &d.space, &d.created); err != nil {
			rows.Close()
			return 0, err
		}
		all = append(all, d)
	}
	rows.Close()
	n := 0
	for _, d := range all {
		mins := a.Settings.Int(ctx, &d.space, "cards.expire_minutes")
		if mins <= 0 || a.now().Sub(d.created) < time.Duration(mins)*time.Minute {
			continue
		}
		// A run without a chat has no card message; its approval still expires.
		msg, _ := a.Chats.FindCard(ctx, d.space, d.card)
		kind := "approval"
		err = a.E.WithTx(ctx, func(ctx context.Context) error {
			tx := drel.MustFromContext(ctx)
			res, err := tx.Exec(ctx, "UPDATE approvals SET status = 'expired' WHERE card_id = $1 AND status = 'pending'", d.card)
			if err != nil || res == 0 {
				return err
			}
			if msg != nil {
				card := *msg.Card
				card.Status, card.Answer = "expired", "Expired"
				kind = card.Kind
				m, err := a.Chats.SetCard(ctx, msg.ID(), &card)
				if err != nil {
					return err
				}
				msg = m
			}
			if _, err := a.Journal.Append(ctx, d.run, runs.NoFence, &runs.Update{Status: strp(runs.StatusEscalated), WaitReason: strp("")},
				runs.New{Type: runs.EvCardAnswered, Data: evCard{CardID: d.card, Kind: kind, Action: "expired", By: "fettle"}}); err != nil {
				return err
			}
			r, err := a.Runs.Find(ctx, d.run)
			if err != nil {
				return err
			}
			return a.Envelopes.SetStatus(ctx, r.EnvelopeID, "escalated", nil)
		})
		if err != nil {
			return n, err
		}
		n++
		if msg != nil {
			a.Chats.Publish(msg.ChatID, "message", a.MessageView(ctx, msg))
		}
		a.publishRun(ctx, d.run)
	}
	return n, nil
}
