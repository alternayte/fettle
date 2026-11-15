// Package learning records steering signals, scores runs and proposes skill patches (§14).
package learning

import (
	"time"

	"github.com/alternayte/drel"
	"github.com/google/uuid"
)

// Signal types (Appendix F).
const (
	Correction               = "correction"
	DraftEdited              = "draft_edited"
	ApprovalDenied           = "approval_denied"
	Rerun                    = "rerun"
	UnrequestedTakeover      = "unrequested_takeover"
	ThumbsDown               = "thumbs_down"
	CardDeclined             = "card_declined"
	EscalationAcceptedAnyway = "escalation_accepted_anyway"
	UncertainConfirmedWrong  = "uncertain_confirmed_wrong"
)

// Signal is one steering signal: a human action that shows a bot was wrong or incomplete
// (§14.1). It links to the run, the bot, the step and the skills the run loaded.
type Signal struct {
	drel.Model[uuid.UUID] `db:"table=steering_signals,index=idx_signals_space_time[space_id,created_at],index=idx_signals_run[run_id],unique_index=uq_signals_ref[space_id,type,ref]"`
	SpaceID               uuid.UUID  `db:"space_id,references=spaces,on_delete=cascade"`
	RunID                 *uuid.UUID `db:"run_id"`
	BotID                 *uuid.UUID `db:"bot_id"`
	Step                  string     `db:"step"`
	Skills                []string   `db:"skills"`
	Type                  string     `db:"type,check=type IN ('correction','draft_edited','approval_denied','rerun','unrequested_takeover','thumbs_down','card_declined','escalation_accepted_anyway','uncertain_confirmed_wrong')"`
	Detail                string     `db:"detail"`
	// Ref is the card, message or run that the signal comes from, so one action makes
	// one signal.
	Ref    string `db:"ref"`
	UserID string `db:"user_id"`
}

// Score is the score of one finished owner run (§14.2), with the parts it comes from.
type Score struct {
	drel.Model[uuid.UUID] `db:"table=run_scores,unique_index=uq_run_scores_run[run_id],index=idx_run_scores_space_time[space_id,finished_at]"`
	SpaceID               uuid.UUID `db:"space_id,references=spaces,on_delete=cascade"`
	RunID                 uuid.UUID `db:"run_id"`
	BotID                 uuid.UUID `db:"bot_id"`
	Step                  string    `db:"step"`
	Skills                []string  `db:"skills"`
	ModelName             string    `db:"model_name"`
	VerifierPass          bool      `db:"verifier_pass"`
	Signals               int       `db:"signals"`
	WithinBudget          bool      `db:"within_budget"`
	Escalated             bool      `db:"escalated"`
	Value                 int       `db:"value"`
	CostUSD               float64   `db:"cost_usd"`
	DurationMS            int64     `db:"duration_ms"`
	FinishedAt            time.Time `db:"finished_at"`
}
