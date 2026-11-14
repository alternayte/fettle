package settings

import (
	"github.com/alternayte/drel"
	"github.com/google/uuid"
)

// Setting is one versioned value at instance or space scope. Value is JSON text.
type Setting struct {
	drel.Model[uuid.UUID] `db:"unique_index=uq_settings_scope_key[scope,space_key,key]"`
	Scope                 string     `db:"scope,check=scope IN ('instance','space')"`
	SpaceID               *uuid.UUID `db:"space_id,references=spaces,on_delete=cascade"`
	SpaceKey              string     `db:"space_key"`
	Key                   string     `db:"key"`
	Value                 string     `db:"value"`
	Revision              int        `db:"revision"`
	UpdatedBy             *uuid.UUID `db:"updated_by"`
}

// SettingHistory records one change of a setting.
type SettingHistory struct {
	drel.Model[uuid.UUID] `db:"table=settings_history,index=idx_settings_history_setting[setting_id,revision]"`
	SettingID             uuid.UUID  `db:"setting_id,references=settings,on_delete=cascade"`
	Revision              int        `db:"revision"`
	OldValue              *string    `db:"old_value"`
	NewValue              string     `db:"new_value"`
	UpdatedBy             *uuid.UUID `db:"updated_by"`
}
