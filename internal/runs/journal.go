// Package runs keeps runs and their journals (§7). The journal is the append-only
// list of a run's steps; it is the checkpoint and the timeline of the run.
package runs

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/alternayte/drel"
	"github.com/alternayte/fettle/internal/objects"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Event types (Appendix D).
const (
	EvRunStarted        = "run_started"
	EvRouteDecision     = "route_decision"
	EvContextBuilt      = "context_built"
	EvModelTurn         = "model_turn"
	EvModelSwitch       = "model_switch"
	EvToolCall          = "tool_call"
	EvToolIntent        = "tool_intent"
	EvToolResult        = "tool_result"
	EvToolUncertain     = "tool_uncertain"
	EvApprovalRequested = "approval_requested"
	EvApprovalDecided   = "approval_decided"
	EvCardPosted        = "card_posted"
	EvCardAnswered      = "card_answered"
	EvSteer             = "steer"
	EvSubagentsStarted  = "subagents_started"
	EvSubagentResult    = "subagent_result"
	EvMergeResult       = "merge_result"
	EvHandoffSent       = "handoff_sent"
	EvHandoffResult     = "handoff_result"
	EvVerifyStarted     = "verify_started"
	EvVerifyResult      = "verify_result"
	EvComputerWait      = "computer_wait"
	EvComputerAcquired  = "computer_acquired"
	EvComputerReleased  = "computer_released"
	EvTakeoverRequested = "takeover_requested"
	EvTakeoverDone      = "takeover_done"
	EvMemoryWrite       = "memory_write"
	EvSummary           = "summary"
	EvBudgetReached     = "budget_reached"
	EvSleepUntil        = "sleep_until"
	EvCancel            = "cancel"
	EvRunFinished       = "run_finished"
)

// PayloadLimit is the event size above which the data goes to the object store (§7.1).
const PayloadLimit = 8 * 1024

// PreviewLimit is the preview size kept in an event whose payload is stored outside.
const PreviewLimit = 500

// ErrStaleFence means another job attempt owns the run; this attempt must stop (§7.2).
var ErrStaleFence = errors.New("run fence is stale: another attempt owns this run")

// NoFence appends without a fence check. Card answers, steer messages and cancels use it.
const NoFence int64 = -1

// New is an event to append.
type New struct {
	Type string
	Data any
}

// Sealer encrypts payloads with the space data key (SI-14).
type Sealer interface {
	Seal(ctx context.Context, spaceID uuid.UUID, plain []byte, aad string) ([]byte, error)
	Open(ctx context.Context, spaceID uuid.UUID, sealed []byte, aad string) ([]byte, error)
}

// Journal appends and loads run events.
type Journal struct {
	e       *drel.Engine
	objects objects.Store
	sealer  Sealer
	// Redact is applied to every event before storage (§11.4).
	Redact func(ctx context.Context, spaceID uuid.UUID, s string) string
	now    func() time.Time
}

// NewJournal returns a journal.
func NewJournal(e *drel.Engine, store objects.Store, sealer Sealer) *Journal {
	return &Journal{e: e, objects: store, sealer: sealer, now: func() time.Time { return time.Now().UTC() }}
}

// SetClock replaces the clock (tests).
func (j *Journal) SetClock(now func() time.Time) { j.now = now }

func payloadKey(runID uuid.UUID, seq int64) string {
	return fmt.Sprintf("payloads/%s/%d", runID, seq)
}

type stored struct {
	Preview   string `json:"preview"`
	Truncated bool   `json:"truncated"`
	Bytes     int    `json:"bytes"`
}

// Update changes run columns in the same transaction as an append.
type Update struct {
	Status     *string
	WaitReason *string
	WakeAt     **time.Time
	TokensIn   int64
	TokensOut  int64
	CostUSD    float64
	Finished   bool
}

// Append writes events in order in one transaction, after a fence check. It joins the
// transaction in ctx when one exists. It returns the stored events.
func (j *Journal) Append(ctx context.Context, runID uuid.UUID, fence int64, up *Update, evs ...New) ([]*RunEvent, error) {
	var out []*RunEvent
	err := j.e.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		n := int64(len(evs))
		q := "UPDATE runs SET last_seq = last_seq + $1, updated_at = $2"
		args := []any{n, j.now()}
		if up != nil {
			if up.Status != nil {
				args = append(args, *up.Status)
				q += fmt.Sprintf(", status = $%d", len(args))
			}
			if up.WaitReason != nil {
				args = append(args, *up.WaitReason)
				q += fmt.Sprintf(", wait_reason = $%d", len(args))
			}
			if up.WakeAt != nil {
				args = append(args, *up.WakeAt)
				q += fmt.Sprintf(", wake_at = $%d", len(args))
			}
			if up.TokensIn != 0 || up.TokensOut != 0 || up.CostUSD != 0 {
				args = append(args, up.TokensIn, up.TokensOut, up.CostUSD)
				q += fmt.Sprintf(", tokens_in = tokens_in + $%d, tokens_out = tokens_out + $%d, cost_usd = cost_usd + $%d", len(args)-2, len(args)-1, len(args))
			}
			if up.Finished {
				args = append(args, j.now())
				q += fmt.Sprintf(", finished_at = $%d", len(args))
			}
		}
		args = append(args, runID)
		q += fmt.Sprintf(" WHERE id = $%d", len(args))
		if fence != NoFence {
			args = append(args, fence)
			q += fmt.Sprintf(" AND fence = $%d", len(args))
		}
		q += " RETURNING last_seq, space_id"
		var last int64
		var spaceID uuid.UUID
		if err := tx.QueryRow(ctx, q, args...).Scan(&last, &spaceID); err != nil {
			if errors.Is(err, drel.ErrNotFound) || isNoRows(err) {
				return ErrStaleFence
			}
			return err
		}
		repo := drel.Repo(tx, RunEventMeta)
		seq := last - n
		for _, ev := range evs {
			seq++
			data, err := json.Marshal(ev.Data)
			if err != nil {
				return err
			}
			if j.Redact != nil {
				data = []byte(j.Redact(ctx, spaceID, string(data)))
			}
			re := &RunEvent{RunID: runID, Seq: seq, At: j.now(), Type: ev.Type, Data: string(data)}
			if len(data) > PayloadLimit {
				key := payloadKey(runID, seq)
				sealed, err := j.sealer.Seal(ctx, spaceID, data, key)
				if err != nil {
					return err
				}
				if err := j.objects.Put(ctx, key, bytes.NewReader(sealed), int64(len(sealed))); err != nil {
					return err
				}
				pv := string(data)
				if len(pv) > PreviewLimit {
					pv = pv[:PreviewLimit]
				}
				small, _ := json.Marshal(stored{Preview: pv, Truncated: true, Bytes: len(data)})
				re.Data, re.PayloadKey = string(small), &key
			}
			repo.Add(re)
			out = append(out, re)
		}
		return tx.SaveChanges(ctx)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func isNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows)
}

// Load returns all events of a run in order, with large payloads read back from the object store.
func (j *Journal) Load(ctx context.Context, runID uuid.UUID) ([]*RunEvent, error) {
	evs, err := drel.NewRepository(j.e, RunEventMeta).Where(RunEvents.RunID.Eq(runID)).OrderBy(RunEvents.Seq.Asc()).All(ctx)
	if err != nil {
		return nil, err
	}
	var spaceID uuid.UUID
	for _, ev := range evs {
		if ev.PayloadKey == nil {
			continue
		}
		if spaceID == uuid.Nil {
			if err := j.e.QueryRow(ctx, "SELECT space_id FROM runs WHERE id = $1", runID).Scan(&spaceID); err != nil {
				return nil, err
			}
		}
		full, err := j.payload(ctx, spaceID, *ev.PayloadKey)
		if err != nil {
			return nil, err
		}
		ev.Data = string(full)
	}
	return evs, nil
}

func (j *Journal) payload(ctx context.Context, spaceID uuid.UUID, key string) ([]byte, error) {
	rc, err := j.objects.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("payload %s: %w", key, err)
	}
	defer rc.Close()
	sealed, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	return j.sealer.Open(ctx, spaceID, sealed, key)
}

// Page returns stored events (with previews, not full payloads) for the timeline.
func (j *Journal) Page(ctx context.Context, runID uuid.UUID, afterSeq int64, limit int) ([]*RunEvent, error) {
	if limit <= 0 || limit > 10000 {
		limit = 10000
	}
	return drel.NewRepository(j.e, RunEventMeta).Where(RunEvents.RunID.Eq(runID)).Where(RunEvents.Seq.GT(afterSeq)).OrderBy(RunEvents.Seq.Asc()).Limit(limit).All(ctx)
}
