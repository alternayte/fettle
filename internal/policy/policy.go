// Package policy decides whether an action runs, asks with a card or is blocked (§11).
package policy

import (
	"context"
	"errors"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/alternayte/drel"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/envelopes"
)

// Approval modes (§11.2).
const (
	ModeAsk   = "ask"
	ModeAuto  = "auto"
	ModeNever = "never"
)

// Effects of a decision.
const (
	Run   = "run"
	Card  = "card"
	Block = "block"
)

// Request describes one action that a run wants to take.
type Request struct {
	Tool      string
	Connector string
	Class     string
	Site      string // URL or host the action touches, if any
	Recipient string // recipient address or handle, if any
	HardAsk   bool   // §11.2 hard asks: a card in every mode, without "Always allow"
	// ReducedIntent is true for runs without a user message at the root (routines, webhooks).
	ReducedIntent bool
	// ReadData is true after the run read web, email or connector content.
	ReadData bool
	// MemoryWrite marks remember and forget.
	MemoryWrite bool
}

// Decision is the result of Decide.
type Decision struct {
	Effect string
	// AlwaysAllowed tells whether the card may offer "Always allow".
	AlwaysAllowed bool
	// NeedsIntentCheck is true when Auto mode must ask the verifier role.
	NeedsIntentCheck bool
	RuleID           *uuid.UUID
	Reason           string
}

// ErrSpend is the result for any spend action in v1 (SI-08).
var ErrSpend = errors.New("payments and purchases are blocked in this version")

func ruleMatches(r *Rule, req Request) bool {
	if r.Tool != "" && r.Tool != "*" && r.Tool != req.Tool {
		return false
	}
	if r.Connector != "" && r.Connector != req.Connector {
		return false
	}
	if r.SitePattern != "" && !globMatch(r.SitePattern, hostOf(req.Site)) {
		return false
	}
	if r.RecipientPattern != "" && !globMatch(r.RecipientPattern, strings.ToLower(req.Recipient)) {
		return false
	}
	return true
}

func hostOf(site string) string {
	s := strings.ToLower(strings.TrimSpace(site))
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, ":"); i >= 0 && !strings.Contains(s[i:], "]") {
		s = s[:i]
	}
	return s
}

func globMatch(pattern, s string) bool {
	ok, err := path.Match(strings.ToLower(pattern), s)
	return err == nil && ok
}

// Decide applies §11.2 and §11.3 in order: spend is blocked; hard asks always card; block
// rules win; ask rules win over allow; then the mode and the intent rules decide.
func Decide(mode string, rules []*Rule, req Request) Decision {
	if req.Class == envelopes.ClassSpend {
		return Decision{Effect: Block, Reason: ErrSpend.Error()}
	}
	if req.HardAsk {
		return Decision{Effect: Card, Reason: "This action always needs your approval."}
	}
	var allow, ask, block *Rule
	for _, r := range rules {
		if !ruleMatches(r, req) {
			continue
		}
		switch r.Effect {
		case "block":
			block = r
		case "ask":
			ask = r
		case "allow":
			allow = r
		}
	}
	if block != nil {
		id := block.ID()
		return Decision{Effect: Block, RuleID: &id, Reason: "A rule blocks this action."}
	}
	sensitive := req.Class == envelopes.ClassWriteExternal || req.Class == envelopes.ClassSend
	// After the run read untrusted content, memory writes and admin actions need a card (§11.3).
	if req.ReadData && (req.MemoryWrite || req.Class == envelopes.ClassAdmin) {
		return Decision{Effect: Card, AlwaysAllowed: false, Reason: "The run read outside content, so this change needs your approval."}
	}
	if ask != nil && (sensitive || req.Class == envelopes.ClassAdmin) {
		id := ask.ID()
		return Decision{Effect: Card, AlwaysAllowed: true, RuleID: &id, Reason: "A rule asks for approval."}
	}
	if allow != nil && sensitive {
		id := allow.ID()
		return Decision{Effect: Run, RuleID: &id, Reason: "A rule allows this action."}
	}
	if req.Class == envelopes.ClassRead || req.Class == envelopes.ClassWriteLocal {
		return Decision{Effect: Run}
	}
	if req.Class == envelopes.ClassAdmin {
		if mode == ModeNever && allow != nil {
			id := allow.ID()
			return Decision{Effect: Run, RuleID: &id}
		}
		return Decision{Effect: Card, AlwaysAllowed: true, Reason: "Changes to bots, routines or settings need your approval."}
	}
	// write_external and send.
	if req.ReducedIntent {
		return Decision{Effect: Card, AlwaysAllowed: true, Reason: "No person started this work, so this action needs your approval."}
	}
	switch mode {
	case ModeNever:
		return Decision{Effect: Run}
	case ModeAuto:
		return Decision{Effect: Card, AlwaysAllowed: true, NeedsIntentCheck: true, Reason: "The action does not clearly match what you asked."}
	default:
		return Decision{Effect: Card, AlwaysAllowed: true, Reason: "This action changes something outside the computer."}
	}
}

// Service stores rules and the approval mode.
type Service struct {
	e     *drel.Engine
	audit *audit.Log
}

// New returns the policy service.
func New(e *drel.Engine, a *audit.Log) *Service { return &Service{e: e, audit: a} }

// Rules returns the rules of a space.
func (s *Service) Rules(ctx context.Context, spaceID uuid.UUID) ([]*Rule, error) {
	return drel.NewRepository(s.e, RuleMeta).Where(Rules.SpaceID.Eq(spaceID)).OrderBy(Rules.CreatedAt.Asc()).All(ctx)
}

// AddRule stores a rule in the transaction in ctx.
func (s *Service) AddRule(ctx context.Context, actor audit.Actor, r *Rule) error {
	if !slices.Contains([]string{"allow", "ask", "block"}, r.Effect) {
		return errors.New("effect must be allow, ask or block")
	}
	if r.Tool == "" && r.Connector == "" && r.SitePattern == "" && r.RecipientPattern == "" {
		return errors.New("a rule needs a tool, a connector, a site pattern or a recipient pattern")
	}
	for _, p := range []string{r.SitePattern, r.RecipientPattern} {
		if _, err := path.Match(p, "x"); err != nil {
			return errors.New("a pattern is not valid")
		}
	}
	r.CreatedBy = actor.ID
	return s.e.WithTx(ctx, func(ctx context.Context) error {
		drel.Repo(drel.MustFromContext(ctx), RuleMeta).Add(r)
		sp := r.SpaceID
		return s.audit.Record(ctx, actor, &sp, "policy.rule_add", "policy_rule", map[string]string{"tool": r.Tool, "connector": r.Connector, "site": r.SitePattern, "recipient": r.RecipientPattern, "effect": r.Effect, "card": r.CreatedFromCard})
	})
}

// DeleteRule removes a rule.
func (s *Service) DeleteRule(ctx context.Context, actor audit.Actor, spaceID, id uuid.UUID) error {
	return s.e.WithTx(ctx, func(ctx context.Context) error {
		n, err := drel.MustFromContext(ctx).Exec(ctx, "DELETE FROM policy_rules WHERE id = $1 AND space_id = $2", id, spaceID)
		if err != nil {
			return err
		}
		if n == 0 {
			return errors.New("rule not found")
		}
		return s.audit.Record(ctx, actor, &spaceID, "policy.rule_delete", "policy_rule:"+id.String(), nil)
	})
}

// Send words in accessible names and form purposes (§10.7).
var sendWords = regexp.MustCompile(`(?i)\b(send|submit|post|publish|pay|buy|delete|confirm|reply|purchase|order|checkout|remove)\b`)

var sendOnly = regexp.MustCompile(`(?i)\b(send|post|publish|reply)\b`)

// ClassifyBrowserAct returns the class of a browser action (§10.7): clicks on buttons and
// links whose name or form purpose says send, submit, post, publish, pay, buy, delete or
// confirm are send or write_external; other form submissions are write_external; the rest
// is read.
func ClassifyBrowserAct(action, role, name, formPurpose string, submitsForm bool) string {
	switch action {
	case "scroll", "hover":
		return envelopes.ClassRead
	}
	text := name + " " + formPurpose
	if action == "click" || action == "press" {
		if role == "button" || role == "link" || role == "menuitem" || submitsForm {
			if regexp.MustCompile(`(?i)\b(pay|buy|purchase|checkout)\b`).MatchString(text) {
				return envelopes.ClassSpend
			}
			if sendOnly.MatchString(text) {
				return envelopes.ClassSend
			}
			if sendWords.MatchString(text) {
				return envelopes.ClassWriteExternal
			}
			if submitsForm {
				return envelopes.ClassWriteExternal
			}
		}
		return envelopes.ClassRead
	}
	// type, select and check change only the page until a submit.
	return envelopes.ClassRead
}
