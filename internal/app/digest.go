package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/chats"
	"github.com/alternayte/fettle/internal/settings"
)

func init() {
	settings.Register(settings.Def{Key: "digest.enabled", Scope: "space", Default: false, Doc: "The primary bot sends a daily digest of finished work, waiting cards and failures."})
	settings.Register(settings.Def{Key: "digest.time", Scope: "space", Default: "08:00", Doc: "The local time of the daily digest (HH:MM, space time zone)."})
	settings.Register(settings.Def{Key: "digest.last_at", Scope: "space", Default: "", Doc: "When the last digest was sent (set by Fettle).", Internal: true})
}

// SendDigests sends the daily digest of each space whose digest is due at now (§9.8).
func (a *App) SendDigests(ctx context.Context, now time.Time) {
	rows, err := a.DB.Engine.Query(ctx, "SELECT id FROM spaces")
	if err != nil {
		return
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, sp := range ids {
		if err := a.digest(ctx, sp, now); err != nil {
			a.Log.Warn("digest", "space", sp, "error", err)
		}
	}
}

func (a *App) digest(ctx context.Context, spaceID uuid.UUID, now time.Time) error {
	if !a.Settings.Bool(ctx, &spaceID, "digest.enabled") {
		return nil
	}
	sp, err := a.Spaces.Get(ctx, spaceID)
	if err != nil {
		return err
	}
	loc, err := time.LoadLocation(sp.TimeZone)
	if err != nil {
		loc = time.UTC
	}
	at, err := time.ParseInLocation("15:04", a.Settings.String(ctx, &spaceID, "digest.time"), loc)
	if err != nil {
		return nil
	}
	local := now.In(loc)
	due := time.Date(local.Year(), local.Month(), local.Day(), at.Hour(), at.Minute(), 0, 0, loc)
	if local.Before(due) {
		return nil
	}
	since := due.Add(-24 * time.Hour)
	if last, err := time.Parse(time.RFC3339, a.Settings.String(ctx, &spaceID, "digest.last_at")); err == nil {
		if !last.Before(due) {
			return nil // today's digest went out
		}
		if last.After(since) {
			since = last
		}
	}
	text, err := a.digestText(ctx, spaceID, since.UTC())
	if err != nil {
		return err
	}
	primary, err := a.Bots.Primary(ctx, spaceID)
	if err != nil {
		return err
	}
	chat, err := a.Chats.DirectFor(ctx, spaceID, primary.ID())
	if err != nil {
		return err
	}
	msg := &chats.Message{SpaceID: spaceID, ChatID: chat.ID(), AuthorKind: "bot", AuthorID: primary.ID().String(), Body: text}
	err = a.DB.Engine.WithTx(ctx, func(ctx context.Context) error {
		botID := primary.ID()
		if err := a.Chats.Insert(ctx, msg, &botID); err != nil {
			return err
		}
		_, err := a.Settings.Set(ctx, audit.System, &spaceID, "digest.last_at", now.UTC().Format(time.RFC3339))
		return err
	})
	if err != nil {
		return err
	}
	a.Chats.Publish(chat.ID(), "message", a.Agent.MessageView(ctx, msg))
	return nil
}

// digestText lists finished work, waiting cards and failures since a time.
func (a *App) digestText(ctx context.Context, spaceID uuid.UUID, since time.Time) (string, error) {
	type item struct{ bot, goal, status string }
	rows, err := a.DB.Engine.Query(ctx, `SELECT b.name, e.goal, r.status FROM runs r
		JOIN envelopes e ON e.id = r.envelope_id JOIN bots b ON b.id = r.bot_id
		WHERE r.space_id = $1 AND r.kind = 'owner' AND r.finished_at >= $2 AND r.status IN ('done', 'failed')
		ORDER BY r.finished_at`, spaceID, since)
	if err != nil {
		return "", err
	}
	var done, failed []item
	for rows.Next() {
		var it item
		if rows.Scan(&it.bot, &it.goal, &it.status) != nil {
			continue
		}
		if it.status == "failed" {
			failed = append(failed, it)
		} else {
			done = append(done, it)
		}
	}
	rows.Close()
	cards, err := a.Chats.PendingCards(ctx, spaceID)
	if err != nil {
		return "", err
	}
	line := func(s string) string {
		s = strings.Join(strings.Fields(s), " ")
		if r := []rune(s); len(r) > 100 {
			s = string(r[:100]) + "…"
		}
		return s
	}
	var b strings.Builder
	b.WriteString("**Daily digest**\n\n")
	fmt.Fprintf(&b, "Finished work (%d):\n", len(done))
	for _, it := range done {
		fmt.Fprintf(&b, "- %s: %s\n", it.bot, line(it.goal))
	}
	if len(done) == 0 {
		b.WriteString("- none\n")
	}
	fmt.Fprintf(&b, "\nCards that wait for you (%d):\n", len(cards))
	for _, m := range cards {
		fmt.Fprintf(&b, "- %s\n", line(m.Card.Title))
	}
	if len(cards) == 0 {
		b.WriteString("- none\n")
	}
	fmt.Fprintf(&b, "\nFailures (%d):\n", len(failed))
	for _, it := range failed {
		fmt.Fprintf(&b, "- %s: %s\n", it.bot, line(it.goal))
	}
	if len(failed) == 0 {
		b.WriteString("- none\n")
	}
	return b.String(), nil
}
