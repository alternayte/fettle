package runs

import (
	"time"

	"github.com/alternayte/drel"
	"github.com/google/uuid"
)

// Run is one durable execution of an envelope.
type Run struct {
	drel.Model[uuid.UUID] `db:"index=idx_runs_status[status]"`
	SpaceID               uuid.UUID  `db:"space_id"`
	EnvelopeID            uuid.UUID  `db:"envelope_id,references=envelopes,on_delete=cascade,index"`
	BotID                 uuid.UUID  `db:"bot_id,index"`
	ParentRunID           *uuid.UUID `db:"parent_run_id,index"`
	Kind                  string     `db:"kind,check=kind IN ('owner','subagent','verifier')"`
	Status                string     `db:"status,check=status IN ('queued','running','waiting','verifying','done','failed','escalated','cancelled')"`
	WaitReason            string     `db:"wait_reason"`
	JobID                 *int64     `db:"job_id"`
	Fence                 int64      `db:"fence"`
	LastSeq               int64      `db:"last_seq"`
	WakeAt                *time.Time `db:"wake_at"`
	TokensIn              int64      `db:"tokens_in"`
	TokensOut             int64      `db:"tokens_out"`
	CostUSD               float64    `db:"cost_usd"`
	StartedAt             *time.Time `db:"started_at"`
	FinishedAt            *time.Time `db:"finished_at"`
}

// RunEvent is one journal step of a run.
type RunEvent struct {
	drel.Model[uuid.UUID] `db:"table=run_events,unique_index=uq_run_events_seq[run_id,seq]"`
	RunID                 uuid.UUID `db:"run_id,references=runs,on_delete=cascade"`
	Seq                   int64     `db:"seq"`
	At                    time.Time `db:"at"`
	Type                  string    `db:"type"`
	Data                  string    `db:"data"`
	PayloadKey            *string   `db:"payload_key"`
}
