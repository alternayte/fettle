// Package audit keeps the append-only audit log. The application only inserts rows (SI-18).
package audit

import (
	"context"
	"time"

	"github.com/alternayte/drel"
	"github.com/google/uuid"
)

// Actor names who made a change.
type Actor struct {
	Kind string // user, bot, system
	ID   string
}

// System is the actor for changes that Fettle makes itself.
var System = Actor{Kind: "system", ID: "fettle"}

// Log writes and reads audit rows.
type Log struct{ e *drel.Engine }

// New returns the audit log.
func New(e *drel.Engine) *Log { return &Log{e: e} }

// Record appends one row. It joins the transaction in ctx when one exists, so the
// audit row commits with the change it describes.
func (l *Log) Record(ctx context.Context, a Actor, spaceID *uuid.UUID, action, target string, details map[string]string) error {
	if details == nil {
		details = map[string]string{}
	}
	return l.e.WithTx(ctx, func(ctx context.Context) error {
		drel.Repo(drel.MustFromContext(ctx), AuditEntryMeta).Add(&AuditEntry{
			ActorKind: a.Kind,
			ActorID:   a.ID,
			SpaceID:   spaceID,
			Action:    action,
			Target:    target,
			Details:   details,
		})
		return nil
	})
}

// Filter selects audit rows.
type Filter struct {
	SpaceID *uuid.UUID
	Action  string
	Since   *time.Time
	Limit   int
}

// List returns rows newest first.
func (l *Log) List(ctx context.Context, f Filter) ([]*AuditEntry, error) {
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	q := drel.NewRepository(l.e, AuditEntryMeta).Limit(limit)
	if f.SpaceID != nil {
		q = q.Where(AuditEntries.SpaceID.Eq(f.SpaceID))
	}
	if f.Action != "" {
		q = q.Where(AuditEntries.Action.Eq(f.Action))
	}
	if f.Since != nil {
		q = q.Where(AuditEntries.CreatedAt.GTE(*f.Since))
	}
	return q.OrderBy(AuditEntries.CreatedAt.Desc()).All(ctx)
}
