package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/apptest"
	"github.com/alternayte/fettle/internal/bots"
	"github.com/alternayte/fettle/internal/testenv"
)

func TestSCN_BOT_008_OnlyOpenDueWorkWakesTheBot(t *testing.T) {
	testenv.Each(t, func(t *testing.T, dialect string) {
		e := apptest.Start(t, dialect)
		e.SetupModel("openai-chat", "", 128000)
		ctx := context.Background()
		bot := uuid.MustParse(e.Chief().ID)
		due := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
		open, err := e.App.Bots.AddOpenWork(ctx, bot, "Send the weekly numbers", &due)
		if err != nil {
			t.Fatal(err)
		}
		done, err := e.App.Bots.AddOpenWork(ctx, bot, "Book the meeting room", &due)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.App.Bots.CompleteOpenWork(ctx, bot, bots.ShortID(done.ID()), due.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		e.App.Agent.SetClock(func() time.Time { return due.Add(-time.Minute) })
		if err := e.App.WakeOpenWork(ctx); err != nil {
			t.Fatal(err)
		}
		count := func() (n int, key string) {
			rows, err := e.App.DB.Engine.Query(ctx, "SELECT idempotency_key FROM envelopes WHERE idempotency_key LIKE 'open_work:%'")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for rows.Next() {
				n++
				_ = rows.Scan(&key)
			}
			return n, key
		}
		if n, _ := count(); n != 0 {
			t.Fatalf("%d wakes before the due time", n)
		}
		e.App.Agent.SetClock(func() time.Time { return due.Add(time.Minute) })
		_ = e.App.WakeOpenWork(ctx)
		_ = e.App.WakeOpenWork(ctx)
		n, key := count()
		if n != 1 || key != "open_work:"+open.ID().String() {
			t.Fatalf("%d wakes (%s), want one for the open item", n, key)
		}
	})
}
