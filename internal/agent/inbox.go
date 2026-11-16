package agent

import (
	"context"
	"encoding/json"

	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/alternayte/drel"

	"github.com/alternayte/fettle/internal/policy"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/chats"
	"github.com/alternayte/fettle/internal/envelopes"
	"github.com/alternayte/fettle/internal/runs"
)

// UserCapabilities are the classes of an envelope from a user message. spend is never included (SI-08).
var UserCapabilities = []string{envelopes.ClassRead, envelopes.ClassWriteLocal, envelopes.ClassWriteExternal, envelopes.ClassSend, envelopes.ClassAdmin}

// PostInput is a user message to a chat.
type PostInput struct {
	Body        string
	ReplyTo     *uuid.UUID
	Attachments []chats.Attachment
	// ConfirmEveryone sends an @everyone message after the user saw the cost hint.
	ConfirmEveryone bool
}

// PostResult tells what a user message started.
type PostResult struct {
	Message    *chats.Message
	EnvelopeID *uuid.UUID
	RunID      *uuid.UUID
	// RunIDs are all runs that the message started or steered (a group can wake several).
	RunIDs  []uuid.UUID
	Steered bool
	// Status is true when a bot answered a status question from its waiting run's events.
	Status bool
}

func (a *Agent) defaultBudget(ctx context.Context, spaceID uuid.UUID) envelopes.Budget {
	return envelopes.Budget{
		USD:           a.Settings.Float(ctx, &spaceID, "budget.envelope_usd"),
		Tokens:        int64(a.Settings.Int(ctx, &spaceID, "budget.envelope_tokens")),
		WallMS:        int64(a.Settings.Int(ctx, &spaceID, "budget.envelope_wall_ms")),
		HopsRemaining: a.Settings.Int(ctx, &spaceID, "budget.hops"),
		MaxSubagents:  a.Settings.Int(ctx, &spaceID, "budget.max_subagents"),
	}
}

// PostMessage stores a user message. When the addressed bot already runs in this chat,
// the message becomes a steer event (§7.4); otherwise the inbox creates an envelope and
// a run in the same transaction (§9.1).
func (a *Agent) PostMessage(ctx context.Context, user audit.Actor, spaceID, chatID uuid.UUID, in PostInput) (*PostResult, error) {
	body := strings.TrimSpace(in.Body)
	if body == "" && len(in.Attachments) == 0 {
		return nil, chats.ErrEmptyMessage
	}
	chat, err := a.Chats.Get(ctx, spaceID, chatID)
	if err != nil {
		return nil, err
	}
	members, err := a.Chats.Members(ctx, chatID)
	if err != nil {
		return nil, err
	}
	if len(members) == 0 {
		return nil, errors.New("this chat has no bots")
	}
	owners, err := a.addressees(ctx, spaceID, chat.Kind, members, body, in.ConfirmEveryone)
	if err != nil {
		return nil, err
	}
	res := &PostResult{}
	msg := &chats.Message{SpaceID: spaceID, ChatID: chat.ID(), AuthorKind: "user", AuthorID: user.ID, Body: body, ReplyTo: in.ReplyTo, Attachments: in.Attachments}
	active := make([]*runs.Run, len(owners))
	for i, o := range owners {
		if active[i], err = a.Runs.Active(ctx, o.ID(), chatID); err != nil {
			return nil, err
		}
	}
	var started []uuid.UUID
	var replies []*chats.Message
	err = a.E.WithTx(ctx, func(ctx context.Context) error {
		first := owners[0].ID()
		if err := a.Chats.Insert(ctx, msg, &first); err != nil {
			return err
		}
		for i, o := range owners {
			owner := o.ID()
			if r := active[i]; r != nil && r.Status == runs.StatusWaiting && statusWaits[r.WaitReason] && isStatusQuestion(body) {
				// A status question during a wait gets a direct reply from the run's events
				// and leaves the run as it is (§7.4).
				text, err := a.statusReply(ctx, r)
				if err != nil {
					return err
				}
				reply := &chats.Message{SpaceID: spaceID, ChatID: chat.ID(), AuthorKind: "bot", AuthorID: owner.String(), Body: text, ReplyTo: ptr(msg.ID())}
				if err := a.Chats.Insert(ctx, reply, &owner); err != nil {
					return err
				}
				rid := r.ID()
				if res.RunID == nil {
					res.RunID, res.EnvelopeID = &rid, &r.EnvelopeID
				}
				res.Status = true
				replies = append(replies, reply)
				continue
			}
			if r := active[i]; r != nil {
				res.Steered = true
				rid := r.ID()
				if res.RunID == nil {
					res.RunID, res.EnvelopeID = &rid, &r.EnvelopeID
				}
				res.RunIDs = append(res.RunIDs, rid)
				if _, err := a.Journal.Append(ctx, rid, runs.NoFence, nil, runs.New{Type: runs.EvSteer, Data: evSteer{Text: body, MessageID: msg.ID().String()}}); err != nil {
					return err
				}
				if r.Status == runs.StatusWaiting && r.WaitReason != "card" {
					if err := a.Runs.Wake(ctx, rid, QueueFor("user")); err != nil {
						return err
					}
				}
				started = append(started, rid)
				continue
			}
			env := &envelopes.Envelope{SpaceID: spaceID, Kind: "message", Origin: "user", Requester: envelopes.Requester{Type: "user", ID: user.ID}, OwnerBotID: &owner, ChatID: ptr(chat.ID()), MessageID: ptr(msg.ID()), Goal: body, Capabilities: UserCapabilities, Budget: a.defaultBudget(ctx, spaceID)}
			// "/name ..." starts the work with that skill loaded (§13.1).
			if name := slashSkill(body); name != "" && a.SkillIndex != nil {
				for _, sk := range a.SkillIndex(ctx, spaceID, owner) {
					if sk.Name == name {
						env.Decision.Skill = name
					}
				}
			}
			if env.Goal == "" {
				env.Goal = "Look at the attached files."
			}
			if len(owners) > 1 {
				env.IdempotencyKey = msg.ID().String() + ":" + owner.String()
			}
			if err := a.Envelopes.Create(ctx, env, nil); err != nil {
				return err
			}
			if msg.EnvelopeID == nil {
				msg.EnvelopeID = ptr(env.ID())
			}
			r := &runs.Run{SpaceID: spaceID, EnvelopeID: env.ID(), BotID: owner, Kind: "owner"}
			if err := a.Runs.Create(ctx, r, QueueFor("user")); err != nil {
				return err
			}
			if res.RunID == nil {
				res.EnvelopeID, res.RunID = ptr(env.ID()), ptr(r.ID())
			}
			res.RunIDs = append(res.RunIDs, r.ID())
			started = append(started, r.ID())
		}
		if msg.EnvelopeID != nil {
			_, err := drel.MustFromContext(ctx).Exec(ctx, "UPDATE messages SET envelope_id = $1 WHERE id = $2", *msg.EnvelopeID, msg.ID())
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	res.Message = msg
	a.Chats.Publish(chatID, "message", a.MessageView(ctx, msg))
	for _, m := range replies {
		a.Chats.Publish(chatID, "message", a.MessageView(ctx, m))
	}
	for _, id := range started {
		a.publishRun(ctx, id)
	}
	if !res.Steered && !res.Status && res.EnvelopeID != nil && a.Learning != nil {
		envID := *res.EnvelopeID
		a.bg.Add(1)
		go func() {
			defer a.bg.Done()
			a.checkCorrection(context.WithoutCancel(ctx), spaceID, msg, envID)
		}()
	}
	return res, nil
}

// Errors of card answers.
var ErrBadAction = errors.New("this action is not valid for this card")

// AnswerCard records a decision on a card, appends card_answered to its run and wakes
// the run in the same transaction (§7.2). Every decision has an audit row (REQ-CORE-010).
func (a *Agent) AnswerCard(ctx context.Context, user audit.Actor, spaceID uuid.UUID, cardID, action, value string) (*chats.Message, error) {
	msg, err := a.Chats.FindCard(ctx, spaceID, cardID)
	if err != nil {
		return nil, err
	}
	card := *msg.Card
	if card.Status != "pending" {
		return nil, chats.ErrCardAnswered
	}
	valid := map[string][]string{
		"turn_limit":   {"continue", "stop"},
		"budget":       {"add_budget", "stop"},
		"uncertain":    {"done", "not_done", "stop"},
		"approval":     {"allow", "always", "deny"},
		"question":     {"answer"},
		"takeover":     {"done"},
		"change":       {"allow", "deny"},
		"warning":      {"dismiss"},
		"verification": {"accept", "retry", "stop"},
		"check":        {"pass", "fail"},
		"proposal":     {"create", "decline"},
		"connect":      {"done", "skip"},
		"patch":        {"apply", "decline"},
	}[card.Kind]
	ok := false
	for _, v := range valid {
		if v == action {
			ok = true
		}
	}
	if !ok {
		return nil, ErrBadAction
	}
	switch action {
	case "deny":
		card.Status = "denied"
	case "allow", "always":
		card.Status = "allowed"
	default:
		card.Status = "answered"
	}
	card.Answer = cardAnswerText(card.Kind, action, value)
	var runID uuid.UUID
	if card.RunID != "" {
		runID, _ = uuid.Parse(card.RunID)
	}
	var out, tasks *chats.Message
	err = a.E.WithTx(ctx, func(ctx context.Context) error {
		m, err := a.Chats.SetCard(ctx, msg.ID(), &card)
		if err != nil {
			return err
		}
		out = m
		if err := a.Audit.Record(ctx, user, &spaceID, "card.decide", "card:"+cardID, map[string]string{"kind": card.Kind, "action": action, "value": value}); err != nil {
			return err
		}
		if card.Kind == "proposal" && action == "create" {
			if tasks, err = a.applyProposal(ctx, user, spaceID, msg, card, value); err != nil {
				return err
			}
		}
		if card.Kind == "patch" && action == "apply" {
			if a.ApplyPatch == nil {
				return errors.New("this server cannot apply skill patches")
			}
			if err := a.ApplyPatch(ctx, user, spaceID, card); err != nil {
				return err
			}
		}
		if card.Kind == "change" && action == "allow" {
			if err := a.applyChange(ctx, user, spaceID, card); err != nil {
				return err
			}
		}
		if card.Kind == "question" && card.Data["teach_id"] != "" {
			if a.TeachAnswer == nil {
				return errors.New("this server cannot record teach answers")
			}
			if err := a.TeachAnswer(ctx, user, spaceID, card); err != nil {
				return err
			}
		}
		if runID == uuid.Nil {
			return nil
		}
		r, err := a.Runs.Find(ctx, runID)
		if err != nil {
			return err
		}
		evs := []runs.New{{Type: runs.EvCardAnswered, Data: evCard{CardID: cardID, Kind: card.Kind, Action: action, Value: value, By: user.ID}}}
		if action == "stop" {
			evs = append(evs, runs.New{Type: runs.EvCancel, Data: evCancel{By: user.ID}})
		}
		if card.Kind == "approval" {
			if action == "always" && card.HardAsk {
				return fmt.Errorf("%w: this action always needs approval", ErrBadAction)
			}
			decision := "allowed"
			if action == "deny" {
				decision = "denied"
			}
			ev := evApproval{CallID: card.Data["call_id"], CardID: cardID, Tool: card.Tool, Decision: decision, Class: card.Data["class"]}
			if decision == "allowed" && value != "" {
				// The person changed the draft before Allow (§14.1, draft_edited).
				var ed map[string]string
				if json.Unmarshal([]byte(value), &ed) != nil {
					return fmt.Errorf("%w: the edited draft is not a map of text fields", ErrBadAction)
				}
				ev.Edited = map[string]string{}
				for k, v := range ed {
					if old, ok := card.Preview[k]; ok && old != v {
						ev.Edited[k] = v
					}
				}
			}
			evs = append(evs, runs.New{Type: runs.EvApprovalDecided, Data: ev})
			tx := drel.MustFromContext(ctx)
			if _, err := tx.Exec(ctx, "UPDATE approvals SET status = $1, decided_by = $2 WHERE card_id = $3", decision, user.ID, cardID); err != nil {
				return err
			}
			if id, err := uuid.Parse(card.Data["routine_id"]); err == nil && decision == "allowed" && a.RoutineSendApproved != nil {
				if err := a.RoutineSendApproved(ctx, id); err != nil {
					return err
				}
			}
			if action == "always" {
				if err := a.Policy.AddRule(ctx, user, &policy.Rule{SpaceID: spaceID, Tool: card.Tool, Effect: "allow", CreatedFromCard: cardID}); err != nil {
					return err
				}
			}
		}
		if card.Kind == "budget" && action == "add_budget" {
			usd, err := strconv.ParseFloat(value, 64)
			if err != nil || usd <= 0 || usd > 1000 {
				return fmt.Errorf("%w: give an amount from 0 to 1000", ErrBadAction)
			}
			if err := a.Envelopes.AddBudget(ctx, r.EnvelopeID, usd); err != nil {
				return err
			}
			if err := a.Envelopes.SetStatus(ctx, r.EnvelopeID, "running", nil); err != nil {
				return err
			}
		}
		if _, err := a.Journal.Append(ctx, runID, runs.NoFence, nil, evs...); err != nil {
			return err
		}
		env, err := a.Envelopes.Get(ctx, r.SpaceID, r.EnvelopeID)
		if err != nil {
			return err
		}
		return a.Runs.Wake(ctx, runID, QueueFor(env.Origin))
	})
	if err != nil {
		return nil, err
	}
	a.Chats.Publish(out.ChatID, "message", a.MessageView(ctx, out))
	a.cardSignal(ctx, user, spaceID, msg, card, action, value)
	if tasks != nil {
		a.Chats.Publish(tasks.ChatID, "message", a.MessageView(ctx, tasks))
	}
	if runID != uuid.Nil {
		a.publishRun(ctx, runID)
	}
	return out, nil
}

func cardAnswerText(kind, action, value string) string {
	switch {
	case kind == "budget" && action == "add_budget":
		return "Added $" + value
	case action == "answer":
		return value
	case action == "create":
		return "Created"
	case action == "decline":
		return "Declined"
	case action == "apply":
		return "Applied"
	case action == "accept":
		return "Accepted anyway"
	case action == "retry":
		return "Retry"
	case action == "pass":
		return "Passes"
	case action == "fail":
		return "Fails"
	case action == "continue":
		return "Continue"
	case action == "stop":
		return "Stop"
	case kind == "connect" && action == "done":
		return "Connected"
	case action == "skip":
		return "Skipped"
	case kind == "takeover" && action == "done":
		return "Control returned"
	case action == "done":
		return "It happened"
	case action == "not_done":
		return "It did not happen"
	}
	return action
}

// Stop cancels a run and its sub-agent runs (§7.4).
func (a *Agent) Stop(ctx context.Context, user audit.Actor, spaceID, runID uuid.UUID) error {
	r, err := a.Runs.Get(ctx, spaceID, runID)
	if err != nil {
		return err
	}
	ids := []uuid.UUID{r.ID()}
	children, err := a.Runs.Children(ctx, r.ID())
	if err != nil {
		return err
	}
	for _, c := range children {
		ids = append(ids, c.ID())
	}
	err = a.E.WithTx(ctx, func(ctx context.Context) error {
		for _, id := range ids {
			cr, err := a.Runs.Find(ctx, id)
			if err != nil {
				return err
			}
			if runs.Terminal(cr.Status) {
				continue
			}
			if _, err := a.Journal.Append(ctx, id, runs.NoFence, nil, runs.New{Type: runs.EvCancel, Data: evCancel{By: user.ID}}); err != nil {
				return err
			}
			if err := a.Runs.Wake(ctx, id, jobsQueueForRun(cr)); err != nil {
				return err
			}
		}
		return a.Audit.Record(ctx, user, &spaceID, "run.stop", "run:"+runID.String(), nil)
	})
	if err == nil {
		a.publishRun(ctx, runID)
	}
	return err
}

func jobsQueueForRun(r *runs.Run) string {
	if r.Kind == "owner" {
		return QueueFor("user")
	}
	return QueueFor("bot")
}

// Submit creates an envelope from a source other than a chat message (a routine firing, a
// webhook, a handoff) and its owner run in one transaction (§9.1). The envelope's origin
// picks the queue: user work is interactive, the rest is background.
func (a *Agent) Submit(ctx context.Context, env *envelopes.Envelope) (*runs.Run, error) {
	if env.OwnerBotID == nil {
		return nil, errors.New("the envelope has no owner bot")
	}
	if env.Budget == (envelopes.Budget{}) {
		env.Budget = a.defaultBudget(ctx, env.SpaceID)
	}
	var r *runs.Run
	err := a.E.WithTx(ctx, func(ctx context.Context) error {
		if err := a.Envelopes.Create(ctx, env, nil); err != nil {
			return err
		}
		r = &runs.Run{SpaceID: env.SpaceID, EnvelopeID: env.ID(), BotID: *env.OwnerBotID, Kind: "owner"}
		return a.Runs.Create(ctx, r, QueueFor(env.Origin))
	})
	if err != nil {
		return nil, err
	}
	a.publishRun(ctx, r.ID())
	return r, nil
}

// slashSkill returns the skill name of a message that starts with /name.
func slashSkill(body string) string {
	if !strings.HasPrefix(body, "/") {
		return ""
	}
	name := strings.TrimPrefix(strings.Fields(body)[0], "/")
	return strings.ToLower(name)
}
