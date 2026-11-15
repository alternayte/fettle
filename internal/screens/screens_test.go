package screens_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/screens"
	"github.com/alternayte/fettle/internal/testenv"
)

// The lease rules run as one upsert, so each dialect must give the same answers.
func TestLeasesAndTokens(t *testing.T) {
	testenv.Each(t, func(t *testing.T, dialect string) {
		db, _ := testenv.Open(t, dialect)
		ctx := context.Background()
		s := screens.New(db.Engine, func(context.Context) ([]byte, error) { return screens.NewKey() })
		clock := time.Now().UTC()
		s.SetClock(func() time.Time { return clock })
		space, bot, ada, bea := uuid.New(), uuid.New(), uuid.New(), uuid.New()

		view, _, err := s.View(ctx, space, bot, ada)
		if err != nil {
			t.Fatal(err)
		}
		if c, err := s.Check(ctx, view); err != nil || c.Control || c.Bot != bot {
			t.Fatalf("view token: %+v %v", c, err)
		}
		if _, err := s.Check(ctx, view[:len(view)-2]+"xx"); !errors.Is(err, screens.ErrToken) {
			t.Fatalf("a changed token passed: %v", err)
		}

		release, err := s.BotUse(ctx, space, bot)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.Control(ctx, space, bot, ada); !errors.Is(err, screens.ErrBotBusy) {
			t.Fatalf("control during a bot action: %v", err)
		}
		release()

		ctl, _, err := s.Control(ctx, space, bot, ada)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Check(ctx, ctl); err != nil {
			t.Fatalf("control token: %v", err)
		}
		if _, _, err := s.Control(ctx, space, bot, bea); !errors.Is(err, screens.ErrHeld) {
			t.Fatalf("a second person got control: %v", err)
		}
		if _, err := s.BotUse(ctx, space, bot); !errors.Is(err, screens.ErrControlled) {
			t.Fatalf("the bot acted during control: %v", err)
		}
		if _, _, err := s.Control(ctx, space, bot, ada); err != nil {
			t.Fatalf("renew: %v", err)
		}

		clock = clock.Add(screens.ViewTTL)
		if _, err := s.Check(ctx, view); !errors.Is(err, screens.ErrToken) {
			t.Fatalf("the view token is valid after 10 minutes: %v", err)
		}

		if ok, err := s.Release(ctx, bot, ada); !ok || err != nil {
			t.Fatalf("release: %v %v", ok, err)
		}
		if _, err := s.Check(ctx, ctl); !errors.Is(err, screens.ErrToken) {
			t.Fatalf("the control token is valid after Done: %v", err)
		}
		if _, err := s.BotUse(ctx, space, bot); err != nil {
			t.Fatalf("the bot cannot act after Done: %v", err)
		}

		// A lease that a person forgot expires.
		clock = clock.Add(screens.BotTTL + time.Second)
		if _, _, err := s.Control(ctx, space, bot, bea); err != nil {
			t.Fatalf("control after the bot lease expired: %v", err)
		}
		clock = clock.Add(screens.ControlTTL + time.Second)
		if _, _, err := s.Control(ctx, space, bot, ada); err != nil {
			t.Fatalf("control after the lease expired: %v", err)
		}
	})
}
