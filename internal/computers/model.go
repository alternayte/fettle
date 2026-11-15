package computers

import (
	"time"

	"github.com/alternayte/drel"
	"github.com/google/uuid"
)

// Caps are the capabilities of a provider (Appendix B).
type Caps struct {
	Snapshot         bool `json:"snapshot"`
	NativeFork       bool `json:"native_fork"`
	NetworkAllowlist bool `json:"network_allowlist"`
	Screen           bool `json:"screen"`
	Persistent       bool `json:"persistent"`
	MaxVCPU          int  `json:"max_vcpu"`
	MaxMemMiB        int  `json:"max_mem_mib"`
}

// ProviderRow is a configured computer provider at instance (SpaceID nil) or space level.
type ProviderRow struct {
	drel.Model[uuid.UUID] `db:"table=providers"`
	SpaceID               *uuid.UUID        `db:"space_id"`
	Kind                  string            `db:"kind,check=kind IN ('host','docker','applecontainer','kiln','e2b')"`
	Name                  string            `db:"name"`
	Config                map[string]string `db:"config"`
	SecretItemID          *uuid.UUID        `db:"secret_item_id"`
	Caps                  Caps              `db:"caps"`
	Enabled               bool              `db:"enabled"`
	TestedAt              *time.Time        `db:"tested_at"`
	TestResult            string            `db:"test_result"`
}

// Computer is the machine of a zone.
type Computer struct {
	drel.Model[uuid.UUID] `db:"index=idx_computers_zone[zone_id]"`
	ZoneID                uuid.UUID  `db:"zone_id,references=zones,on_delete=cascade"`
	ProviderID            uuid.UUID  `db:"provider_id"`
	ProviderRef           string     `db:"provider_ref"`
	State                 string     `db:"state,check=state IN ('none','starting','running','sleeping','stopping','error','quarantined')"`
	AgentdURL             string     `db:"agentd_url"`
	TokenHash             string     `db:"token_hash"`
	CertPEM               string     `db:"cert_pem"`
	ImageDigest           string     `db:"image_digest"`
	WorkspaceRev          int64      `db:"workspace_rev"`
	LastActiveAt          *time.Time `db:"last_active_at"`
	// MeteredAt is the end of the running time that usage already holds; nil when the
	// computer does not run.
	MeteredAt *time.Time `db:"metered_at"`
	Error     string     `db:"error"`
}

// Workspace is one stored revision pointer of a bot home or a zone shared folder (§10.3).
type Workspace struct {
	drel.Model[uuid.UUID] `db:"unique_index=uq_workspaces_owner[owner_key,kind]"`
	BotID                 *uuid.UUID `db:"bot_id"`
	ZoneID                *uuid.UUID `db:"zone_id"`
	OwnerKey              string     `db:"owner_key"`
	Kind                  string     `db:"kind,check=kind IN ('bot_home','zone_shared')"`
	Rev                   int64      `db:"rev"`
	ObjectKey             string     `db:"object_key"`
	Size                  int64      `db:"size"`
	SHA256                string     `db:"sha256"`
	SpaceID               uuid.UUID  `db:"space_id"`
}
