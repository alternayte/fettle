package usage

import (
	"time"

	"github.com/alternayte/drel"
	"github.com/google/uuid"
)

// UsageEvent is one unit of metered use: model tokens, computer seconds or search calls.
type UsageEvent struct {
	drel.Model[uuid.UUID] `db:"table=usage_events,index=idx_usage_space_time[space_id,at]"`
	SpaceID               uuid.UUID  `db:"space_id,references=spaces,on_delete=cascade"`
	Kind                  string     `db:"kind,check=kind IN ('model','computer','search')"`
	BotID                 *uuid.UUID `db:"bot_id"`
	ZoneID                *uuid.UUID `db:"zone_id"`
	RunID                 *uuid.UUID `db:"run_id,index"`
	Step                  string     `db:"step"`
	Role                  string     `db:"role"`
	ModelName             string     `db:"model"`
	TokensIn              int64      `db:"tokens_in"`
	TokensOut             int64      `db:"tokens_out"`
	CacheRead             int64      `db:"cache_read"`
	Quantity              int64      `db:"quantity"`
	Unit                  string     `db:"unit,check=unit IN ('tokens','seconds','calls')"`
	Size                  string     `db:"size"`
	CostUSD               float64    `db:"cost_usd"`
	Reported              bool       `db:"reported"`
	At                    time.Time  `db:"at"`
}
