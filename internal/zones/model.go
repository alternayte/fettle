package zones

import (
	"github.com/alternayte/drel"
	"github.com/google/uuid"
)

// Zone is a trust zone: credential grants, a network policy and one computer (§10.1).
type Zone struct {
	drel.Model[uuid.UUID] `db:"unique_index=uq_zones_space_name[space_id,name]"`
	SpaceID               uuid.UUID  `db:"space_id,references=spaces,on_delete=cascade"`
	Name                  string     `db:"name"`
	ProviderID            *uuid.UUID `db:"provider_id"`
	Network               string     `db:"network,check=network IN ('open','allowlist')"`
	AllowHosts            []string   `db:"allow_hosts"`
	Size                  string     `db:"size,check=size IN ('small','browser','desktop')"`
	IdleMinutes           int        `db:"idle_minutes"`
	// ShareBlocked stops handoffs from copying files of this zone's shared folder to
	// other zones (§9.7).
	ShareBlocked bool `db:"share_blocked"`
}

// ZoneGrant lets a zone use one vault item or one connector account.
type ZoneGrant struct {
	drel.Model[uuid.UUID] `db:"unique_index=uq_zone_grants[zone_id,kind,target_id]"`
	ZoneID                uuid.UUID `db:"zone_id,references=zones,on_delete=cascade"`
	Kind                  string    `db:"kind,check=kind IN ('vault_item','connector_account')"`
	TargetID              uuid.UUID `db:"target_id"`
}
