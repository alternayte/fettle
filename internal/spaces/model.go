package spaces

import (
	"time"

	"github.com/alternayte/drel"
	"github.com/google/uuid"
)

// Space is an authorization boundary that holds bots, chats, zones, skills, memory and connectors.
type Space struct {
	drel.Model[uuid.UUID]
	Name     string `db:"name"`
	TimeZone string `db:"time_zone"`
}

// SpaceMember links a user to a space with a role.
type SpaceMember struct {
	drel.Model[uuid.UUID] `db:"table=space_members,unique_index=uq_space_members[space_id,user_id]"`
	SpaceID               uuid.UUID `db:"space_id,references=spaces,on_delete=cascade"`
	UserID                uuid.UUID `db:"user_id,references=users,on_delete=cascade,index"`
	Role                  string    `db:"role,check=role IN ('owner','member')"`
}

// Invite is a link that adds a person to a space with a role (§16). The link holds a
// random token; the table holds only its hash. An invite works once, until it expires.
type Invite struct {
	drel.Model[uuid.UUID] `db:"table=space_invites,unique_index=uq_space_invites_hash[token_hash],index=idx_space_invites_space[space_id]"`
	SpaceID               uuid.UUID  `db:"space_id,references=spaces,on_delete=cascade"`
	TokenHash             string     `db:"token_hash"`
	Role                  string     `db:"role,check=role IN ('owner','member')"`
	CreatedBy             uuid.UUID  `db:"created_by"`
	ExpiresAt             time.Time  `db:"expires_at"`
	AcceptedBy            *uuid.UUID `db:"accepted_by"`
	AcceptedAt            *time.Time `db:"accepted_at"`
	RevokedAt             *time.Time `db:"revoked_at"`
}
