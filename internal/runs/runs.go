package runs

import (
	"context"
	"errors"
	"time"

	"github.com/alternayte/drel"
	"github.com/alternayte/fettle/internal/jobs"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

// Run states (§7.1).
const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusWaiting   = "waiting"
	StatusVerifying = "verifying"
	StatusDone      = "done"
	StatusFailed    = "failed"
	StatusEscalated = "escalated"
	StatusCancelled = "cancelled"
)

// Terminal reports whether a status ends the run.
func Terminal(s string) bool {
	return s == StatusDone || s == StatusFailed || s == StatusCancelled
}

// ErrNotFound means the run does not exist.
var ErrNotFound = errors.New("run not found")

// Args is the River job of a run. Each active run has exactly one (§7.2).
type Args struct {
	RunID uuid.UUID `json:"run_id"`
}

// Kind is the River job kind.
func (Args) Kind() string { return "run" }

// Service creates, claims and wakes runs.
type Service struct {
	e     *drel.Engine
	Queue jobs.Queue
	now   func() time.Time
}

// NewService returns the runs service. Set Queue before use.
func NewService(e *drel.Engine) *Service {
	return &Service{e: e, now: func() time.Time { return time.Now().UTC() }}
}

// SetClock replaces the clock (tests).
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// InsertOpts returns the job options of a run on a queue, scheduled at at (nil for now).
// Jobs are not unique in River: the run status gates inserts (a wake inserts only when it
// moves the run to queued), and the fence lets only the newest attempt append (§7.2).
func InsertOpts(queue string, at *time.Time) *river.InsertOpts {
	o := &river.InsertOpts{Queue: queue, MaxAttempts: 25}
	if at != nil {
		o.ScheduledAt = *at
	}
	return o
}

// Create stores a queued run and its job in the transaction in ctx.
func (s *Service) Create(ctx context.Context, r *Run, queue string) error {
	r.Status = StatusQueued
	return s.e.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		drel.Repo(tx, RunMeta).Add(r)
		if err := tx.SaveChanges(ctx); err != nil {
			return err
		}
		_, err := s.Queue.InsertTx(ctx, tx, Args{RunID: r.ID()}, InsertOpts(queue, nil))
		return err
	})
}

// Get returns a run of a space.
func (s *Service) Get(ctx context.Context, spaceID, id uuid.UUID) (*Run, error) {
	r, err := drel.NewRepository(s.e, RunMeta).Where(Runs.ID.Eq(id)).Where(Runs.SpaceID.Eq(spaceID)).FirstOrNil(ctx)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, ErrNotFound
	}
	return r, nil
}

// Find returns a run by ID in any space (workers only).
func (s *Service) Find(ctx context.Context, id uuid.UUID) (*Run, error) {
	r, err := drel.NewRepository(s.e, RunMeta).Where(Runs.ID.Eq(id)).FirstOrNil(ctx)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, ErrNotFound
	}
	return r, nil
}

// ForEnvelope returns the owner run of an envelope.
func (s *Service) ForEnvelope(ctx context.Context, envelopeID uuid.UUID) (*Run, error) {
	r, err := drel.NewRepository(s.e, RunMeta).Where(Runs.EnvelopeID.Eq(envelopeID)).Where(Runs.Kind.Eq("owner")).FirstOrNil(ctx)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, ErrNotFound
	}
	return r, nil
}

// Children returns the sub-agent and verifier runs of a run.
func (s *Service) Children(ctx context.Context, parent uuid.UUID) ([]*Run, error) {
	return drel.NewRepository(s.e, RunMeta).Where(Runs.ParentRunID.Eq(&parent)).OrderBy(Runs.CreatedAt.Asc()).All(ctx)
}

// Claim starts a job attempt: it increments the fence and marks the run running.
// It returns the run with the new fence, or nil when the run already ended.
func (s *Service) Claim(ctx context.Context, id uuid.UUID, jobID int64) (*Run, error) {
	now := s.now()
	var fence int64
	err := s.e.WithTx(ctx, func(ctx context.Context) error {
		return drel.MustFromContext(ctx).QueryRow(ctx, `UPDATE runs SET fence = fence + 1, status = $1, job_id = $2,
			started_at = COALESCE(started_at, $3), wait_reason = '', updated_at = $4
			WHERE id = $5 AND status NOT IN ('done', 'failed', 'cancelled', 'escalated') RETURNING fence`,
			StatusRunning, jobID, now, now, id).Scan(&fence)
	})
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r, err := s.Find(ctx, id)
	if err != nil {
		return nil, err
	}
	r.Fence = fence
	return r, nil
}

// Wake moves a waiting or escalated run back to queued and inserts its job in the
// transaction in ctx, so the job commits with the event that ends the wait (§7.2).
func (s *Service) Wake(ctx context.Context, id uuid.UUID, queue string) error {
	return s.e.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		var status string
		if err := tx.QueryRow(ctx, "SELECT status FROM runs WHERE id = $1", id).Scan(&status); err != nil {
			return ErrNotFound
		}
		if Terminal(status) {
			return nil
		}
		// A running attempt sees queued when it ends and stops; the new job continues.
		if status != StatusWaiting && status != StatusEscalated && status != StatusRunning {
			return nil // already queued: its job is pending
		}
		if _, err := tx.Exec(ctx, "UPDATE runs SET status = $1, wait_reason = '', wake_at = NULL, updated_at = $2 WHERE id = $3", StatusQueued, s.now(), id); err != nil {
			return err
		}
		_, err := s.Queue.InsertTx(ctx, tx, Args{RunID: id}, InsertOpts(queue, nil))
		return err
	})
}

// ScheduleWake inserts the run's job at a time (timers, §7.5).
func (s *Service) ScheduleWake(ctx context.Context, id uuid.UUID, queue string, at time.Time) error {
	return s.e.WithTx(ctx, func(ctx context.Context) error {
		_, err := s.Queue.InsertTx(ctx, drel.MustFromContext(ctx), Args{RunID: id}, InsertOpts(queue, &at))
		return err
	})
}

// Active returns the running, queued or waiting owner run of a bot in a chat, if any.
func (s *Service) Active(ctx context.Context, botID, chatID uuid.UUID) (*Run, error) {
	var id uuid.UUID
	err := s.e.QueryRow(ctx, `SELECT r.id FROM runs r JOIN envelopes e ON e.id = r.envelope_id
		WHERE r.bot_id = $1 AND e.chat_id = $2 AND r.kind = 'owner' AND r.status IN ('queued','running','waiting','verifying')
		ORDER BY r.created_at DESC LIMIT 1`, botID, chatID).Scan(&id)
	if err != nil {
		return nil, nil
	}
	return s.Find(ctx, id)
}
