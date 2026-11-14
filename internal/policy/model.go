package policy

import (
	"github.com/alternayte/drel"
	"github.com/google/uuid"
)

// Rule allows, asks or blocks a tool, a connector tool, a site pattern or a recipient pattern (§11.2).
type Rule struct {
	drel.Model[uuid.UUID] `db:"table=policy_rules,index=idx_policy_rules_space[space_id]"`
	SpaceID               uuid.UUID `db:"space_id,references=spaces,on_delete=cascade"`
	Tool                  string    `db:"tool"`
	Connector             string    `db:"connector"`
	SitePattern           string    `db:"site_pattern"`
	RecipientPattern      string    `db:"recipient_pattern"`
	Effect                string    `db:"effect,check=effect IN ('allow','ask','block')"`
	CreatedFromCard       string    `db:"created_from_card"`
	CreatedBy             string    `db:"created_by"`
}

// Approval is one approval card of a run.
type Approval struct {
	drel.Model[uuid.UUID] `db:"index=idx_approvals_run[run_id]"`
	SpaceID               uuid.UUID  `db:"space_id,references=spaces,on_delete=cascade"`
	RunID                 uuid.UUID  `db:"run_id"`
	CallID                string     `db:"call_id"`
	CardID                string     `db:"card_id,unique"`
	Tool                  string     `db:"tool"`
	Class                 string     `db:"class"`
	ArgsPreview           string     `db:"args_preview"`
	Status                string     `db:"status,check=status IN ('pending','allowed','denied','expired')"`
	HardAsk               bool       `db:"hard_ask"`
	DecidedBy             string     `db:"decided_by"`
	RuleID                *uuid.UUID `db:"rule_id"`
	Reason                string     `db:"reason"`
}
