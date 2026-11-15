package learning_test

import (
	"context"
	"testing"
	"time"

	"github.com/alternayte/drel"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/envelopes"
	"github.com/alternayte/fettle/internal/learning"
	"github.com/alternayte/fettle/internal/runs"
	"github.com/alternayte/fettle/internal/spaces"
	"github.com/alternayte/fettle/internal/testenv"
)

func TestSCN_LRN_005_RouterReportFiguresEqualTheRows(t *testing.T) {
	testenv.Each(t, func(t *testing.T, dialect string) {
		db, _ := testenv.Open(t, dialect)
		e := db.Engine
		ctx := context.Background()
		sp := &spaces.Space{Name: "acme", TimeZone: "UTC"}
		if err := e.WithTx(ctx, func(ctx context.Context) error {
			tx := drel.MustFromContext(ctx)
			drel.Repo(tx, spaces.SpaceMeta).Add(sp)
			return tx.SaveChanges(ctx)
		}); err != nil {
			t.Fatal(err)
		}
		svc := learning.New(e, runs.NewService(e), runs.NewJournal(e, nil, nil), envelopes.New(e))
		now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
		from := now.AddDate(0, 0, -7)

		// A week of runs: single runs score well; fanout runs score worse and cost more.
		type row struct {
			step  string
			pass  bool
			value int
			cost  float64
			dur   int64
			at    time.Time
		}
		var rows []row
		for i := 0; i < 6; i++ {
			rows = append(rows, row{"single", true, 100 - 10*(i%2), 0.02, 4000, now.Add(-time.Duration(i+1) * time.Hour)})
		}
		for i := 0; i < 5; i++ {
			rows = append(rows, row{"fanout", i < 2, 60, 0.30, 30000, now.Add(-time.Duration(i+1) * 24 * time.Hour)})
		}
		rows = append(rows, row{"direct", true, 100, 0.001, 900, now.Add(-2 * time.Hour)})
		rows = append(rows, row{"single", false, 30, 5, 1, now.AddDate(0, 0, -9)}) // before the week
		bot := uuid.New()
		if err := e.WithTx(ctx, func(ctx context.Context) error {
			tx := drel.MustFromContext(ctx)
			for _, r := range rows {
				drel.Repo(tx, learning.ScoreMeta).Add(&learning.Score{SpaceID: sp.ID(), RunID: uuid.New(), BotID: bot, Step: r.step, Skills: []string{}, VerifierPass: r.pass, WithinBudget: true, Value: r.value, CostUSD: r.cost, DurationMS: r.dur, FinishedAt: r.at})
			}
			return tx.SaveChanges(ctx)
		}); err != nil {
			t.Fatal(err)
		}

		rep, err := svc.Report(ctx, sp.ID(), from, now)
		if err != nil {
			t.Fatal(err)
		}
		// The expected figures, from the rows in the week.
		want := map[string]learning.StepRow{}
		for _, r := range rows {
			if r.at.Before(from) || !r.at.Before(now) {
				continue
			}
			w := want[r.step]
			w.Step = r.step
			w.Runs++
			if r.pass {
				w.PassRate++
			}
			w.AvgScore += float64(r.value)
			w.AvgCostUSD += r.cost
			w.AvgDurationMS += r.dur
			want[r.step] = w
		}
		if len(rep.Steps) != len(want) {
			t.Fatalf("steps %+v", rep.Steps)
		}
		for _, got := range rep.Steps {
			w := want[got.Step]
			n := float64(w.Runs)
			exp := learning.StepRow{Step: w.Step, Runs: w.Runs, PassRate: w.PassRate / n, AvgScore: w.AvgScore / n, AvgCostUSD: w.AvgCostUSD / n, AvgDurationMS: w.AvgDurationMS / int64(w.Runs)}
			if got.Runs != exp.Runs || !near(got.PassRate, exp.PassRate) || !near(got.AvgScore, exp.AvgScore) || !near(got.AvgCostUSD, exp.AvgCostUSD) || got.AvgDurationMS != exp.AvgDurationMS {
				t.Fatalf("step %s: got %+v, want %+v", got.Step, got, exp)
			}
		}
		if rep.Steps[0].Step != "direct" || rep.Steps[1].Step != "single" || rep.Steps[2].Step != "fanout" {
			t.Fatalf("the steps are not in ladder order: %+v", rep.Steps)
		}
		if len(rep.Suggestions) != 1 || rep.Suggestions[0].Setting != "agent.max_step" || rep.Suggestions[0].Value != "single" {
			t.Fatalf("suggestions %+v", rep.Suggestions)
		}
	})
}

func near(a, b float64) bool { d := a - b; return d < 1e-9 && d > -1e-9 }
