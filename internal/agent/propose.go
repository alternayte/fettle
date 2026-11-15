package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/chats"
	"github.com/alternayte/fettle/internal/envelopes"
	"github.com/alternayte/fettle/internal/models"
	"github.com/alternayte/fettle/internal/runs"
)

// ProposedRoutine is one routine of a proposed bot. It starts paused.
type ProposedRoutine struct {
	Name   string `json:"name"`
	Cron   string `json:"cron"`
	TZ     string `json:"tz,omitempty"`
	Prompt string `json:"prompt"`
}

// Proposal is a bot that the primary bot proposes (§9.8).
type Proposal struct {
	Name         string            `json:"name"`
	Job          string            `json:"job"`
	Instructions string            `json:"instructions"`
	Zone         string            `json:"zone,omitempty"`
	Grants       []string          `json:"grants,omitempty"`
	Skills       []string          `json:"skills,omitempty"`
	Routines     []ProposedRoutine `json:"routines,omitempty"`
	ModelRoles   map[string]string `json:"model_roles,omitempty"`
}

// ErrNotPrimary means a bot that is not primary tried to propose bots.
var ErrNotPrimary = errors.New("only the primary bot proposes bots")

const proposalSchema = `{"type":"object","properties":{
"name":{"type":"string"},"job":{"type":"string"},"instructions":{"type":"string"},
"zone":{"type":"string","description":"The zone name, for example personal or browsing."},
"grants":{"type":"array","items":{"type":"string"},"description":"Names of vault items or connector accounts the bot needs."},
"skills":{"type":"array","items":{"type":"string"}},
"routines":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"},"cron":{"type":"string"},"tz":{"type":"string"},"prompt":{"type":"string"}},"required":["name","cron","prompt"],"additionalProperties":false}},
"model_roles":{"type":"object","additionalProperties":{"type":"string"}}},
"required":["name","job","instructions"],"additionalProperties":false}`

func (a *Agent) registerPropose() {
	a.Tools.Add(Tool{
		Name:        "propose_bot",
		Description: "Propose a new bot to the user with a card. Nothing is created until the user presses Create. Its routines start paused.",
		Class:       envelopes.ClassAdmin,
		NoSubagent:  true,
		Schema:      schema(proposalSchema),
		Run: func(context.Context, *ToolEnv, json.RawMessage) (ToolOutput, error) {
			return ToolOutput{}, errors.New("propose_bot runs in the loop")
		},
	})
	a.Tools.Add(Tool{
		Name:        "propose_team",
		Description: "Propose up to 3 bots to the user in one card.",
		Class:       envelopes.ClassAdmin,
		NoSubagent:  true,
		Schema:      schema(`{"type":"object","properties":{"bots":{"type":"array","minItems":1,"maxItems":3,"items":` + proposalSchema + `}},"required":["bots"],"additionalProperties":false}`),
		Run: func(context.Context, *ToolEnv, json.RawMessage) (ToolOutput, error) {
			return ToolOutput{}, errors.New("propose_team runs in the loop")
		},
	})
}

// Validate checks a proposal before it reaches a card.
func (p *Proposal) Validate() error {
	p.Name, p.Job = strings.TrimSpace(p.Name), strings.TrimSpace(p.Job)
	if p.Name == "" || p.Job == "" || strings.TrimSpace(p.Instructions) == "" {
		return errors.New("a proposal needs a name, a job and instructions")
	}
	if len([]rune(p.Name)) > 40 {
		return errors.New("the name is longer than 40 characters")
	}
	for _, r := range p.Routines {
		if strings.TrimSpace(r.Name) == "" || strings.TrimSpace(r.Cron) == "" || strings.TrimSpace(r.Prompt) == "" {
			return errors.New("each routine needs a name, a cron schedule and a prompt")
		}
	}
	return nil
}

// propose posts a proposal card in the chat of the work. The run does not wait: the user
// decides in their own time (§9.8).
func (x *exec) propose(ctx context.Context, call models.ToolCall) error {
	a := x.a
	refuse := func(msg string) error {
		_, err := x.append(ctx, nil, runs.New{Type: runs.EvToolCall, Data: evTool{CallID: call.ID, Tool: call.Name, Args: call.Args, Error: msg}})
		return err
	}
	if !x.bot.IsPrimary {
		return refuse("Only the primary bot proposes bots. Ask the primary bot.")
	}
	var list []Proposal
	if call.Name == "propose_team" {
		var in struct {
			Bots []Proposal `json:"bots"`
		}
		if err := json.Unmarshal(call.Args, &in); err != nil || len(in.Bots) == 0 || len(in.Bots) > 3 {
			return refuse("Give 1 to 3 bots.")
		}
		list = in.Bots
	} else {
		var p Proposal
		if err := json.Unmarshal(call.Args, &p); err != nil {
			return refuse("The proposal does not match the schema.")
		}
		list = []Proposal{p}
	}
	for i := range list {
		if err := list[i].Validate(); err != nil {
			return refuse(err.Error())
		}
		if _, err := a.Bots.ByName(ctx, x.run.SpaceID, list[i].Name); err == nil {
			return refuse(fmt.Sprintf("A bot named %s exists.", list[i].Name))
		}
	}
	raw, _ := json.Marshal(list)
	title := "New bot: " + list[0].Name
	if len(list) > 1 {
		names := make([]string, len(list))
		for i, p := range list {
			names[i] = p.Name
		}
		title = "New team: " + strings.Join(names, ", ")
	}
	preview := map[string]string{}
	for _, p := range list {
		line := p.Job
		if p.Zone != "" {
			line += " · zone " + p.Zone
		}
		if len(p.Grants) > 0 {
			line += " · access " + strings.Join(p.Grants, ", ")
		}
		if len(p.Routines) > 0 {
			line += fmt.Sprintf(" · %d routines (paused)", len(p.Routines))
		}
		preview[p.Name] = line
	}
	chatID := x.env.ChatID
	if chatID == nil {
		c, err := a.Chats.DirectFor(ctx, x.run.SpaceID, x.run.BotID)
		if err != nil {
			return err
		}
		id := c.ID()
		chatID = &id
	}
	card := chats.Card{ID: uuid.NewString(), Kind: "proposal", Status: "pending", Title: title, Detail: "Create makes the bots with their routines paused. Access that a bot needs asks you with its own card.", Preview: preview,
		Data: map[string]string{"proposal": string(raw)}}
	msg := &chats.Message{SpaceID: x.run.SpaceID, ChatID: *chatID, AuthorKind: "bot", AuthorID: x.run.BotID.String(), Card: &card, EnvelopeID: ptr(x.env.ID())}
	err := a.E.WithTx(ctx, func(ctx context.Context) error {
		if err := a.Chats.Insert(ctx, msg, nil); err != nil {
			return err
		}
		// The card has no run: the run goes on, and the card stays for the user.
		if _, err := x.append(ctx, nil, runs.New{Type: runs.EvToolCall, Data: evTool{CallID: call.ID, Tool: call.Name, Class: envelopes.ClassAdmin, Args: call.Args, Result: "The proposal card is in the chat. Nothing exists until the user presses Create.", Origin: OriginTool}}); err != nil {
			return err
		}
		return a.Audit.Record(ctx, audit.Actor{Kind: "bot", ID: x.run.BotID.String()}, &x.run.SpaceID, "card.post", "card:"+card.ID, map[string]string{"kind": "proposal"})
	})
	if err != nil {
		return err
	}
	a.Chats.Publish(*chatID, "message", a.MessageView(ctx, msg))
	return nil
}

// applyProposal creates the bots of an allowed proposal card. value, when set, is the
// proposal list after the user edited it.
func (a *Agent) applyProposal(ctx context.Context, user audit.Actor, spaceID uuid.UUID, card chats.Card, value string) error {
	raw := card.Data["proposal"]
	if strings.TrimSpace(value) != "" {
		raw = value
	}
	var list []Proposal
	if err := json.Unmarshal([]byte(raw), &list); err != nil || len(list) == 0 || len(list) > 3 {
		return fmt.Errorf("%w: the proposal is not valid", ErrBadAction)
	}
	for i := range list {
		if err := list[i].Validate(); err != nil {
			return fmt.Errorf("%w: %v", ErrBadAction, err)
		}
	}
	if a.Hire == nil {
		return errors.New("this server cannot create bots from proposals")
	}
	return a.Hire(ctx, user, spaceID, list)
}
