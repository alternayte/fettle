package envelopes

import (
	"time"

	"github.com/alternayte/drel"
	"github.com/google/uuid"
)

// Requester names who asked for the work.
type Requester struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// Check is one acceptance check.
type Check struct {
	Kind string `json:"kind"`
	Spec string `json:"spec"`
}

// ContextRef points at context for the work.
type ContextRef struct {
	Type string `json:"type"`
	Ref  string `json:"ref"`
}

// Budget limits the work.
type Budget struct {
	USD           float64 `json:"usd"`
	Tokens        int64   `json:"tokens"`
	WallMS        int64   `json:"wall_ms"`
	HopsRemaining int     `json:"hops_remaining"`
	MaxSubagents  int     `json:"max_subagents"`
}

// Result is set when the envelope is done.
type Result struct {
	Summary   string   `json:"summary"`
	Artifacts []string `json:"artifacts"`
	Evidence  []string `json:"evidence"`
	OpenItems []string `json:"open_items"`
	NextOwner string   `json:"next_owner,omitempty"`
}

// Part is one part of a fanout or forked step (§9.3).
type Part struct {
	Goal       string `json:"goal"`
	Acceptance string `json:"acceptance"`
	// Needs is the highest class that the part needs (rule R1).
	Needs string `json:"needs"`
}

// Route is the router's decision for an envelope.
type Route struct {
	Step       string   `json:"step"`
	Parts      []Part   `json:"parts,omitempty"`
	Rules      []string `json:"rules"`
	OwnerBotID string   `json:"owner_bot_id,omitempty"`
}

// Decision holds what was decided about the work: the router's route and the skill that
// the work started with.
type Decision struct {
	Skill string `json:"skill,omitempty"`
	Route *Route `json:"route,omitempty"`
	// Brief is the context brief that the owner wrote for a sub-agent part (§9.4).
	Brief string `json:"brief,omitempty"`
}

// Envelope is one unit of work (Appendix C).
type Envelope struct {
	drel.Model[uuid.UUID] `db:"unique_index=uq_envelopes_idem[space_id,idempotency_key]"`
	ParentID              *uuid.UUID   `db:"parent_id"`
	RootID                uuid.UUID    `db:"root_id,index"`
	SpaceID               uuid.UUID    `db:"space_id,references=spaces,on_delete=cascade"`
	Kind                  string       `db:"kind,check=kind IN ('message','routine','webhook','handoff','part','verify')"`
	Origin                string       `db:"origin,check=origin IN ('user','routine','webhook','bot')"`
	Requester             Requester    `db:"requester"`
	OwnerBotID            *uuid.UUID   `db:"owner_bot_id"`
	ChatID                *uuid.UUID   `db:"chat_id"`
	MessageID             *uuid.UUID   `db:"message_id"`
	Goal                  string       `db:"goal"`
	Acceptance            []Check      `db:"acceptance"`
	ContextRefs           []ContextRef `db:"context_refs"`
	Capabilities          []string     `db:"capabilities"`
	Budget                Budget       `db:"budget"`
	Step                  *string      `db:"step"`
	IdempotencyKey        string       `db:"idempotency_key"`
	Deadline              *time.Time   `db:"deadline"`
	Status                string       `db:"status,check=status IN ('created','routed','running','verifying','done','failed','escalated','cancelled')"`
	Decision              Decision     `db:"decision"`
	Result                *Result      `db:"result"`
}
