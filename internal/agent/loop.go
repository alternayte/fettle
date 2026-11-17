package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

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

// Timeout is the rescue timeout (§7.2). River rescues a job only after its timeout, and a
// job without a timeout never; a live attempt sends heartbeats, so only a dead one is
// rescued. The attempt does not stop at this deadline (see Work).
func (w *Worker) Timeout(*river.Job[runs.Args]) time.Duration {
	if w.A.RescueAfter > 0 {
		return w.A.RescueAfter
	}
	return 60 * time.Second
}

// detach returns a context that ends when River cancels the job or stops, but not at the
// job's deadline: a long model stream or command keeps going while heartbeats show that
// the attempt lives.
func detach(ctx context.Context) (context.Context, context.CancelFunc) {
	inner, cancel := context.WithCancel(context.WithoutCancel(ctx))
	go func() {
		select {
		case <-ctx.Done():
			if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
				cancel()
			}
		case <-inner.Done():
		}
	}()
	return inner, cancel
}

// exec is one job attempt of one run.
type exec struct {
	a     *Agent
	run   *runs.Run
	env   *envelopes.Envelope
	bot   *bots.Bot
	fence int64
	jobID int64
	queue string
	// st is the state of the current step, for paths that end the run from a helper.
	st *state
	// forkDiff is the diff of a forked sub-agent, reported to its owner.
	forkDiff []FileDiff
}

// Work runs steps until the run finishes or waits (§7.2).
func (w *Worker) Work(rctx context.Context, job *river.Job[runs.Args]) error {
	a := w.A
	ctx, cancel := detach(rctx)
	defer cancel()
	// Heartbeats keep the rescuer away from a live attempt (§7.2).
	if a.Runs.Queue != nil {
		every := w.Timeout(job) / 3
		go func() {
			t := time.NewTicker(every)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					_ = a.Runs.Queue.Heartbeat(ctx, job.ID)
				}
			}
		}()
	}
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
	if errors.Is(err, errWaitRace) || errors.Is(err, errParked) {
		err = nil // parked, or a wake inserted the next job
	}
	a.publishRun(context.WithoutCancel(ctx), run.ID())
	if err != nil && ctx.Err() == nil {
		a.Log.Error("run attempt failed; River retries it", "run", run.ID(), "error", err)
	}
	return err
}

var errWaitRace = errors.New("the run was woken while this attempt was ending")

// errParked ends a job attempt after the run moved to waiting or escalated.
var errParked = errors.New("the run is parked")

func (x *exec) append(ctx context.Context, up *runs.Update, evs ...runs.New) ([]*runs.RunEvent, error) {
	return x.a.Journal.Append(ctx, x.run.ID(), x.fence, up, evs...)
}

func strp(s string) *string { return &s }

func (x *exec) loop(ctx context.Context) error {
	a := x.a
	// The journal is read once, then only its new events: this attempt's appends and the
	// appends of others (steer, cancel, card answers).
	var evs []*runs.RunEvent
	for i := 0; ; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		var after int64
		if len(evs) > 0 {
			after = evs[len(evs)-1].Seq
		}
		more, err := a.Journal.LoadAfter(ctx, x.run.ID(), after)
		if err != nil {
			return err
		}
		evs = append(evs, more...)
		st := rebuild(evs)
		if st.finished {
			return nil
		}
		if !st.started {
			step := "single"
			if x.env.Step != nil {
				step = *x.env.Step
			} else if x.run.Kind == "owner" {
				// Envelopes with a fixed step skip the router (§9.3).
				if step, err = x.route(ctx); err != nil {
					return err
				}
			}
			if atts := x.attachments(ctx); len(atts) > 0 && a.DeliverAttachments != nil && x.bot.ZoneID != nil {
				if err := a.DeliverAttachments(ctx, x.run.SpaceID, x.run.BotID, atts); err != nil {
					a.Log.Warn("deliver attachments", "run", x.run.ID(), "error", a.Vault.Redactor.Global(err.Error()))
				}
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
		x.st = st
		if x.run.Kind == "verifier" {
			return x.runVerifier(ctx, st)
		}
		if x.run.Kind == "owner" && (st.step == "fanout" || st.step == "forked") && !st.subStarted {
			if err := x.startFanout(ctx, st); err != nil {
				return err
			}
			continue
		}
		if st.subStarted && len(st.subResults) < len(st.subRuns) {
			return x.wait(ctx, "subagents")
		}
		if x.run.Kind == "owner" && st.step == "forked" && st.subStarted && !st.merged {
			if err := x.merge(ctx, st); err != nil {
				return err
			}
			continue
		}
		if x.run.Kind == "owner" && st.step == "handoff" && x.env.Decision.Route != nil {
			// The router gave the whole work to another bot: hand it off and wait (§9.2).
			call := models.ToolCall{ID: "route_handoff", Name: "handoff"}
			call.Args, _ = json.Marshal(map[string]any{"bot": x.env.Decision.Route.OwnerBotID, "goal": x.env.Goal, "budget_share": 0.9, "wait": true})
			if res, ok := st.handoffResults[call.ID]; ok {
				status := runs.StatusDone
				if res.Status != runs.StatusDone {
					status = runs.StatusFailed
				}
				return x.finish(ctx, st, status, res.Summary)
			}
			if err := x.handoff(ctx, st, call); err != nil {
				return err
			}
			continue
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
			handled, err := x.settle(ctx, st)
			if err != nil {
				return err
			}
			if handled {
				continue
			}
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
	n, err := x.a.E.Exec(ctx, "UPDATE runs SET status = $1, wait_reason = $2 WHERE id = $3 AND fence = $4 AND status = $5",
		status, reason, x.run.ID(), x.fence, runs.StatusRunning)
	if err != nil {
		return err
	}
	if n == 0 {
		return errWaitRace
	}
	return errParked
}

// finish appends run_finished and ends the envelope.
func (x *exec) finish(ctx context.Context, st *state, status, summary string) error {
	a := x.a
	x.forkFinish(ctx)
	envStatus := map[string]string{runs.StatusDone: "done", runs.StatusFailed: "failed", runs.StatusCancelled: "cancelled", runs.StatusEscalated: "escalated"}[status]
	err := a.E.WithTx(ctx, func(ctx context.Context) error {
		if _, err := x.append(ctx, &runs.Update{Status: strp(status), Finished: status != runs.StatusEscalated}, runs.New{Type: runs.EvRunFinished, Data: evFinished{Status: status, Summary: truncate(summary, 2000)}}); err != nil {
			return err
		}
		if err := x.tellParent(ctx, st, status, summary); err != nil {
			return err
		}
		if err := x.tellSender(ctx, status, summary); err != nil {
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
	if a.Metrics != nil && st != nil {
		a.Metrics.RunFinished(status, st.step)
	}
	if x.run.Kind == "owner" {
		x.notifyFinished(ctx, status, summary)
	}
	if x.run.Kind == "subagent" && st != nil && st.computer && a.SubagentEnded != nil {
		a.SubagentEnded(context.WithoutCancel(ctx), x.run.SpaceID, x.run.BotID, x.run.ID())
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// context builds the model input for this run with the tools that the model gets.
func (x *exec) context(ctx context.Context, st *state, window int, tools []Tool) (Context, error) {
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
	var skills []Skill
	if a.SkillIndex != nil {
		skills = a.SkillIndex(ctx, x.run.SpaceID, x.bot.ID())
	}
	loaded := ""
	if name := x.env.Decision.Skill; name != "" && a.LoadSkill != nil {
		env := &ToolEnv{SpaceID: x.run.SpaceID, BotID: x.run.BotID, RunID: x.run.ID(), Seq: st.lastSeq}
		if text, err := a.LoadSkill(ctx, env, name); err == nil {
			loaded = text
		}
	}
	in := Input{
		BotName: x.bot.Name, BotJob: x.bot.Job, Instructions: x.bot.Instructions, Skills: skills, LoadedSkill: loaded,
		SharedMemory: shared.Content, BotMemory: mem.Content, OpenWork: ow,
		Now: a.now(), TimeZone: sp.TimeZone,
		Goal: x.env.Goal, GoalOrigin: origin, Acceptance: accept, BudgetLeftUSD: x.env.Budget.USD - spent, Capabilities: x.env.Capabilities,
		Conversation: conv, Turns: st.turns, Steers: st.steers, ContextWindow: window,
		Helper: x.run.Kind == "subagent", Brief: x.env.Decision.Brief,
		Notes: st.notes, Refs: x.refs(ctx),
	}
	for _, t := range tools {
		switch t.Name {
		case "web_search":
			in.WebSearch = true
		case "web_fetch":
			in.WebFetch = true
		}
	}
	for _, at := range x.attachments(ctx) {
		where := "in your home at attachments/" + at.ID + "/" + at.Name
		if x.bot.ZoneID == nil {
			where = "not on a computer, because you have no zone"
		}
		in.Refs = append(in.Refs, fmt.Sprintf("Attached file %s (%s, %d bytes), %s", at.Name, at.Type, at.Size, where))
	}
	if x.env.Deadline != nil && a.now().Sub(*x.env.Deadline) >= 15*time.Minute {
		in.DueAt = x.env.Deadline
	}
	if st.sleepUntil != nil && a.now().Sub(*st.sleepUntil) >= 15*time.Minute {
		in.DueAt = st.sleepUntil
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
	// A direct step is one conversational answer with no tools (§9.2).
	direct := st.step == "direct"
	var tools []Tool
	if !direct {
		tools = x.tools(ctx)
	}
	c, err := x.context(ctx, st, window, tools)
	if err != nil {
		return err
	}
	if float64(c.Tokens) > 0.7*float64(window) && len(st.turns) >= 3 {
		if done, err := x.compact(ctx, st, window); err != nil || done {
			return err
		}
	}
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
	// Tool call IDs must be unique in the run, because the journal matches results by ID.
	// Some providers reuse IDs across turns, so a reused or empty ID gets a new one.
	used := map[string]bool{}
	for _, tr := range st.turns {
		for _, tc := range tr.ToolCalls {
			used[tc.ID] = true
		}
	}
	for _, ev := range st.events {
		if ev.Type == runs.EvModelTurn && st.summarySeq > 0 {
			var d evModelTurn
			_ = json.Unmarshal([]byte(ev.Data), &d)
			for _, tc := range d.ToolCalls {
				used[tc.ID] = true
			}
		}
	}
	for i := range resp.ToolCalls {
		if id := resp.ToolCalls[i].ID; id == "" || used[id] {
			resp.ToolCalls[i].ID = fmt.Sprintf("call_t%d_%d", st.modelTurns+1, i)
		}
		used[resp.ToolCalls[i].ID] = true
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
		up := &runs.Update{TokensIn: resp.Usage.In, TokensOut: resp.Usage.Out, CostUSD: cost}
		if direct {
			// The direct step journals only its start and its finish (NFR-011).
			up.Status, up.Finished = strp(runs.StatusDone), true
			if _, err := x.append(ctx, up, runs.New{Type: runs.EvRunFinished, Data: evFinished{Status: runs.StatusDone, Summary: truncate(text, 2000), Turn: &data}}); err != nil {
				return err
			}
			if err := a.Envelopes.SetStatus(ctx, x.env.ID(), "done", &envelopes.Result{Summary: truncate(text, 8000), Artifacts: []string{}, Evidence: []string{}, OpenItems: []string{}}); err != nil {
				return err
			}
		} else if _, err := x.append(ctx, up, runs.New{Type: runs.EvModelTurn, Data: data}); err != nil {
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
		if direct {
			a.Metrics.RunFinished(runs.StatusDone, st.step)
		}
	}
	if direct {
		x.notifyFinished(ctx, runs.StatusDone, text)
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

// tools returns the tools of this run: the built-in tools and the bot's connector tools,
// filtered by the envelope's capabilities and the step (REQ-AGT-004).
func (x *exec) tools(ctx context.Context) []Tool {
	sub := x.run.Kind == "subagent"
	var out []Tool
	for _, n := range x.a.Tools.Names() {
		t, _ := x.a.Tools.Get(n)
		if !x.allowed(t.Class) || (sub && t.NoSubagent) || (!sub && t.SubagentOnly) {
			continue
		}
		if t.Available != nil && !t.Available(ctx, x.run.SpaceID, x.run.BotID) {
			continue
		}
		out = append(out, t)
	}
	if x.a.Dynamic != nil {
		dyn, err := x.a.Dynamic(ctx, x.run.SpaceID, x.run.BotID)
		if err != nil {
			x.a.Log.Warn("connector tools unavailable", "bot", x.run.BotID, "error", x.a.Vault.Redactor.Global(err.Error()))
		}
		for _, t := range dyn {
			if x.allowed(t.Class) {
				out = append(out, t)
			}
		}
	}
	return out
}

// runTool executes one pending tool call with intent and result events for side effects (§7.3).
func (x *exec) runTool(ctx context.Context, st *state, call models.ToolCall) error {
	a := x.a
	var tool Tool
	ok := false
	for _, t := range x.tools(ctx) {
		if t.Name == call.Name {
			tool, ok = t, true
		}
	}
	if !ok {
		_, err := x.append(ctx, nil, runs.New{Type: runs.EvToolCall, Data: evTool{CallID: call.ID, Tool: call.Name, Args: call.Args, Error: "This tool is not available in this run."}})
		return err
	}
	if call.Name == "sleep_until" {
		return x.sleepUntil(ctx, call)
	}
	if call.Name == "propose_bot" || call.Name == "propose_team" {
		return x.propose(ctx, call)
	}
	if call.Name == "suggest_connectors" {
		return x.suggestConnectors(ctx, call)
	}
	if call.Name == "handoff" {
		return x.handoff(ctx, st, call)
	}
	if call.Name == "report_result" && x.run.Kind == "subagent" {
		return x.reportResult(ctx, st, call)
	}
	if call.Name == "ask_user" {
		return x.askUser(ctx, st, call)
	}
	if call.Name == "request_takeover" {
		return x.requestTakeover(ctx, st, call)
	}
	env := &ToolEnv{SpaceID: x.run.SpaceID, BotID: x.run.BotID, RunID: x.run.ID(), ChatID: x.env.ChatID, EnvelopeID: x.env.ID(), Actor: audit.Actor{Kind: "bot", ID: x.run.BotID.String()}, Origin: x.env.Origin, ReadData: st.readData, Seq: st.lastSeq + 1, Checked: map[string]string{}, Subagent: x.run.Kind == "subagent", Fork: x.forkRun()}
	// The computer comes first: a full host makes the run wait before any card, and
	// ClassOf may need the computer to see what the call touches.
	if tool.NeedsComputer && a.Computer != nil && !x.forkRun() {
		if err := a.Computer(ctx, x.run.SpaceID, x.run.BotID); err != nil {
			var cw *ComputerWait
			if errors.As(err, &cw) {
				return x.waitForComputer(ctx, st, cw.Reason)
			}
			if errors.Is(err, ErrNoComputer) {
				return x.waitForComputer(ctx, st, "Waiting for a free computer")
			}
			_, aerr := x.append(ctx, nil, runs.New{Type: runs.EvToolCall, Data: evTool{CallID: call.ID, Tool: call.Name, Class: tool.Class, Error: "The computer could not start: " + a.Vault.Redactor.String(ctx, x.run.SpaceID, err.Error())}})
			return aerr
		}
		if !st.computer {
			if _, err := x.append(ctx, nil, runs.New{Type: runs.EvComputerAcquired, Data: map[string]string{"tool": call.Name}}); err != nil {
				return err
			}
		}
	}
	if tool.Screen && a.ScreenUse != nil {
		release, err := a.ScreenUse(ctx, x.run.SpaceID, x.run.BotID)
		if errors.Is(err, ErrScreenControlled) {
			return x.waitForScreen(ctx, st)
		}
		if err != nil {
			return err
		}
		defer release()
	}
	if ed := st.edited[call.ID]; len(ed) > 0 {
		call.Args = x.applyEdits(ctx, call.Args, ed)
	}
	class := tool.classOf(ctx, env, call.Args)
	if !x.allowed(class) {
		_, err := x.append(ctx, nil, runs.New{Type: runs.EvToolCall, Data: evTool{CallID: call.ID, Tool: call.Name, Class: class, Args: call.Args, Error: "This action is of class " + class + ", which this work may not use."}})
		return err
	}
	side := class != envelopes.ClassRead
	decision := st.approvals[call.ID]
	// A decision covers the class that it saw. When the call now has another class (the
	// page changed), it needs a new decision.
	if decision == "allowed" && st.approvedAs[call.ID] != "" && st.approvedAs[call.ID] != class && class != envelopes.ClassRead {
		decision = ""
	}
	if side || tool.MemoryWrite {
		switch decision {
		case "allowed":
		case "denied":
			_, err := x.append(ctx, nil, runs.New{Type: runs.EvToolCall, Data: evTool{CallID: call.ID, Tool: call.Name, Class: class, Error: "The user denied this action. Do not try it again in another way. Ask the user what to do instead."}})
			return err
		case "requested":
			return x.wait(ctx, "card")
		default:
			proceed, err := x.authorize(ctx, st, tool, env, class, call)
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
	res := evTool{CallID: call.ID, Tool: call.Name, Class: class, Result: truncate(out.Text, 64*1024), Origin: out.Origin, MS: time.Since(start).Milliseconds(), Images: out.Images}
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

// sleepUntil records the timer, schedules the run's next job at that time and parks the run (§7.5).
func (x *exec) sleepUntil(ctx context.Context, call models.ToolCall) error {
	a := x.a
	var in struct {
		Time   string `json:"time"`
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(call.Args, &in)
	until, err := time.Parse(time.RFC3339, strings.TrimSpace(in.Time))
	if err != nil {
		_, aerr := x.append(ctx, nil, runs.New{Type: runs.EvToolCall, Data: evTool{CallID: call.ID, Tool: call.Name, Error: "The time must be RFC 3339 with a zone, for example 2026-11-15T09:00:00+01:00."}})
		return aerr
	}
	until = until.UTC()
	err = a.E.WithTx(ctx, func(ctx context.Context) error {
		wake := &until
		if _, err := x.append(ctx, &runs.Update{WakeAt: &wake}, runs.New{Type: runs.EvSleepUntil, Data: evSleep{CallID: call.ID, Until: until, Reason: in.Reason}},
			runs.New{Type: runs.EvToolResult, Data: evTool{CallID: call.ID, Tool: call.Name, Class: envelopes.ClassWriteLocal, Result: "Slept until " + until.Format(time.RFC3339) + ".", Origin: OriginTool}}); err != nil {
			return err
		}
		return a.Runs.ScheduleWake(ctx, x.run.ID(), x.queue, until)
	})
	if err != nil {
		return err
	}
	return x.park(ctx, runs.StatusWaiting, "timer")
}

// ComputerWait makes a run wait for its computer with a reason, for example while the
// provider downloads the computer image (§15.3).
type ComputerWait struct{ Reason string }

func (e *ComputerWait) Error() string { return e.Reason }

// waitForComputer parks the run until a computer is free. A sleeping computer wakes the
// oldest waiting run; a timer retries after 30 s as well.
func (x *exec) waitForComputer(ctx context.Context, st *state, reason string) error {
	a := x.a
	if !st.computerWait {
		if _, err := x.append(ctx, nil, runs.New{Type: runs.EvComputerWait, Data: map[string]string{"reason": reason}}); err != nil {
			return err
		}
	}
	at := a.now().Add(30 * time.Second)
	if err := a.Runs.ScheduleWake(ctx, x.run.ID(), x.queue, at); err != nil {
		return err
	}
	return x.park(ctx, runs.StatusWaiting, "computer")
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
	// Sub-agents cannot use cards (§9.4): the part fails and the owner decides.
	if x.run.Kind == "subagent" {
		return x.finish(ctx, x.st, runs.StatusFailed, "The helper stopped because it needed a person: "+card.Title+". "+card.Detail)
	}
	if card.ID == "" {
		card.ID = uuid.NewString()
	}
	card.Status = "pending"
	card.RunID = x.run.ID().String()
	var msg, link *chats.Message
	// In a group, the card goes to the bot's direct chat and the group gets a link (§9.6).
	cardChat := x.env.ChatID
	if x.env.ChatID != nil {
		if c, err := a.Chats.Get(ctx, x.run.SpaceID, *x.env.ChatID); err == nil && c.Kind == "group" {
			if d, err := a.Chats.DirectFor(ctx, x.run.SpaceID, x.run.BotID); err == nil {
				id := d.ID()
				cardChat = &id
				link = &chats.Message{SpaceID: x.run.SpaceID, ChatID: *x.env.ChatID, AuthorKind: "bot", AuthorID: x.bot.ID().String(), EnvelopeID: ptr(x.env.ID()),
					Body: fmt.Sprintf("I need your answer: %s. [Open it in my chat](/chat/%s?card=%s)", card.Title, id, card.ID)}
			}
		}
	}
	err := a.E.WithTx(ctx, func(ctx context.Context) error {
		if cardChat != nil {
			msg = &chats.Message{SpaceID: x.run.SpaceID, ChatID: *cardChat, AuthorKind: "bot", AuthorID: x.bot.ID().String(), Card: &card, EnvelopeID: ptr(x.env.ID())}
			if err := a.Chats.Insert(ctx, msg, nil); err != nil {
				return err
			}
		}
		if link != nil {
			if err := a.Chats.Insert(ctx, link, nil); err != nil {
				return err
			}
		}
		mid := ""
		if msg != nil {
			mid = msg.ID().String()
		}
		if _, err := x.append(ctx, nil, runs.New{Type: runs.EvCardPosted, Data: evCard{CardID: card.ID, Kind: card.Kind, MessageID: mid, CallID: card.Data["call_id"]}}); err != nil {
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
	x.notifyCard(ctx, card, msg)
	if msg != nil {
		a.Chats.Publish(msg.ChatID, "message", a.MessageView(ctx, msg))
	}
	if link != nil {
		a.Chats.Publish(link.ChatID, "message", a.MessageView(ctx, link))
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
		if scope == "" && a.OrgBudget != nil {
			user := ""
			if x.env.Requester.Type == "user" {
				user = x.env.Requester.ID
			}
			scope, left = a.OrgBudget(ctx, x.run.SpaceID, user, estimate)
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
	// Nothing new to compact: only an earlier summary is older than the last turn.
	if len(older) == 1 && older[0].Kind == "summary" {
		return false, nil
	}
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

// refs resolves the context references of the envelope to text lines.
func (x *exec) refs(ctx context.Context) []string {
	var out []string
	for _, r := range x.env.ContextRefs {
		switch r.Type {
		case "message":
			id, err := uuid.Parse(r.Ref)
			if err != nil || x.env.ChatID == nil {
				continue
			}
			if m, err := x.a.Chats.GetMessage(ctx, *x.env.ChatID, id); err == nil {
				out = append(out, "Message: "+truncate(m.Body, 4000))
			}
		case "file":
			out = append(out, "File in your zone: "+r.Ref)
		default:
			out = append(out, r.Type+": "+r.Ref)
		}
	}
	return out
}

// attachments returns the files of the user message that started the work.
func (x *exec) attachments(ctx context.Context) []chats.Attachment {
	if x.env.MessageID == nil || x.env.ChatID == nil {
		return nil
	}
	m, err := x.a.Chats.GetMessage(ctx, *x.env.ChatID, *x.env.MessageID)
	if err != nil {
		return nil
	}
	return m.Attachments
}

// forkRun reports a forked sub-agent, which works on its own fork (§9.4).
func (x *exec) forkRun() bool {
	return x.run.Kind == "subagent" && x.env.Step != nil && *x.env.Step == "forked"
}

// allowed applies the envelope's capabilities and the sub-agent limits: fanout helpers
// read only; forked helpers read and write local files in their fork (SI-09).
func (x *exec) allowed(class string) bool {
	if x.forkRun() {
		return (class == envelopes.ClassRead || class == envelopes.ClassWriteLocal) && slices.Contains(x.env.Capabilities, class)
	}
	return Allowed(x.env.Capabilities, x.run.Kind == "subagent", class)
}
