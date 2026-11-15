package learning

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/alternayte/drel"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/envelopes"
	"github.com/alternayte/fettle/internal/runs"
)

// Service records steering signals and keeps run scores.
type Service struct {
	e         *drel.Engine
	runs      *runs.Service
	journal   *runs.Journal
	envelopes *envelopes.Service
	now       func() time.Time
}

// New returns the learning service.
func New(e *drel.Engine, r *runs.Service, j *runs.Journal, env *envelopes.Service) *Service {
	return &Service{e: e, runs: r, journal: j, envelopes: env, now: time.Now}
}

// SetClock replaces the clock (tests).
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// Facts are what a run's journal says about the run, for its score and its signals.
type Facts struct {
	Step         string
	Skills       []string
	ModelName    string
	VerifierPass bool
	Escalated    bool
	BudgetCard   bool
}

// facts reads the journal of a run.
func (s *Service) facts(ctx context.Context, r *runs.Run) (Facts, error) {
	var f Facts
	evs, err := s.journal.Load(ctx, r.ID())
	if err != nil {
		return f, err
	}
	skills := map[string]bool{}
	if env, err := s.envelopes.Get(ctx, r.SpaceID, r.EnvelopeID); err == nil && env.Decision.Skill != "" {
		skills[env.Decision.Skill] = true
	}
	verified, lastPass := false, false
	for _, ev := range evs {
		switch ev.Type {
		case runs.EvRunStarted:
			var d struct {
				Step string `json:"step"`
			}
			if json.Unmarshal([]byte(ev.Data), &d) == nil && d.Step != "" {
				f.Step = d.Step
			}
		case runs.EvModelTurn:
			var d struct {
				Model string `json:"model"`
			}
			if json.Unmarshal([]byte(ev.Data), &d) == nil && d.Model != "" {
				f.ModelName = d.Model
			}
		case runs.EvRunFinished:
			var d struct {
				Status string `json:"status"`
				Turn   *struct {
					Model string `json:"model"`
				} `json:"turn"`
			}
			if json.Unmarshal([]byte(ev.Data), &d) == nil {
				if d.Turn != nil && d.Turn.Model != "" {
					f.ModelName = d.Turn.Model
				}
				if d.Status == runs.StatusEscalated {
					f.Escalated = true
				}
			}
		case runs.EvToolCall:
			var d struct {
				Tool string          `json:"tool"`
				Args json.RawMessage `json:"args"`
			}
			if json.Unmarshal([]byte(ev.Data), &d) == nil && d.Tool == "load_skill" {
				var a struct {
					Name string `json:"name"`
				}
				if json.Unmarshal(d.Args, &a) == nil && a.Name != "" {
					skills[strings.ToLower(strings.TrimPrefix(a.Name, "/"))] = true
				}
			}
		case runs.EvVerifyResult:
			var d struct {
				Pass bool `json:"pass"`
			}
			if json.Unmarshal([]byte(ev.Data), &d) == nil {
				verified, lastPass = true, d.Pass
			}
		case runs.EvCardPosted:
			var d struct {
				Kind string `json:"kind"`
			}
			if json.Unmarshal([]byte(ev.Data), &d) == nil {
				switch d.Kind {
				case "verification", "turn_limit":
					f.Escalated = true
				case "budget":
					f.Escalated, f.BudgetCard = true, true
				}
			}
		}
	}
	if f.Step == "" {
		f.Step = "direct"
	}
	// A step without a verification (a direct reply) counts as passed (§9.5).
	f.VerifierPass = !verified || lastPass
	for k := range skills {
		f.Skills = append(f.Skills, k)
	}
	sort.Strings(f.Skills)
	if f.Skills == nil {
		f.Skills = []string{}
	}
	return f, nil
}

// Value is the run score of §14.2.
func Value(verifierPass bool, signals int, withinBudget, escalated bool) int {
	v := 0
	if verifierPass {
		v += 50
	}
	if signals == 0 {
		v += 30
	}
	if withinBudget {
		v += 10
	}
	if !escalated {
		v += 10
	}
	return v
}

// ScoreRun computes and stores the score of a finished owner run. Later signals call it
// again, so the score follows them (REQ-LRN-002).
func (s *Service) ScoreRun(ctx context.Context, runID uuid.UUID) (*Score, error) {
	r, err := s.runs.Find(ctx, runID)
	if err != nil {
		return nil, err
	}
	if r.Kind != "owner" || r.FinishedAt == nil {
		return nil, nil
	}
	f, err := s.facts(ctx, r)
	if err != nil {
		return nil, err
	}
	env, err := s.envelopes.Get(ctx, r.SpaceID, r.EnvelopeID)
	if err != nil {
		return nil, err
	}
	var spent float64
	_ = s.e.QueryRow(ctx, "SELECT COALESCE(SUM(cost_usd), 0) FROM runs WHERE envelope_id = $1", r.EnvelopeID).Scan(&spent)
	within := !f.BudgetCard && (env.Budget.USD <= 0 || spent <= env.Budget.USD)
	var signals int
	if err := s.e.QueryRow(ctx, "SELECT COUNT(*) FROM steering_signals WHERE run_id = $1", runID).Scan(&signals); err != nil {
		return nil, err
	}
	var dur int64
	if r.StartedAt != nil {
		dur = r.FinishedAt.Sub(*r.StartedAt).Milliseconds()
	}
	sc := &Score{SpaceID: r.SpaceID, RunID: runID, BotID: r.BotID, Step: f.Step, Skills: f.Skills, ModelName: f.ModelName,
		VerifierPass: f.VerifierPass, Signals: signals, WithinBudget: within, Escalated: f.Escalated,
		Value: Value(f.VerifierPass, signals, within, f.Escalated), CostUSD: spent, DurationMS: dur, FinishedAt: r.FinishedAt.UTC()}
	err = s.e.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		old, err := drel.Repo(tx, ScoreMeta).Where(Scores.RunID.Eq(runID)).FirstOrNil(ctx)
		if err != nil {
			return err
		}
		if old != nil {
			sc.SetID(old.ID())
			old.Step, old.Skills, old.ModelName = sc.Step, sc.Skills, sc.ModelName
			old.VerifierPass, old.Signals, old.WithinBudget, old.Escalated = sc.VerifierPass, sc.Signals, sc.WithinBudget, sc.Escalated
			old.Value, old.CostUSD, old.DurationMS, old.FinishedAt = sc.Value, sc.CostUSD, sc.DurationMS, sc.FinishedAt
		} else {
			drel.Repo(tx, ScoreMeta).Add(sc)
		}
		return tx.SaveChanges(ctx)
	})
	return sc, err
}

// Input is a new signal. RunID is the run that the human action judges; without a run
// the signal still counts for its bot.
type Input struct {
	SpaceID uuid.UUID
	RunID   *uuid.UUID
	BotID   *uuid.UUID
	Type    string
	Detail  string
	Ref     string
	UserID  string
}

// ErrBadType means the signal type is not in Appendix F.
var ErrBadType = errors.New("unknown steering signal type")

var types = map[string]bool{Correction: true, DraftEdited: true, ApprovalDenied: true, Rerun: true, UnrequestedTakeover: true, ThumbsDown: true, CardDeclined: true, EscalationAcceptedAnyway: true, UncertainConfirmedWrong: true}

// Record stores a signal with its run, bot, step and skills, and updates the run's score.
// The same action (type and ref) records once.
func (s *Service) Record(ctx context.Context, in Input) (*Signal, error) {
	if !types[in.Type] {
		return nil, ErrBadType
	}
	sig := &Signal{SpaceID: in.SpaceID, RunID: in.RunID, BotID: in.BotID, Type: in.Type, Detail: truncate(in.Detail, 2000), Ref: in.Ref, UserID: in.UserID, Skills: []string{}}
	if sig.Ref == "" {
		sig.Ref = uuid.NewString()
	}
	if in.RunID != nil {
		r, err := s.runs.Find(ctx, *in.RunID)
		if err != nil {
			return nil, err
		}
		// A helper's or a verifier's run counts for its owner run.
		for r.Kind != "owner" && r.ParentRunID != nil {
			p, err := s.runs.Find(ctx, *r.ParentRunID)
			if err != nil {
				return nil, err
			}
			r = p
		}
		id, bot := r.ID(), r.BotID
		sig.RunID, sig.BotID = &id, &bot
		f, err := s.facts(ctx, r)
		if err != nil {
			return nil, err
		}
		sig.Step, sig.Skills = f.Step, f.Skills
	}
	err := s.e.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		dup, err := drel.Repo(tx, SignalMeta).Where(Signals.SpaceID.Eq(sig.SpaceID)).Where(Signals.Type.Eq(sig.Type)).Where(Signals.Ref.Eq(sig.Ref)).FirstOrNil(ctx)
		if err != nil {
			return err
		}
		if dup != nil {
			sig = dup
			return nil
		}
		drel.Repo(tx, SignalMeta).Add(sig)
		return tx.SaveChanges(ctx)
	})
	if err != nil {
		return nil, err
	}
	if sig.RunID != nil {
		if _, err := s.ScoreRun(ctx, *sig.RunID); err != nil {
			return sig, err
		}
	}
	return sig, nil
}

// Signals lists the signals of a space, newest first.
func (s *Service) Signals(ctx context.Context, spaceID uuid.UUID, since time.Time, limit int) ([]*Signal, error) {
	q := drel.NewRepository(s.e, SignalMeta).Where(Signals.SpaceID.Eq(spaceID)).Where(Signals.CreatedAt.GTE(since.UTC())).OrderBy(Signals.CreatedAt.Desc())
	if limit > 0 {
		q = q.Limit(limit)
	}
	return q.All(ctx)
}

// Scores lists the run scores of a space that finished since a time.
func (s *Service) Scores(ctx context.Context, spaceID uuid.UUID, since time.Time) ([]*Score, error) {
	return drel.NewRepository(s.e, ScoreMeta).Where(Scores.SpaceID.Eq(spaceID)).Where(Scores.FinishedAt.GTE(since.UTC())).OrderBy(Scores.FinishedAt.Desc()).All(ctx)
}

// ScoreOf returns the score of a run, or nil.
func (s *Service) ScoreOf(ctx context.Context, runID uuid.UUID) (*Score, error) {
	return drel.NewRepository(s.e, ScoreMeta).Where(Scores.RunID.Eq(runID)).FirstOrNil(ctx)
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
