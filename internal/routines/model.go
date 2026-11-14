package routines

import (
	"time"

	"github.com/alternayte/drel"
	"github.com/google/uuid"
)

// Routine runs a prompt for a bot on a cron schedule or a signed webhook (§13.2).
type Routine struct {
	drel.Model[uuid.UUID] `db:"index=idx_routines_due[enabled,next_at]"`
	SpaceID               uuid.UUID `db:"space_id,references=spaces,on_delete=cascade"`
	BotID                 uuid.UUID `db:"bot_id,index"`
	Name                  string    `db:"name"`
	Prompt                string    `db:"prompt"`
	Trigger               string    `db:"trigger,check=trigger IN ('cron','webhook')"`
	Cron                  string    `db:"cron"`
	TZ                    string    `db:"tz"`
	// WebhookSecret is the HMAC key, sealed with the space data key (base64).
	WebhookSecret     string     `db:"webhook_secret"`
	WebhookSecretHash string     `db:"webhook_secret_hash"`
	Enabled           bool       `db:"enabled"`
	Notify            string     `db:"notify,check=notify IN ('always','on_failure','never')"`
	NextAt            *time.Time `db:"next_at"`
	// SendApproved is set after a person allowed the routine's first external send (§11.2).
	SendApproved bool   `db:"send_approved"`
	CreatedBy    string `db:"created_by"`
}

// RoutineRun is one firing of a routine, or one tick that a later firing covered.
type RoutineRun struct {
	drel.Model[uuid.UUID] `db:"index=idx_routine_runs_routine[routine_id,due_at]"`
	RoutineID             uuid.UUID  `db:"routine_id,references=routines,on_delete=cascade"`
	EnvelopeID            *uuid.UUID `db:"envelope_id"`
	DueAt                 time.Time  `db:"due_at"`
	StartedAt             *time.Time `db:"started_at"`
	// Coalesced marks a tick that the firing of an earlier due time covered (§7.5).
	Coalesced bool `db:"coalesced"`
	// Missed marks a tick that came due while Fettle was not running.
	Missed bool   `db:"missed"`
	Source string `db:"source,check=source IN ('cron','webhook','manual')"`
}
