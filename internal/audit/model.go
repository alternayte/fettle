package audit

import (
	"github.com/alternayte/drel"
	"github.com/google/uuid"
)

// AuditEntry is one append-only audit row. The application never updates or deletes it.
type AuditEntry struct {
	drel.Model[uuid.UUID] `db:"table=audit_log,index=idx_audit_space_time[space_id,created_at]"`
	ActorKind             string            `db:"actor_kind,check=actor_kind IN ('user','bot','system')"`
	ActorID               string            `db:"actor_id"`
	SpaceID               *uuid.UUID        `db:"space_id"`
	Action                string            `db:"action,index"`
	Target                string            `db:"target"`
	Details               map[string]string `db:"details"`
}
