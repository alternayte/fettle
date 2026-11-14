package spaces

import (
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
