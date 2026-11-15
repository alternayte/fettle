package screens

import (
	"time"

	"github.com/alternayte/drel"
	"github.com/google/uuid"
)

// Lease is who uses the screen of a bot now (§10.8): a person with the control lease, or
// the bot during one browser action. One row per bot; an expired row is free.
type Lease struct {
	drel.Model[uuid.UUID] `db:"table=screen_leases"`
	BotID                 uuid.UUID `db:"bot_id,unique"`
	SpaceID               uuid.UUID `db:"space_id"`
	HolderKind            string    `db:"holder_kind"` // person or bot
	HolderID              string    `db:"holder_id"`
	ExpiresAt             time.Time `db:"expires_at"`
}
