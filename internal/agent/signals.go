package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/chats"
	"github.com/alternayte/fettle/internal/envelopes"
	"github.com/alternayte/fettle/internal/learning"
	"github.com/alternayte/fettle/internal/runs"
)

// Signal records a steering signal (§14.1). A failure is logged: the human action it
// comes from has happened already.
func (a *Agent) Signal(ctx context.Context, in learning.Input) {
	if a.Learning == nil {
		return
	}
	if _, err := a.Learning.Record(context.WithoutCancel(ctx), in); err != nil {
		a.Log.Warn("record a steering signal", "type", in.Type, "error", err)
	}
}

// OwnerRunOf returns the owner run of an envelope, or nil.
func (a *Agent) OwnerRunOf(ctx context.Context, envelopeID uuid.UUID) *uuid.UUID {
	var id uuid.UUID
	if err := a.E.QueryRow(ctx, "SELECT id FROM runs WHERE envelope_id = $1 AND kind = 'owner' ORDER BY created_at DESC LIMIT 1", envelopeID).Scan(&id); err != nil {
		return nil
	}
	return &id
}

// cardSignal records the signal that a card answer is, if any (Appendix F).
func (a *Agent) cardSignal(ctx context.Context, user audit.Actor, spaceID uuid.UUID, msg *chats.Message, card chats.Card, action, value string) {
	typ := ""
	switch {
	case card.Kind == "approval" && action == "deny":
		typ = learning.ApprovalDenied
	case card.Kind == "approval" && (action == "allow" || action == "always") && draftChanged(card, value):
		typ, value = learning.DraftEdited, ""
	case card.Kind == "verification" && action == "accept":
		typ = learning.EscalationAcceptedAnyway
	case card.Kind == "uncertain" && action == "not_done":
		typ = learning.UncertainConfirmedWrong
	case (card.Kind == "proposal" || card.Kind == "patch") && action == "decline":
		typ = learning.CardDeclined
	default:
		return
	}
	in := learning.Input{SpaceID: spaceID, Type: typ, Ref: "card:" + card.ID, UserID: user.ID, Detail: card.Title}
	if value != "" {
		in.Detail += ": " + value
	}
	if id, err := uuid.Parse(card.RunID); err == nil {
		in.RunID = &id
	} else if msg.EnvelopeID != nil {
		in.RunID = a.OwnerRunOf(ctx, *msg.EnvelopeID)
	}
	if in.RunID == nil {
		if id, err := uuid.Parse(msg.AuthorID); err == nil && msg.AuthorKind == "bot" {
			in.BotID = &id
		}
	}
	a.Signal(ctx, in)
}

// ErrNotFinished means the run still works, so it cannot run again.
var ErrNotFinished = errors.New("the run has not ended; stop it or wait for it")

// Rerun runs the work of an owner run again as a new envelope in the same chat. The
// request is a steering signal on the old run (Appendix F, rerun).
func (a *Agent) Rerun(ctx context.Context, user audit.Actor, spaceID, runID uuid.UUID) (*runs.Run, error) {
	r, err := a.Runs.Get(ctx, spaceID, runID)
	if err != nil {
		return nil, err
	}
	if r.Kind != "owner" {
		return nil, fmt.Errorf("%w: only the owner run of a piece of work runs again", ErrBadAction)
	}
	if !runs.Terminal(r.Status) && r.Status != runs.StatusEscalated {
		return nil, ErrNotFinished
	}
	old, err := a.Envelopes.Get(ctx, spaceID, r.EnvelopeID)
	if err != nil {
		return nil, err
	}
	owner := r.BotID
	env := &envelopes.Envelope{SpaceID: spaceID, Kind: "message", Origin: "user", Requester: envelopes.Requester{Type: "user", ID: user.ID},
		OwnerBotID: &owner, ChatID: old.ChatID, Goal: old.Goal, Acceptance: old.Acceptance, ContextRefs: old.ContextRefs,
		Capabilities: old.Capabilities, Decision: envelopes.Decision{Skill: old.Decision.Skill}}
	if len(env.Capabilities) == 0 {
		env.Capabilities = UserCapabilities
	}
	nr, err := a.Submit(ctx, env)
	if err != nil {
		return nil, err
	}
	if err := a.Audit.Record(ctx, user, &spaceID, "run.rerun", "run:"+runID.String(), map[string]string{"new_run": nr.ID().String()}); err != nil {
		return nil, err
	}
	a.Signal(ctx, learning.Input{SpaceID: spaceID, RunID: &runID, Type: learning.Rerun, Ref: "rerun:" + nr.ID().String(), UserID: user.ID, Detail: truncate(old.Goal, 300)})
	return nr, nil
}

// draftChanged reports whether an approval answer changed a field that the card showed.
func draftChanged(card chats.Card, value string) bool {
	var ed map[string]string
	if value == "" || json.Unmarshal([]byte(value), &ed) != nil {
		return false
	}
	for k, v := range ed {
		if old, ok := card.Preview[k]; ok && old != v {
			return true
		}
	}
	return false
}
