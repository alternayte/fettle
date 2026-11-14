// Package jobs wraps River so feature packages can enqueue jobs inside drel
// transactions on SQLite and PostgreSQL alike.
package jobs

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/alternayte/drel"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/riverdriver/riversqlite"
	"github.com/riverqueue/river/rivertype"
)

// Queue names (§7.2).
const (
	Interactive = "interactive"
	Background  = "background"
	Maintenance = "maintenance"
)

// Config sets worker counts and the rescue timeout.
type Config struct {
	Interactive, Background, Maintenance int
	RescueAfter                          time.Duration
	PeriodicJobs                         []*river.PeriodicJob
	Logger                               *slog.Logger
	// Insecure test option: poll quickly.
	FetchPoll time.Duration
}

// Queue enqueues and runs jobs.
type Queue interface {
	// InsertTx enqueues a job in the drel transaction tx, so it commits with the caller's writes.
	InsertTx(ctx context.Context, tx *drel.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
	Insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
	// Heartbeat marks a running job as alive, so the rescuer does not take it while a long step runs.
	Heartbeat(ctx context.Context, jobID int64) error
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	// StopFetching stops new jobs and lets running jobs reach a safe point.
	StopFetching(ctx context.Context) error
	// StopAndCancel stops and cancels the work contexts of running jobs; they resume later.
	StopAndCancel(ctx context.Context) error
	JobCancel(ctx context.Context, jobID int64) error
}

type queue[TTx any] struct {
	client    *river.Client[TTx]
	toTx      func(*drel.Tx) (TTx, error)
	heartbeat func(ctx context.Context, jobID int64) error
}

func (q *queue[TTx]) InsertTx(ctx context.Context, tx *drel.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	t, err := q.toTx(tx)
	if err != nil {
		return nil, err
	}
	return q.client.InsertTx(ctx, t, args, opts)
}

func (q *queue[TTx]) Insert(ctx context.Context, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error) {
	return q.client.Insert(ctx, args, opts)
}

func (q *queue[TTx]) Heartbeat(ctx context.Context, jobID int64) error {
	return q.heartbeat(ctx, jobID)
}
func (q *queue[TTx]) Start(ctx context.Context) error { return q.client.Start(ctx) }
func (q *queue[TTx]) Stop(ctx context.Context) error  { return q.client.Stop(ctx) }
func (q *queue[TTx]) StopFetching(ctx context.Context) error {
	return q.client.Stop(ctx)
}
func (q *queue[TTx]) StopAndCancel(ctx context.Context) error {
	return q.client.StopAndCancel(ctx)
}
func (q *queue[TTx]) JobCancel(ctx context.Context, jobID int64) error {
	_, err := q.client.JobCancel(ctx, jobID)
	return err
}

func riverConfig(c Config, workers *river.Workers) *river.Config {
	rescue := c.RescueAfter
	if rescue <= 0 {
		rescue = 60 * time.Second
	}
	rc := &river.Config{
		Queues: map[string]river.QueueConfig{
			Interactive: {MaxWorkers: max(1, c.Interactive)},
			Background:  {MaxWorkers: max(1, c.Background)},
			Maintenance: {MaxWorkers: max(1, c.Maintenance)},
		},
		Workers:              workers,
		RescueStuckJobsAfter: rescue,
		JobTimeout:           -1,
		PeriodicJobs:         c.PeriodicJobs,
		Logger:               c.Logger,
	}
	if c.FetchPoll > 0 {
		rc.FetchPollInterval = c.FetchPoll
		rc.FetchCooldown = c.FetchPoll / 2
	}
	return rc
}

// NewPostgres builds a queue on a pgx pool.
func NewPostgres(pool *pgxpool.Pool, c Config, workers *river.Workers) (Queue, error) {
	client, err := river.NewClient(riverpgxv5.New(pool), riverConfig(c, workers))
	if err != nil {
		return nil, err
	}
	return &queue[pgx.Tx]{
		client: client,
		toTx: func(tx *drel.Tx) (pgx.Tx, error) {
			t, ok := tx.Underlying().(pgx.Tx)
			if !ok {
				return nil, fmt.Errorf("jobs: drel transaction is %T, not pgx.Tx", tx.Underlying())
			}
			return t, nil
		},
		heartbeat: func(ctx context.Context, jobID int64) error {
			_, err := pool.Exec(ctx, "UPDATE river_job SET attempted_at = now() WHERE id = $1 AND state = 'running'", jobID)
			return err
		},
	}, nil
}

// NewSQLite builds a queue on River's own single-connection SQLite pool.
func NewSQLite(db *sql.DB, c Config, workers *river.Workers) (Queue, error) {
	client, err := river.NewClient(riversqlite.New(db), riverConfig(c, workers))
	if err != nil {
		return nil, err
	}
	return &queue[*sql.Tx]{
		client: client,
		toTx: func(tx *drel.Tx) (*sql.Tx, error) {
			t, ok := tx.Underlying().(*sql.Tx)
			if !ok {
				return nil, fmt.Errorf("jobs: drel transaction is %T, not *sql.Tx", tx.Underlying())
			}
			return t, nil
		},
		heartbeat: func(ctx context.Context, jobID int64) error {
			_, err := db.ExecContext(ctx, "UPDATE river_job SET attempted_at = datetime('now', 'subsec') WHERE id = ? AND state = 'running'", jobID)
			return err
		},
	}, nil
}
