package agent

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/chats"
	"github.com/alternayte/fettle/internal/runs"
)

// MessageView is a chat message as the API and the stream show it.
type MessageView struct {
	ID          string             `json:"id"`
	ChatID      string             `json:"chat_id"`
	AuthorKind  string             `json:"author_kind"`
	AuthorID    string             `json:"author_id"`
	Body        string             `json:"body"`
	CreatedAt   time.Time          `json:"created_at"`
	Streaming   bool               `json:"streaming,omitempty"`
	Card        *chats.Card        `json:"card,omitempty"`
	EnvelopeID  string             `json:"envelope_id,omitempty"`
	ReplyTo     string             `json:"reply_to,omitempty"`
	Attachments []chats.Attachment `json:"attachments"`
	Reactions   map[string]string  `json:"reactions"`
}

// RunLine is the compact line of an envelope in a chat (§6.2).
type RunLine struct {
	RunID      string  `json:"run_id"`
	EnvelopeID string  `json:"envelope_id"`
	BotID      string  `json:"bot_id"`
	Step       string  `json:"step"`
	Label      string  `json:"label"`
	Helpers    int     `json:"helpers"`
	DurationMS int64   `json:"duration_ms"`
	CostUSD    float64 `json:"cost_usd"`
	Tokens     int64   `json:"tokens"`
	State      string  `json:"state"`
	Verified   *bool   `json:"verified"`
	// Activity is the current step of a live run, or nil.
	Activity *Activity `json:"activity,omitempty"`
}

// MessageView converts a message.
func (a *Agent) MessageView(_ context.Context, m *chats.Message) MessageView {
	v := MessageView{ID: m.ID().String(), ChatID: m.ChatID.String(), AuthorKind: m.AuthorKind, AuthorID: m.AuthorID, Body: m.Body, CreatedAt: m.SentAt, Card: m.Card, Attachments: m.Attachments, Reactions: m.Reactions}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = a.now()
	}
	if m.EnvelopeID != nil {
		v.EnvelopeID = m.EnvelopeID.String()
	}
	if m.ReplyTo != nil {
		v.ReplyTo = m.ReplyTo.String()
	}
	if v.Attachments == nil {
		v.Attachments = []chats.Attachment{}
	}
	if v.Reactions == nil {
		v.Reactions = map[string]string{}
	}
	return v
}

func (a *Agent) streamingView(x *exec, id uuid.UUID) MessageView {
	return MessageView{ID: id.String(), ChatID: x.env.ChatID.String(), AuthorKind: "bot", AuthorID: x.bot.ID().String(), CreatedAt: a.now(), Streaming: true, EnvelopeID: x.env.ID().String(), Attachments: []chats.Attachment{}, Reactions: map[string]string{}}
}

func label(goal string) string {
	r := []rune(goal)
	for i, c := range r {
		if c == '\n' {
			r = r[:i]
			break
		}
	}
	if len(r) > 60 {
		return string(r[:57]) + "…"
	}
	return string(r)
}

// RunLineFor builds the run line of a run.
func (a *Agent) RunLineFor(ctx context.Context, r *runs.Run) (RunLine, error) {
	env, err := a.Envelopes.Get(ctx, r.SpaceID, r.EnvelopeID)
	if err != nil {
		return RunLine{}, err
	}
	step := "single"
	if env.Step != nil {
		step = *env.Step
	}
	var helpers int
	_ = a.E.QueryRow(ctx, "SELECT COUNT(*) FROM runs WHERE parent_run_id = $1 AND kind = 'subagent'", r.ID()).Scan(&helpers)
	var cost float64
	var tokens int64
	_ = a.E.QueryRow(ctx, "SELECT COALESCE(SUM(cost_usd),0), COALESCE(SUM(tokens_in + tokens_out),0) FROM runs WHERE envelope_id = $1 OR parent_run_id = $2", r.EnvelopeID, r.ID()).Scan(&cost, &tokens)
	var dur int64
	if r.StartedAt != nil {
		end := a.now()
		if r.FinishedAt != nil {
			end = *r.FinishedAt
		}
		dur = end.Sub(*r.StartedAt).Milliseconds()
	}
	var verified *bool
	var vdata string
	if err := a.E.QueryRow(ctx, "SELECT data FROM run_events WHERE run_id = $1 AND type = 'verify_result' ORDER BY seq DESC LIMIT 1", r.ID()).Scan(&vdata); err == nil {
		v := len(vdata) > 0 && strings.Contains(vdata, `"pass":true`)
		verified = &v
	}
	return RunLine{RunID: r.ID().String(), EnvelopeID: r.EnvelopeID.String(), BotID: r.BotID.String(), Step: step, Label: label(env.Goal), Helpers: helpers, DurationMS: dur, CostUSD: cost, Tokens: tokens, State: r.Status, Verified: verified, Activity: a.activityOf(r.ID())}, nil
}

// publishRun sends the run line to the run's chat and the bot status to the space.
func (a *Agent) publishRun(ctx context.Context, runID uuid.UUID) {
	r, err := a.Runs.Find(ctx, runID)
	if err != nil {
		return
	}
	line, err := a.RunLineFor(ctx, r)
	if err != nil {
		return
	}
	env, err := a.Envelopes.Get(ctx, r.SpaceID, r.EnvelopeID)
	if err == nil && env.ChatID != nil && r.Kind == "owner" {
		a.Chats.Publish(*env.ChatID, "run", line)
	}
	a.Chats.Hub.Publish(chats.SpaceTopic(r.SpaceID.String()), "run", line)
}
