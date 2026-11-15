package app

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/agent"
	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/envelopes"
	"github.com/alternayte/fettle/internal/settings"
)

// InterviewSkill is the skill of the Chief of Staff template that runs the interview.
const InterviewSkill = "onboarding-interview"

func init() {
	settings.Register(settings.Def{Key: "onboarding.started_at", Scope: "space", Default: "", Doc: "When the onboarding interview started (set by Fettle)."})
}

// OnboardingView tells the wizard whether the interview started and in which chat.
type OnboardingView struct {
	Started bool   `json:"started"`
	ChatID  string `json:"chat_id,omitempty"`
	RunID   string `json:"run_id,omitempty"`
}

// startOnboarding starts the onboarding interview of the primary bot once per space
// (§15.3 step 5, §15.4, D-154). A second call returns the chat and starts nothing.
func (a *App) startOnboarding(ctx context.Context, user audit.Actor, sid uuid.UUID) (OnboardingView, error) {
	var out OnboardingView
	b, err := a.Bots.Primary(ctx, sid)
	if err != nil {
		return out, err
	}
	chat, err := a.Chats.DirectFor(ctx, sid, b.ID())
	if err != nil {
		return out, err
	}
	out.ChatID = chat.ID().String()
	if a.Settings.String(ctx, &sid, "onboarding.started_at") != "" {
		out.Started = true
		return out, nil
	}
	if _, err := a.Settings.Set(ctx, user, &sid, "onboarding.started_at", time.Now().UTC().Format(time.RFC3339)); err != nil {
		return out, err
	}
	owner, chatID := b.ID(), chat.ID()
	env := &envelopes.Envelope{SpaceID: sid, Kind: "message", Origin: "user", Requester: envelopes.Requester{Type: "user", ID: user.ID},
		OwnerBotID: &owner, ChatID: &chatID, Goal: "Start the onboarding interview. Greet the user and ask the first question.",
		Capabilities: agent.UserCapabilities, IdempotencyKey: "onboarding:" + sid.String()}
	if list, err := a.Skills.ForBot(ctx, sid, owner); err == nil {
		for _, sk := range list {
			if sk.Name == InterviewSkill {
				env.Decision.Skill = InterviewSkill
			}
		}
	}
	r, err := a.Agent.Submit(ctx, env)
	if err != nil {
		return out, err
	}
	out.Started, out.RunID = true, r.ID().String()
	return out, nil
}

func (a *App) registerOnboarding(api huma.API) {
	huma.Register(api, op("start-onboarding", http.MethodPost, "/spaces/{space}/onboarding/start", "Start the onboarding interview of the primary bot, once per space", "bots"), func(ctx context.Context, in *struct{ SpacePath }) (*struct{ Body OnboardingView }, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		v, err := a.startOnboarding(ctx, actorOf(p), in.Space)
		if err != nil {
			return nil, apiError(err)
		}
		return &struct{ Body OnboardingView }{Body: v}, nil
	})
}
