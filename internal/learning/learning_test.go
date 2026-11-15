package learning_test

import (
	"context"
	"testing"
	"time"

	"github.com/alternayte/drel"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/bots"
	"github.com/alternayte/fettle/internal/envelopes"
	"github.com/alternayte/fettle/internal/learning"
	"github.com/alternayte/fettle/internal/runs"
	"github.com/alternayte/fettle/internal/spaces"
	"github.com/alternayte/fettle/internal/testenv"
)

func TestSCN_LRN_002_RunScores(t *testing.T) {
	// The formula of §14.2 for every combination of its four parts.
	for _, pass := range []bool{false, true} {
		for _, signals := range []int{0, 2} {
			for _, within := range []bool{false, true} {
				for _, esc := range []bool{false, true} {
					want := 0
					if pass {
						want += 50
					}
					if signals == 0 {
						want += 30
					}
					if within {
						want += 10
					}
					if !esc {
						want += 10
					}
					if got := learning.Value(pass, signals, within, esc); got != want {
						t.Fatalf("Value(%t, %d, %t, %t) = %d, want %d", pass, signals, within, esc, got, want)
					}
				}
			}
		}
	}

	testenv.Each(t, func(t *testing.T, dialect string) {
		db, _ := testenv.Open(t, dialect)
		e := db.Engine
		ctx := context.Background()
		sp := &spaces.Space{Name: "acme", TimeZone: "UTC"}
		bot := &bots.Bot{Name: "Research", Job: "Finds sources", Status: "active", CreatedByKind: "user"}
		if err := e.WithTx(ctx, func(ctx context.Context) error {
			tx := drel.MustFromContext(ctx)
			drel.Repo(tx, spaces.SpaceMeta).Add(sp)
			if err := tx.SaveChanges(ctx); err != nil {
				return err
			}
			bot.SpaceID = sp.ID()
			drel.Repo(tx, bots.BotMeta).Add(bot)
			return tx.SaveChanges(ctx)
		}); err != nil {
			t.Fatal(err)
		}
		envs := envelopes.New(e)
		rs := runs.NewService(e)
		j := runs.NewJournal(e, nil, nil)
		svc := learning.New(e, rs, j, envs)

		// run makes an ended owner run with a verifier result and an optional escalation.
		run := func(pass, budgetCard bool, cost float64) uuid.UUID {
			botID := bot.ID()
			env := &envelopes.Envelope{SpaceID: sp.ID(), Kind: "message", Origin: "user", Requester: envelopes.Requester{Type: "user", ID: "u1"}, OwnerBotID: &botID, Goal: "Compare desks", Budget: envelopes.Budget{USD: 1}, Decision: envelopes.Decision{Skill: "desk-research"}}
			if err := envs.Create(ctx, env, nil); err != nil {
				t.Fatal(err)
			}
			start := time.Now().Add(-time.Minute).UTC()
			r := &runs.Run{SpaceID: sp.ID(), EnvelopeID: env.ID(), BotID: botID, Kind: "owner", Status: runs.StatusRunning, StartedAt: &start}
			if err := e.WithTx(ctx, func(ctx context.Context) error {
				tx := drel.MustFromContext(ctx)
				drel.Repo(tx, runs.RunMeta).Add(r)
				return tx.SaveChanges(ctx)
			}); err != nil {
				t.Fatal(err)
			}
			evs := []runs.New{
				{Type: runs.EvRunStarted, Data: map[string]any{"step": "fanout", "goal": env.Goal}},
				{Type: runs.EvModelTurn, Data: map[string]any{"text": "", "model": "fake/large"}},
				{Type: runs.EvVerifyResult, Data: map[string]any{"attempt": 1, "pass": pass}},
			}
			if budgetCard {
				evs = append(evs, runs.New{Type: runs.EvCardPosted, Data: map[string]any{"card_id": "c1", "kind": "budget"}})
			}
			evs = append(evs, runs.New{Type: runs.EvRunFinished, Data: map[string]any{"status": runs.StatusDone}})
			done := runs.StatusDone
			if _, err := j.Append(ctx, r.ID(), runs.NoFence, &runs.Update{Status: &done, Finished: true, CostUSD: cost}, evs...); err != nil {
				t.Fatal(err)
			}
			return r.ID()
		}

		good := run(true, false, 0.2)
		sc, err := svc.ScoreRun(ctx, good)
		if err != nil {
			t.Fatal(err)
		}
		if sc.Value != 100 || sc.Step != "fanout" || sc.ModelName != "fake/large" || len(sc.Skills) != 1 || sc.Skills[0] != "desk-research" {
			t.Fatalf("score %+v", sc)
		}
		bad := run(false, true, 1.5)
		if sc, _ = svc.ScoreRun(ctx, bad); sc.Value != 30 || sc.WithinBudget || !sc.Escalated || sc.VerifierPass {
			t.Fatalf("a failed, escalated run over budget: %+v", sc)
		}

		// The helpers of a run count toward its cost and its budget.
		fan := run(true, false, 0.2)
		botID := bot.ID()
		single := "single"
		child := &envelopes.Envelope{SpaceID: sp.ID(), Kind: "part", Origin: "user", Requester: envelopes.Requester{Type: "bot", ID: botID.String()}, OwnerBotID: &botID, Goal: "Read the Beta Desk page", Budget: envelopes.Budget{USD: 1}, Step: &single}
		if err := envs.Create(ctx, child, nil); err != nil {
			t.Fatal(err)
		}
		helper := &runs.Run{SpaceID: sp.ID(), EnvelopeID: child.ID(), BotID: botID, ParentRunID: &fan, Kind: "subagent", Status: runs.StatusRunning}
		if err := e.WithTx(ctx, func(ctx context.Context) error {
			tx := drel.MustFromContext(ctx)
			drel.Repo(tx, runs.RunMeta).Add(helper)
			return tx.SaveChanges(ctx)
		}); err != nil {
			t.Fatal(err)
		}
		hdone := runs.StatusDone
		if _, err := j.Append(ctx, helper.ID(), runs.NoFence, &runs.Update{Status: &hdone, Finished: true, CostUSD: 0.9}); err != nil {
			t.Fatal(err)
		}
		if sc, _ = svc.ScoreRun(ctx, fan); sc.CostUSD < 1.09 || sc.CostUSD > 1.11 || sc.WithinBudget {
			t.Fatalf("a run whose helpers spent over its budget: %+v", sc)
		}

		// A later signal lowers the score of its run.
		if _, err := svc.Record(ctx, learning.Input{SpaceID: sp.ID(), RunID: &good, Type: learning.ThumbsDown, Ref: "message:m1"}); err != nil {
			t.Fatal(err)
		}
		if sc, _ = svc.ScoreOf(ctx, good); sc.Value != 70 || sc.Signals != 1 {
			t.Fatalf("after a signal: %+v", sc)
		}
		// The same action records once.
		if _, err := svc.Record(ctx, learning.Input{SpaceID: sp.ID(), RunID: &good, Type: learning.ThumbsDown, Ref: "message:m1"}); err != nil {
			t.Fatal(err)
		}
		if sc, _ = svc.ScoreOf(ctx, good); sc.Signals != 1 {
			t.Fatalf("a repeated action counted twice: %+v", sc)
		}
		if _, err := svc.Record(ctx, learning.Input{SpaceID: sp.ID(), Type: "praise"}); err == nil {
			t.Fatal("an unknown signal type was recorded")
		}
	})
}
