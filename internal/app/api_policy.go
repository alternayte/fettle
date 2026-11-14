package app

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/policy"
)

// RuleView is one approval rule (§11.2).
type RuleView struct {
	ID               string    `json:"id"`
	Tool             string    `json:"tool"`
	Connector        string    `json:"connector"`
	SitePattern      string    `json:"site_pattern"`
	RecipientPattern string    `json:"recipient_pattern"`
	Effect           string    `json:"effect"`
	FromCard         bool      `json:"from_card"`
	CreatedAt        time.Time `json:"created_at"`
}

func ruleView(r *policy.Rule) RuleView {
	return RuleView{ID: r.ID().String(), Tool: r.Tool, Connector: r.Connector, SitePattern: r.SitePattern, RecipientPattern: r.RecipientPattern, Effect: r.Effect, FromCard: r.CreatedFromCard != "", CreatedAt: r.CreatedAt()}
}

func (a *App) registerPolicy(api huma.API) {
	type rulesOut struct {
		Body struct {
			Mode  string     `json:"mode"`
			Rules []RuleView `json:"rules"`
		}
	}
	huma.Register(api, op("list-policy", http.MethodGet, "/spaces/{space}/policy", "The approval mode and the rules", "policy"), func(ctx context.Context, in *SpacePath) (*rulesOut, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, true); err != nil {
			return nil, err
		}
		rules, err := a.Policy.Rules(ctx, in.Space)
		if err != nil {
			return nil, err
		}
		out := &rulesOut{}
		out.Body.Mode = a.Settings.String(ctx, &in.Space, "approval.mode")
		out.Body.Rules = []RuleView{}
		for _, r := range rules {
			out.Body.Rules = append(out.Body.Rules, ruleView(r))
		}
		return out, nil
	})

	huma.Register(api, op("set-approval-mode", http.MethodPut, "/spaces/{space}/approval-mode", "Ask to change the approval mode. It changes after a person allows the card.", "policy"), func(ctx context.Context, in *struct {
		SpacePath
		Body struct {
			Mode string `json:"mode" enum:"ask,auto,never"`
		}
	}) (*struct{ Body CardReply }, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		detail := map[string]string{
			policy.ModeAsk:   "Bots ask before each change outside the computer and each message they send.",
			policy.ModeAuto:  "Bots act without asking when the action clearly matches what you asked. Else they ask.",
			policy.ModeNever: "Bots act without asking. Hard asks still need a card.",
		}[in.Body.Mode]
		msg, err := a.Agent.PostChange(ctx, actorOf(p), in.Space, "approval.mode", "Change the approval mode", detail, map[string]string{"mode": in.Body.Mode}, in.Body.Mode)
		if err != nil {
			return nil, err
		}
		return &struct{ Body CardReply }{Body: CardReply{CardID: msg.Card.ID, ChatID: msg.ChatID.String(), MessageID: msg.ID().String()}}, nil
	})

	huma.Register(api, op("add-policy-rule", http.MethodPost, "/spaces/{space}/policy/rules", "Add an allow, ask or block rule", "policy"), func(ctx context.Context, in *struct {
		SpacePath
		Body struct {
			Tool             string `json:"tool,omitempty" maxLength:"200" doc:"A tool name, or * for every tool."`
			Connector        string `json:"connector,omitempty" maxLength:"200"`
			SitePattern      string `json:"site_pattern,omitempty" maxLength:"300"`
			RecipientPattern string `json:"recipient_pattern,omitempty" maxLength:"300"`
			Effect           string `json:"effect" enum:"allow,ask,block"`
		}
	}) (*struct{ Body RuleView }, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		r := &policy.Rule{SpaceID: in.Space, Tool: in.Body.Tool, Connector: in.Body.Connector, SitePattern: in.Body.SitePattern, RecipientPattern: in.Body.RecipientPattern, Effect: in.Body.Effect}
		if err := a.Policy.AddRule(ctx, actorOf(p), r); err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		return &struct{ Body RuleView }{Body: ruleView(r)}, nil
	})

	huma.Register(api, op("delete-policy-rule", http.MethodDelete, "/spaces/{space}/policy/rules/{rule}", "Delete a rule", "policy"), func(ctx context.Context, in *struct {
		SpacePath
		Rule uuid.UUID `path:"rule"`
	}) (*struct{}, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		if err := a.Policy.DeleteRule(ctx, actorOf(p), in.Space, in.Rule); err != nil {
			return nil, huma.Error404NotFound(err.Error())
		}
		return nil, nil
	})
}
