package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alternayte/drel"
	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/bots"
	"github.com/alternayte/fettle/internal/chats"
	"github.com/alternayte/fettle/internal/envelopes"
	"github.com/alternayte/fettle/internal/jobs"
	"github.com/alternayte/fettle/internal/models"
	"github.com/alternayte/fettle/internal/runs"
	"github.com/alternayte/fettle/internal/usage"
)

// Worker executes run jobs.
type Worker struct {
	river.WorkerDefaults[runs.Args]
	A *Agent
}

// Timeout lets a run job run as long as it needs; the fence and the heartbeat protect it.
func (w *Worker) Timeout(*river.Job[runs.Args]) time.Duration { return -1 }

// exec is one job attempt of one run.
type exec struct {
	a     *Agent
	run   *runs.Run
	env   *envelopes.Envelope
	bot   *bots.Bot
	fence int64
	jobID int64
	queue string
}

// Work runs steps until the run finishes or waits (§7.2).
func (w *Worker) Work(ctx context.Context, job *river.Job[runs.Args]) error {
	a := w.A
	run, err := a.Runs.Claim(ctx, job.Args.RunID, job.ID)
	if err != nil {
		a.Log.Error("run claim failed; River retries it", "run", job.Args.RunID, "error", err)
		return err
	}
	if run == nil {
		return nil
	}
	env, err := a.Envelopes.Get(ctx, run.SpaceID, run.EnvelopeID)
	if err != nil {
		return err
	}
	bot, err := a.Bots.Get(ctx, run.SpaceID, run.BotID)
	if err != nil {
		return err
	}
	x := &exec{a: a, run: run, env: env, bot: bot, fence: run.Fence, jobID: job.ID, queue: job.Queue}
	a.publishRun(ctx, run.ID())
	err = x.loop(ctx)
	if errors.Is(err, runs.ErrStaleFence) {
		a.Log.Info("run attempt stopped: another attempt owns the run", "run", run.ID())
		return nil
	}
	if errors.Is(err, errWaitRace) {
		return nil // a wake inserted the next job
	}
	a.publishRun(context.WithoutCancel(ctx), run.ID())
	if err != nil && ctx.Err() == nil {
		a.Log.Error("run attempt failed; River retries it", "run", run.ID(), "error", err)
	}
	return err
}

var errWaitRace = errors.New("the run was woken while this attempt was ending")

func (x *exec) append(ctx context.Context, up *runs.Update, evs ...runs.New) ([]*runs.RunEvent, error) {
	return x.a.Journal.Append(ctx, x.run.ID(), x.fence, up, evs...)
}

func strp(s string) *string { return &s }

func (x *exec) loop(ctx context.Context) error {
	a := x.a
	lastBeat := time.Now()
	for i := 0; ; i++ {
		if time.Since(lastBeat) > 15*time.Second && a.Runs.Queue != nil {
			_ = a.Runs.Queue.Heartbeat(ctx, x.jobID)
			lastBeat = time.Now()
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		evs, err := a.Journal.Load(ctx, x.run.ID())
		if err != nil {
			return err
		}
		st := rebuild(evs)
		if st.finished {
			return nil
		}
		if !st.started {
			step := "single"
			if x.env.Step != nil {
				step = *x.env.Step
			}
			if _, err := x.append(ctx, nil, runs.New{Type: runs.EvRunStarted, Data: evRunStarted{Step: step, Goal: x.env.Goal}}); err != nil {
				return err
			}
			if err := a.Envelopes.SetStatus(ctx, x.env.ID(), "running", nil); err != nil {
				return err
			}
			continue
		}
		if st.cancelled {
			return x.finish(ctx, st, runs.StatusCancelled, "")
		}
		if st.openCard != nil {
			return x.wait(ctx, "card")
		}
		if len(st.uncertain) > 0 {
			if err := x.resolveUncertain(ctx, st); err != nil {
				return err
			}
			continue
		}
		if len(st.pending) > 0 {
			if err := x.runTool(ctx, st, st.pending[0]); err != nil {
				return err
			}
			continue
		}
		if !st.needsModel() {
			return x.finish(ctx, st, runs.StatusDone, st.finalText)
		}
		maxTurns := a.Settings.Int(ctx, &x.run.SpaceID, "agent.max_turns")
		if maxTurns <= 0 {
			maxTurns = 200
		}
		if st.modelTurns >= maxTurns*(1+st.extraTurns) {
			return x.postCard(ctx, chats.Card{Kind: "turn_limit", Title: "Turn limit reached", Detail: fmt.Sprintf("%s used %d model turns and stopped. Continue for %d more turns, or stop.", x.bot.Name, st.modelTurns, maxTurns)}, runs.StatusEscalated)
		}
		if err := x.modelTurn(ctx, st); err != nil {
			return err
		}
	}
}

// wait ends the job and leaves the run waiting. A wake that came in between makes the job snooze.
func (x *exec) wait(ctx context.Context, reason string) error {
	return x.park(ctx, runs.StatusWaiting, reason)
}

// park moves a running run to waiting or escalated. When a wake came in between, the
// status is no longer running, and the job snoozes to pick up the event (§7.2).
func (x *exec) park(ctx context.Context, status, reason string) error {
	return x.a.E.WithTx(ctx, func(ctx context.Context) error {
		n, err := drel.MustFromContext(ctx).Exec(ctx, "UPDATE runs SET status = $1, wait_reason = $2 WHERE id = $3 AND fence = $4 AND status = $5",
			status, reason, x.run.ID(), x.fence, runs.StatusRunning)
		if err != nil {
			return err
		}
		if n == 0 {
			return errWaitRace
		}
		return nil
	})
}

// finish appends run_finished and ends the envelope.
func (x *exec) finish(ctx context.Context, st *state, status, summary string) error {
	a := x.a
	envStatus := map[string]string{runs.StatusDone: "done", runs.StatusFailed: "failed", runs.StatusCancelled: "cancelled", runs.StatusEscalated: "escalated"}[status]
	err := a.E.WithTx(ctx, func(ctx context.Context) error {
		if _, err := x.append(ctx, &runs.Update{Status: strp(status), Finished: status != runs.StatusEscalated}, runs.New{Type: runs.EvRunFinished, Data: evFinished{Status: status, Summary: truncate(summary, 2000)}}); err != nil {
			return err
		}
		var res *envelopes.Result
		if status == runs.StatusDone {
			res = &envelopes.Result{Summary: truncate(summary, 8000), Artifacts: []string{}, Evidence: []string{}, OpenItems: []string{}}
		}
		return a.Envelopes.SetStatus(ctx, x.env.ID(), envStatus, res)
	})
	if err != nil {
		return err
	}
	if a.Metrics != nil {
		a.Metrics.RunFinished(status, st.step)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// context builds the model input for this run.
func (x *exec) context(ctx context.Context, st *state, window int) (Context, error) {
	a := x.a
	sp, err := a.Spaces.Get(ctx, x.run.SpaceID)
	if err != nil {
		return Context{}, err
	}
	shared, err := a.Bots.GetMemory(ctx, x.run.SpaceID, nil)
	if err != nil {
		return Context{}, err
	}
	botID := x.bot.ID()
	mem, err := a.Bots.GetMemory(ctx, x.run.SpaceID, &botID)
	if err != nil {
		return Context{}, err
	}
	ow, err := a.Bots.OpenWorkList(ctx, botID)
	if err != nil {
		return Context{}, err
	}
	var conv []ChatLine
	if x.env.ChatID != nil {
		msgs, err := a.Chats.Recent(ctx, *x.env.ChatID, 60)
		if err != nil {
			return Context{}, err
		}
		names := map[string]string{}
		envSent := x.env.CreatedAt()
		for _, m := range msgs {
			if x.env.MessageID != nil && m.ID() == *x.env.MessageID {
				envSent = m.SentAt
			}
		}
		for _, m := range msgs {
			if x.env.MessageID != nil && m.AuthorKind == "user" && m.ID() != *x.env.MessageID && m.SentAt.After(envSent) {
				continue // later user messages arrive as steer events
			}
			if m.EnvelopeID != nil && *m.EnvelopeID == x.env.ID() && m.AuthorKind == "bot" {
				continue // this run's own replies are in the run events
			}
			if m.Card != nil && m.Body == "" {
				continue
			}
			cl := ChatLine{AuthorKind: m.AuthorKind, Text: m.Body, At: m.SentAt}
			if m.AuthorKind == "bot" {
				cl.Self = m.AuthorID == botID.String()
				if n, ok := names[m.AuthorID]; ok {
					cl.AuthorName = n
				} else if id, err := uuid.Parse(m.AuthorID); err == nil {
					if b, err := a.Bots.Get(ctx, x.run.SpaceID, id); err == nil {
						cl.AuthorName = b.Name
						names[m.AuthorID] = b.Name
					}
				}
			}
			if m.AuthorKind == "system" {
				continue
			}
			conv = append(conv, cl)
		}
	}
	accept := make([]string, len(x.env.Acceptance))
	for i, c := range x.env.Acceptance {
		accept[i] = c.Kind + ": " + c.Spec
	}
	spent := x.envelopeSpent(ctx)
	origin := OriginUser
	if x.env.Origin != "user" {
		origin = map[string]string{"routine": OriginRoutine, "webhook": OriginRoutine, "bot": OriginBot}[x.env.Origin]
	}
	in := Input{
		BotName: x.bot.Name, BotJob: x.bot.Job, Instructions: x.bot.Instructions,
		SharedMemory: shared.Content, BotMemory: mem.Content, OpenWork: ow,
		Now: a.now(), TimeZone: sp.TimeZone,
		Goal: x.env.Goal, GoalOrigin: origin, Acceptance: accept, BudgetLeftUSD: x.env.Budget.USD - spent, Capabilities: x.env.Capabilities,
		Conversation: conv, Turns: st.turns, Steers: st.steers, ContextWindow: window,
	}
	if x.env.Deadline != nil && a.now().Sub(*x.env.Deadline) >= 15*time.Minute {
		in.DueAt = x.env.Deadline
	}
	return Assemble(in), nil
}

func (x *exec) envelopeSpent(ctx context.Context) float64 {
	var v float64
	_ = x.a.E.QueryRow(ctx, "SELECT COALESCE(SUM(cost_usd), 0) FROM runs WHERE envelope_id = $1", x.env.ID()).Scan(&v)
	return v
}

func toolDefs(ts []Tool) []models.ToolDef {
	out := make([]models.ToolDef, len(ts))
	for i, t := range ts {
		out[i] = models.ToolDef{Name: t.Name, Description: t.Description, Schema: t.Schema}
	}
	return out
}

// modelTurn runs one model call with streaming and appends it to the journal.
func (x *exec) modelTurn(ctx context.Context, st *state) error {
	a := x.a
	targets, err := a.Models.Resolve(ctx, x.run.SpaceID, &x.run.BotID, models.RoleOwner)
	if err != nil {
		return x.fail(ctx, st, "No model is ready for this bot. Add a model key in Settings, then send the message again.", err)
	}
	window := targets[0].Target.ContextWindow
	if window <= 0 {
		window = 128000
	}
	c, err := x.context(ctx, st, window)
	if err != nil {
		return err
	}
	if float64(c.Tokens) > 0.7*float64(window) && len(st.turns) >= 3 {
		if done, err := x.compact(ctx, st, window); err != nil || done {
			return err
		}
	}
	tools := a.Tools.For(x.env.Capabilities, x.run.Kind == "subagent")
	req := models.Request{System: c.System, Messages: c.Messages, Tools: toolDefs(tools), MaxTokens: 4096}

	// Budget check before the call (§8.4).
	est := models.Estimate(c.Tokens, int64(req.MaxTokens), targets[0].Target)
	if halt, err := x.checkBudget(ctx, est); halt || err != nil {
		return err
	}

	msgID := uuid.NewSHA1(x.run.ID(), []byte(fmt.Sprintf("turn-%d", st.modelTurns+1)))
	streamStarted := false
	var chatID uuid.UUID
	if x.env.ChatID != nil {
		chatID = *x.env.ChatID
	}
	onDelta := func(d string) {
		if x.env.ChatID == nil {
			return
		}
		if !streamStarted {
			streamStarted = true
			a.Chats.Publish(chatID, "message", a.streamingView(x, msgID))
		}
		a.Chats.Publish(chatID, "delta", map[string]string{"id": msgID.String(), "text": d})
	}
	var switches []models.Switch
	start := time.Now()
	resp, idx, err := models.CallWithFallback(ctx, a.HTTP, targets, req, onDelta, func(s models.Switch) { switches = append(switches, s) })
	for _, s := range switches {
		if _, aerr := x.append(ctx, nil, runs.New{Type: runs.EvModelSwitch, Data: s}); aerr != nil {
			return aerr
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if streamStarted {
			a.Chats.Publish(chatID, "message_removed", map[string]string{"id": msgID.String()})
		}
		return x.fail(ctx, st, "The model call failed: "+a.Vault.Redactor.Global(err.Error())+". Check the model provider in Settings, then try again.", err)
	}
	t := targets[idx].Target
	cost, reported := models.Cost(resp.Usage, t)
	text := a.Vault.Redactor.String(ctx, x.run.SpaceID, resp.Text)
	data := evModelTurn{Text: text, ToolCalls: resp.ToolCalls, Usage: resp.Usage, CostUSD: cost, Reported: reported, Model: resp.Model, Role: models.RoleOwner, Request: fmt.Sprintf("%d messages, %d tools, about %d tokens", len(req.Messages), len(req.Tools), c.Tokens), Duration: time.Since(start).Milliseconds()}
	var msg *chats.Message
	err = a.E.WithTx(ctx, func(ctx context.Context) error {
		if strings.TrimSpace(text) != "" && x.env.ChatID != nil {
			msg = &chats.Message{SpaceID: x.run.SpaceID, ChatID: chatID, AuthorKind: "bot", AuthorID: x.bot.ID().String(), Body: text, EnvelopeID: ptr(x.env.ID())}
			msg.SetID(msgID)
			botID := x.bot.ID()
			if err := a.Chats.Insert(ctx, msg, &botID); err != nil {
				return err
			}
			data.MessageID = msgID.String()
		}
		if _, err := x.append(ctx, &runs.Update{TokensIn: resp.Usage.In, TokensOut: resp.Usage.Out, CostUSD: cost}, runs.New{Type: runs.EvModelTurn, Data: data}); err != nil {
			return err
		}
		botID, runID := x.bot.ID(), x.run.ID()
		step := st.step
		return a.Usage.Record(ctx, &usage.UsageEvent{SpaceID: x.run.SpaceID, Kind: "model", BotID: &botID, RunID: &runID, Step: step, Role: models.RoleOwner, ModelName: resp.Model, TokensIn: resp.Usage.In, TokensOut: resp.Usage.Out, CacheRead: resp.Usage.CacheRead, Quantity: resp.Usage.In + resp.Usage.Out, Unit: "tokens", CostUSD: cost, Reported: reported})
	})
	if err != nil {
		return err
	}
	if a.Metrics != nil {
		a.Metrics.ModelCall(models.RoleOwner, resp.Model, resp.Usage.In, resp.Usage.Out, cost)
	}
	if x.env.ChatID != nil {
		if msg != nil {
			a.Chats.Publish(chatID, "message", a.MessageView(ctx, msg))
		} else if streamStarted {
			a.Chats.Publish(chatID, "message_removed", map[string]string{"id": msgID.String()})
		}
	}
	a.publishRun(ctx, x.run.ID())
	return nil
}

func ptr[T any](v T) *T { return &v }

// fail ends the run with an error message in the chat.
func (x *exec) fail(ctx context.Context, st *state, userText string, cause error) error {
	a := x.a
	a.Log.Warn("run failed", "run", x.run.ID(), "error", a.Vault.Redactor.Global(cause.Error()))
	if x.env.ChatID != nil {
		m := &chats.Message{SpaceID: x.run.SpaceID, ChatID: *x.env.ChatID, AuthorKind: "system", AuthorID: "fettle", Body: userText, EnvelopeID: ptr(x.env.ID())}
		if err := a.Chats.Insert(ctx, m, nil); err != nil {
			return err
		}
		a.Chats.Publish(*x.env.ChatID, "message", a.MessageView(ctx, m))
	}
	if _, err := x.append(ctx, nil, runs.New{Type: runs.EvToolResult, Data: evTool{CallID: "error", Tool: "model", Error: truncate(cause.Error(), 500)}}); err != nil && !errors.Is(err, runs.ErrStaleFence) {
		return err
	}
	return x.finish(ctx, st, runs.StatusFailed, userText)
}

// runTool executes one pending tool call with intent and result events for side effects (§7.3).
func (x *exec) runTool(ctx context.Context, st *state, call models.ToolCall) error {
	a := x.a
	tool, ok := a.Tools.Get(call.Name)
	allowed := false
	if ok {
		for _, t := range a.Tools.For(x.env.Capabilities, x.run.Kind == "subagent") {
			if t.Name == call.Name {
				allowed = true
			}
		}
	}
	if !ok || !allowed {
		_, err := x.append(ctx, nil, runs.New{Type: runs.EvToolCall, Data: evTool{CallID: call.ID, Tool: call.Name, Args: call.Args, Error: "This tool is not available in this run."}})
		return err
	}
	env := &ToolEnv{SpaceID: x.run.SpaceID, BotID: x.run.BotID, RunID: x.run.ID(), ChatID: x.env.ChatID, Actor: audit.Actor{Kind: "bot", ID: x.run.BotID.String()}, Origin: x.env.Origin, ReadData: st.readData}
	class := tool.classOf(ctx, env, call.Args)
	side := class != envelopes.ClassRead
	if side || tool.MemoryWrite {
		switch st.approvals[call.ID] {
		case "allowed":
		case "denied":
			_, err := x.append(ctx, nil, runs.New{Type: runs.EvToolCall, Data: evTool{CallID: call.ID, Tool: call.Name, Class: class, Error: "The user denied this action. Do not try it again in another way. Ask the user what to do instead."}})
			return err
		case "requested":
			return x.wait(ctx, "card")
		default:
			proceed, err := x.authorize(ctx, st, tool, class, call)
			if err != nil || !proceed {
				return err
			}
		}
	}
	idem := fmt.Sprintf("%s:%d", x.run.ID(), st.lastSeq+1)
	if side {
		if _, err := x.append(ctx, nil, runs.New{Type: runs.EvToolIntent, Data: evTool{CallID: call.ID, Tool: call.Name, Class: class, Args: call.Args, Idem: idem}}); err != nil {
			return err
		}
	}
	start := time.Now()
	out, err := tool.Run(ctx, env, call.Args)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	res := evTool{CallID: call.ID, Tool: call.Name, Class: class, Result: truncate(out.Text, 64*1024), Origin: out.Origin, MS: time.Since(start).Milliseconds()}
	if res.Origin == "" {
		res.Origin = OriginTool
	}
	if err != nil {
		res.Error = a.Vault.Redactor.String(ctx, x.run.SpaceID, err.Error())
	}
	typ := runs.EvToolCall
	if side {
		typ = runs.EvToolResult
		res.Args = nil
	} else {
		res.Args = call.Args
	}
	_, aerr := x.append(ctx, nil, runs.New{Type: typ, Data: res})
	return aerr
}

// resolveUncertain marks intents without results as uncertain and never repeats them (SI-10).
func (x *exec) resolveUncertain(ctx context.Context, st *state) error {
	for _, it := range st.uncertain {
		if _, err := x.append(ctx, nil, runs.New{Type: runs.EvToolUncertain, Data: evTool{CallID: it.CallID, Tool: it.Tool, Class: it.Class, Idem: it.Idem}}); err != nil {
			return err
		}
		if it.Class == envelopes.ClassSend || it.Class == envelopes.ClassWriteExternal {
			return x.postCard(ctx, chats.Card{Kind: "uncertain", Title: "Uncertain action", Tool: it.Tool, Detail: "Fettle stopped while this action ran. It does not know if the action happened. Check, then tell the bot."}, runs.StatusWaiting)
		}
	}
	return nil
}

// postCard posts a card from this run and makes the run wait or escalate.
func (x *exec) postCard(ctx context.Context, card chats.Card, status string) error {
	a := x.a
	card.ID = uuid.NewString()
	card.Status = "pending"
	card.RunID = x.run.ID().String()
	var msg *chats.Message
	err := a.E.WithTx(ctx, func(ctx context.Context) error {
		if x.env.ChatID != nil {
			msg = &chats.Message{SpaceID: x.run.SpaceID, ChatID: *x.env.ChatID, AuthorKind: "bot", AuthorID: x.bot.ID().String(), Card: &card, EnvelopeID: ptr(x.env.ID())}
			if err := a.Chats.Insert(ctx, msg, nil); err != nil {
				return err
			}
		}
		mid := ""
		if msg != nil {
			mid = msg.ID().String()
		}
		if _, err := x.append(ctx, nil, runs.New{Type: runs.EvCardPosted, Data: evCard{CardID: card.ID, Kind: card.Kind, MessageID: mid}}); err != nil {
			return err
		}
		if status == runs.StatusEscalated {
			return a.Envelopes.SetStatus(ctx, x.env.ID(), "escalated", nil)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if a.Metrics != nil {
		a.Metrics.Card(card.Kind)
	}
	if msg != nil {
		a.Chats.Publish(*x.env.ChatID, "message", a.MessageView(ctx, msg))
	}
	return x.park(ctx, status, "card")
}

// checkBudget stops a call that would pass a budget (§8.4). It reports true when the run halted.
func (x *exec) checkBudget(ctx context.Context, estimate float64) (bool, error) {
	a := x.a
	left := x.env.Budget.USD - x.envelopeSpent(ctx)
	scope := ""
	switch {
	case estimate > left:
		scope = "envelope"
	default:
		now := a.now()
		day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		botID := x.bot.ID()
		if spent, err := a.Usage.Spent(ctx, x.run.SpaceID, &botID, day); err == nil {
			if lim := a.Settings.Float(ctx, &x.run.SpaceID, "budget.bot_day_usd"); lim > 0 && spent+estimate > lim {
				scope, left = "bot_day", lim-spent
			}
		}
		if scope == "" {
			if spent, err := a.Usage.Spent(ctx, x.run.SpaceID, nil, month); err == nil {
				if lim := a.Settings.Float(ctx, &x.run.SpaceID, "budget.space_month_usd"); lim > 0 && spent+estimate > lim {
					scope, left = "space_month", lim-spent
				}
			}
		}
	}
	if scope == "" {
		return false, nil
	}
	if _, err := x.append(ctx, nil, runs.New{Type: runs.EvBudgetReached, Data: evBudget{Estimate: estimate, Left: left, Scope: scope}}); err != nil {
		return true, err
	}
	detail := fmt.Sprintf("The next model call can cost up to $%.4f. The %s budget has $%.4f left.", estimate, strings.ReplaceAll(scope, "_", " "), left)
	return true, x.postCard(ctx, chats.Card{Kind: "budget", Title: "Budget reached", Detail: detail, Data: map[string]string{"scope": scope}}, runs.StatusEscalated)
}

// compact asks the summarizer role to replace older run events with a summary (§8.1).
func (x *exec) compact(ctx context.Context, st *state, window int) (bool, error) {
	a := x.a
	// Keep the last model turn and its tool results; summarize what comes before.
	cut := -1
	for i := len(st.turns) - 1; i >= 0; i-- {
		if st.turns[i].Kind == "model" {
			cut = i
			break
		}
	}
	if cut <= 0 {
		return false, nil
	}
	older := st.turns[:cut]
	var b strings.Builder
	for _, t := range older {
		switch t.Kind {
		case "summary":
			fmt.Fprintf(&b, "Earlier summary: %s\n", t.Text)
		case "model":
			fmt.Fprintf(&b, "You said: %s", t.Text)
			for _, c := range t.ToolCalls {
				fmt.Fprintf(&b, " [called %s %s]", c.Name, string(c.Args))
			}
			b.WriteString("\n")
		case "tool":
			fmt.Fprintf(&b, "Tool %s returned: %s\n", t.ToolName, truncate(t.Text, 2000))
		}
	}
	targets, err := a.Models.Resolve(ctx, x.run.SpaceID, &x.run.BotID, models.RoleSummarizer)
	if err != nil {
		return false, nil
	}
	req := models.Request{System: "You summarize the earlier steps of a bot's work. Keep facts, decisions, open questions, file paths and results. Write at most 300 words.", Messages: []models.Message{{Role: "user", Parts: []models.Part{models.TextPart("Goal: " + x.env.Goal + "\n\nSteps:\n" + b.String())}}}, MaxTokens: 1024}
	resp, idx, err := models.CallWithFallback(ctx, a.HTTP, targets, req, nil, nil)
	if err != nil {
		return false, nil
	}
	cost, reported := models.Cost(resp.Usage, targets[idx].Target)
	err = a.E.WithTx(ctx, func(ctx context.Context) error {
		if _, err := x.append(ctx, &runs.Update{TokensIn: resp.Usage.In, TokensOut: resp.Usage.Out, CostUSD: cost}, runs.New{Type: runs.EvSummary, Data: evSummary{Through: older[len(older)-1].Seq, Text: resp.Text}}); err != nil {
			return err
		}
		botID, runID := x.bot.ID(), x.run.ID()
		return a.Usage.Record(ctx, &usage.UsageEvent{SpaceID: x.run.SpaceID, Kind: "model", BotID: &botID, RunID: &runID, Step: st.step, Role: models.RoleSummarizer, ModelName: resp.Model, TokensIn: resp.Usage.In, TokensOut: resp.Usage.Out, Quantity: resp.Usage.In + resp.Usage.Out, Unit: "tokens", CostUSD: cost, Reported: reported})
	})
	return err == nil, err
}

// QueueFor returns the queue of an envelope origin.
func QueueFor(origin string) string {
	if origin == "user" {
		return jobs.Interactive
	}
	return jobs.Background
}
