package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"unicode"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/bots"
	"github.com/alternayte/fettle/internal/envelopes"
	"github.com/alternayte/fettle/internal/models"
	"github.com/alternayte/fettle/internal/runs"
)

type (
	evHandoffSent struct {
		CallID     string   `json:"call_id"`
		EnvelopeID string   `json:"envelope_id"`
		RunID      string   `json:"run_id"`
		BotID      string   `json:"bot_id"`
		BotName    string   `json:"bot_name"`
		Wait       bool     `json:"wait"`
		Files      []string `json:"files,omitempty"`
	}
	evHandoffResult struct {
		CallID     string `json:"call_id"`
		EnvelopeID string `json:"envelope_id"`
		BotName    string `json:"bot_name"`
		Status     string `json:"status"`
		Summary    string `json:"summary"`
	}
	handoffArgs struct {
		Bot         string                 `json:"bot"`
		Goal        string                 `json:"goal"`
		Acceptance  string                 `json:"acceptance"`
		ContextRefs []envelopes.ContextRef `json:"context_refs"`
		BudgetShare float64                `json:"budget_share"`
		Wait        *bool                  `json:"wait"`
	}
)

// maxSameGoalHandoffs is how many handoffs of one goal a tree takes; the next is refused
// (loop guard, §9.7).
const maxSameGoalHandoffs = 3

func (a *Agent) registerHandoff() {
	a.Tools.Add(Tool{
		Name:        "handoff",
		Description: "Send part of the work to another bot whose job fits it. With wait=true (the default) your work waits for its result, which becomes this tool's result. With wait=false the result arrives later as a message. context_refs can name messages (by ID) and files in shared/.",
		Class:       envelopes.ClassWriteLocal,
		NoSubagent:  true,
		Schema: schema(`{"type":"object","properties":{
"bot":{"type":"string","description":"The name of the bot."},
"goal":{"type":"string"},
"acceptance":{"type":"string","description":"When the work is done, in one sentence."},
"context_refs":{"type":"array","items":{"type":"object","properties":{"type":{"type":"string","enum":["message","file","artifact","run"]},"ref":{"type":"string"}},"required":["type","ref"],"additionalProperties":false}},
"budget_share":{"type":"number","description":"Share of your remaining budget, from 0.05 to 1. Default 0.5."},
"wait":{"type":"boolean"}},
"required":["bot","goal"],"additionalProperties":false}`),
		// The run loop handles handoff, because wait=true parks the run.
		Run: func(context.Context, *ToolEnv, json.RawMessage) (ToolOutput, error) {
			return ToolOutput{}, errors.New("handoff runs in the loop")
		},
	})
}

// fingerprint normalizes a goal for the loop guard: its words in order of first use,
// lower case, without punctuation.
func fingerprint(goal string) string {
	words := strings.FieldsFunc(strings.ToLower(goal), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	return strings.Join(words, " ")
}

// handoffResult turns a finished handoff into the tool result of its call.
func (x *exec) handoffDone(ctx context.Context, call models.ToolCall, res evHandoffResult) error {
	text := fmt.Sprintf("%s finished the work with status %s.\nResult:\n%s", res.BotName, res.Status, res.Summary)
	_, err := x.append(ctx, nil,
		runs.New{Type: runs.EvToolResult, Data: evTool{CallID: call.ID, Tool: call.Name, Class: envelopes.ClassWriteLocal, Result: truncate(text, 16000), Origin: OriginBot}})
	return err
}

// handoff creates the child envelope and its run (§9.7). It runs after the policy
// check of runTool, between the intent and the result events.
func (x *exec) handoff(ctx context.Context, st *state, call models.ToolCall) error {
	a := x.a
	if sent, ok := st.handoffs[call.ID]; ok {
		if res, done := st.handoffResults[call.ID]; done {
			return x.handoffDone(ctx, call, res)
		}
		if sent.Wait {
			return x.wait(ctx, "handoff")
		}
		_, err := x.append(ctx, nil, runs.New{Type: runs.EvToolResult, Data: evTool{CallID: call.ID, Tool: call.Name, Class: envelopes.ClassWriteLocal, Result: "Handed off to " + sent.BotName + ". The result arrives later as a message.", Origin: OriginTool}})
		return err
	}
	refuse := func(msg string) error {
		_, err := x.append(ctx, nil, runs.New{Type: runs.EvToolResult, Data: evTool{CallID: call.ID, Tool: call.Name, Class: envelopes.ClassWriteLocal, Error: msg}})
		return err
	}
	var in handoffArgs
	if err := json.Unmarshal(call.Args, &in); err != nil || strings.TrimSpace(in.Bot) == "" || strings.TrimSpace(in.Goal) == "" {
		return refuse("Give the bot and the goal.")
	}
	target, err := x.findBot(ctx, in.Bot)
	if err != nil {
		return refuse(err.Error())
	}
	if target.ID() == x.run.BotID {
		return refuse("You cannot hand off to yourself.")
	}
	if x.env.Budget.HopsRemaining <= 0 {
		return refuse("No hops are left in this work, so it cannot pass to another bot. Do it yourself or ask the user.")
	}
	fp := fingerprint(in.Goal)
	rows, err := a.E.Query(ctx, "SELECT goal FROM envelopes WHERE root_id = $1 AND kind = 'handoff'", x.env.RootID)
	if err != nil {
		return err
	}
	same := 0
	for rows.Next() {
		var g string
		if rows.Scan(&g) == nil && fingerprint(g) == fp {
			same++
		}
	}
	rows.Close()
	if same >= maxSameGoalHandoffs {
		return refuse("This goal was handed off too often in this work (loop guard). Do it yourself or ask the user.")
	}
	share := in.BudgetShare
	if share <= 0 {
		share = 0.5
	}
	share = min(max(share, 0.05), 1)
	wait := in.Wait == nil || *in.Wait
	left := max(x.env.Budget.USD-x.envelopeSpent(ctx), 0)
	check := envelopes.DefaultCheck
	if s := strings.TrimSpace(in.Acceptance); s != "" {
		check = envelopes.Check{Kind: "llm", Spec: s}
	}
	var refs []envelopes.ContextRef
	var files []string
	for _, r := range in.ContextRefs {
		switch r.Type {
		case "file":
			p := path.Clean("/" + strings.TrimPrefix(strings.TrimSpace(r.Ref), "/home/fettle/"))[1:]
			if !strings.HasPrefix(p, "shared/") {
				return refuse("File references must be in shared/.")
			}
			files = append(files, p)
		case "message", "artifact", "run":
			refs = append(refs, r)
		}
	}
	ev := evHandoffSent{CallID: call.ID, BotID: target.ID().String(), BotName: target.Name, Wait: wait}
	child := &envelopes.Envelope{
		ParentID: ptr(x.env.ID()), RootID: x.env.RootID, SpaceID: x.run.SpaceID, Kind: "handoff", Origin: x.env.Origin,
		Requester: envelopes.Requester{Type: "bot", ID: x.run.BotID.String()}, OwnerBotID: ptr(target.ID()), ChatID: x.env.ChatID,
		Goal: strings.TrimSpace(in.Goal), Acceptance: []envelopes.Check{check}, Capabilities: x.env.Capabilities,
		Budget: envelopes.Budget{USD: left * share, Tokens: int64(float64(x.env.Budget.Tokens) * share), WallMS: x.env.Budget.WallMS, HopsRemaining: x.env.Budget.HopsRemaining - 1, MaxSubagents: x.env.Budget.MaxSubagents},
		IdempotencyKey: fmt.Sprintf("%s:handoff:%s", x.run.ID(), call.ID),
		Decision:       envelopes.Decision{FromRun: x.run.ID().String(), FromCall: call.ID, Wait: wait},
	}
	// Files go to the target zone's shared/inbox/<envelope>/ when the zones differ (§9.7).
	child.SetID(uuid.Must(uuid.NewV7()))
	if len(files) > 0 {
		dest := "shared/inbox/" + child.ID().String()
		copied, note, err := x.copyFiles(ctx, target, files, dest)
		if err != nil {
			return refuse("The files could not be copied: " + err.Error())
		}
		for _, f := range copied {
			refs = append(refs, envelopes.ContextRef{Type: "file", Ref: f})
		}
		ev.Files = copied
		if note != "" {
			child.Goal += "\n\n" + note
		}
	}
	child.ContextRefs = refs
	err = a.E.WithTx(ctx, func(ctx context.Context) error {
		parent := &envelopes.Remainder{Budget: envelopes.Budget{USD: left, Tokens: x.env.Budget.Tokens, WallMS: x.env.Budget.WallMS, HopsRemaining: x.env.Budget.HopsRemaining, MaxSubagents: x.env.Budget.MaxSubagents}, Capabilities: x.env.Capabilities, SpaceID: x.run.SpaceID}
		if err := a.Envelopes.Create(ctx, child, parent); err != nil {
			return err
		}
		r := &runs.Run{SpaceID: x.run.SpaceID, EnvelopeID: child.ID(), BotID: target.ID(), Kind: "owner"}
		if err := a.Runs.Create(ctx, r, x.queue); err != nil {
			return err
		}
		ev.EnvelopeID, ev.RunID = child.ID().String(), r.ID().String()
		_, err := x.append(ctx, nil, runs.New{Type: runs.EvHandoffSent, Data: ev})
		return err
	})
	if err != nil {
		var ve *envelopes.ValidationError
		if errors.As(err, &ve) {
			return refuse(ve.Error())
		}
		return err
	}
	a.publishRun(ctx, x.run.ID())
	if wait {
		return x.wait(ctx, "handoff")
	}
	_, err = x.append(ctx, nil, runs.New{Type: runs.EvToolResult, Data: evTool{CallID: call.ID, Tool: call.Name, Class: envelopes.ClassWriteLocal, Result: "Handed off to " + target.Name + ". The result arrives later as a message.", Origin: OriginTool}})
	return err
}

// copyFiles copies shared files to the target bot's zone, when the zones differ and the
// sender's zone allows it. Inside one zone, the target reads the same shared folder.
func (x *exec) copyFiles(ctx context.Context, target *bots.Bot, files []string, dest string) ([]string, string, error) {
	a := x.a
	if x.bot.ZoneID == nil || target.ZoneID == nil || *x.bot.ZoneID == *target.ZoneID {
		return files, "", nil
	}
	var blocked bool
	_ = a.E.QueryRow(ctx, "SELECT share_blocked FROM zones WHERE id = $1", *x.bot.ZoneID).Scan(&blocked)
	if blocked {
		return nil, "The files of the sender's zone cannot be shared, so no files came with this work.", nil
	}
	if a.CopyShared == nil {
		return nil, "", errors.New("this server cannot copy files between zones")
	}
	out := make([]string, 0, len(files))
	for _, f := range files {
		to := dest + "/" + path.Base(f)
		if err := a.CopyShared(ctx, *x.bot.ZoneID, *target.ZoneID, f, to); err != nil {
			return nil, "", err
		}
		out = append(out, to)
	}
	return out, "", nil
}

// findBot finds an active bot of the space by name or ID.
func (x *exec) findBot(ctx context.Context, name string) (*bots.Bot, error) {
	list, err := x.a.Bots.List(ctx, x.run.SpaceID, false)
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(strings.TrimPrefix(name, "@"))
	var names []string
	for _, b := range list {
		if strings.EqualFold(b.Name, name) || b.ID().String() == name {
			return b, nil
		}
		names = append(names, b.Name)
	}
	slices.Sort(names)
	return nil, fmt.Errorf("no bot %q; the bots are: %s", name, strings.Join(names, ", "))
}

// tellSender reports a finished handoff to the run that sent it, in the finish
// transaction: the tool result with wait=true, else a steer event (§9.7).
func (x *exec) tellSender(ctx context.Context, status, summary string) error {
	a := x.a
	d := x.env.Decision
	if x.env.Kind != "handoff" || d.FromRun == "" {
		return nil
	}
	from, err := uuid.Parse(d.FromRun)
	if err != nil {
		return nil
	}
	sender, err := a.Runs.Find(ctx, from)
	if err != nil || runs.Terminal(sender.Status) {
		return nil
	}
	res := evHandoffResult{CallID: d.FromCall, EnvelopeID: x.env.ID().String(), BotName: x.bot.Name, Status: status, Summary: truncate(summary, 8000)}
	if d.Wait {
		if _, err := a.Journal.Append(ctx, from, runs.NoFence, nil, runs.New{Type: runs.EvHandoffResult, Data: res}); err != nil {
			return err
		}
		return a.Runs.Wake(ctx, from, jobsQueueForRun(sender))
	}
	text := fmt.Sprintf("Result of the work that you handed off to %s (%s):\n%s", x.bot.Name, status, res.Summary)
	if _, err := a.Journal.Append(ctx, from, runs.NoFence, nil, runs.New{Type: runs.EvHandoffResult, Data: res}, runs.New{Type: runs.EvSteer, Data: evSteer{Text: text, Origin: OriginBot}}); err != nil {
		return err
	}
	if sender.Status == runs.StatusWaiting && sender.WaitReason != "card" {
		return a.Runs.Wake(ctx, from, jobsQueueForRun(sender))
	}
	return nil
}
