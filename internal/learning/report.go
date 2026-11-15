package learning

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Ladder is the order of the steps (§9.2).
var Ladder = []string{"direct", "single", "fanout", "forked", "handoff", "hire"}

// StepRow is the figures of one step in a period (§14.4).
type StepRow struct {
	Step          string  `json:"step"`
	Runs          int     `json:"runs"`
	PassRate      float64 `json:"pass_rate"`
	AvgScore      float64 `json:"avg_score"`
	AvgCostUSD    float64 `json:"avg_cost_usd"`
	AvgDurationMS int64   `json:"avg_duration_ms"`
}

// Suggestion is a change to a step setting that a person may apply.
type Suggestion struct {
	Setting string `json:"setting"`
	Value   string `json:"value"`
	Reason  string `json:"reason"`
}

// RouterReport is the router report of one period.
type RouterReport struct {
	From        time.Time    `json:"from"`
	To          time.Time    `json:"to"`
	Steps       []StepRow    `json:"steps"`
	Suggestions []Suggestion `json:"suggestions"`
}

// minRuns is how many runs a step needs before the report compares it.
const minRuns = 5

// Report builds the router report from the run scores that finished in [from, to).
func (s *Service) Report(ctx context.Context, spaceID uuid.UUID, from, to time.Time) (RouterReport, error) {
	rep := RouterReport{From: from.UTC(), To: to.UTC(), Steps: []StepRow{}, Suggestions: []Suggestion{}}
	list, err := s.Scores(ctx, spaceID, from)
	if err != nil {
		return rep, err
	}
	type acc struct {
		runs, pass, score int
		cost              float64
		dur               int64
	}
	by := map[string]*acc{}
	for _, sc := range list {
		if !sc.FinishedAt.Before(to.UTC()) {
			continue
		}
		a := by[sc.Step]
		if a == nil {
			a = &acc{}
			by[sc.Step] = a
		}
		a.runs++
		if sc.VerifierPass {
			a.pass++
		}
		a.score += sc.Value
		a.cost += sc.CostUSD
		a.dur += sc.DurationMS
	}
	rows := map[string]StepRow{}
	for _, step := range Ladder {
		a := by[step]
		if a == nil {
			continue
		}
		r := StepRow{Step: step, Runs: a.runs, PassRate: float64(a.pass) / float64(a.runs), AvgScore: float64(a.score) / float64(a.runs), AvgCostUSD: a.cost / float64(a.runs), AvgDurationMS: a.dur / int64(a.runs)}
		rows[step] = r
		rep.Steps = append(rep.Steps, r)
	}
	// Suggestions: a step above single that scores clearly worse than single, or costs
	// much more without a better score, is a reason to cap the ladder below it.
	base, ok := rows["single"]
	if !ok || base.Runs < minRuns {
		return rep, nil
	}
	for i, step := range Ladder {
		r, ok := rows[step]
		if i <= 1 || !ok || r.Runs < minRuns {
			continue
		}
		below := Ladder[i-1]
		switch {
		case r.AvgScore < base.AvgScore-10:
			rep.Suggestions = append(rep.Suggestions, Suggestion{Setting: "agent.max_step", Value: below,
				Reason: fmt.Sprintf("%s scores %.0f on average against %.0f for single over %d runs. Cap the ladder at %s.", step, r.AvgScore, base.AvgScore, r.Runs, below)})
		case base.AvgCostUSD > 0 && r.AvgCostUSD > 3*base.AvgCostUSD && r.AvgScore < base.AvgScore+5:
			rep.Suggestions = append(rep.Suggestions, Suggestion{Setting: "agent.max_step", Value: below,
				Reason: fmt.Sprintf("%s costs $%.3f per run against $%.3f for single, with no better score. Cap the ladder at %s.", step, r.AvgCostUSD, base.AvgCostUSD, below)})
		}
		if len(rep.Suggestions) > 0 {
			break // the lowest cap that the figures support
		}
	}
	return rep, nil
}
