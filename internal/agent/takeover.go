package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/chats"
	"github.com/alternayte/fettle/internal/envelopes"
	"github.com/alternayte/fettle/internal/models"
	"github.com/alternayte/fettle/internal/runs"
)

// ErrScreenControlled means a person controls the bot's screen (§10.8).
var ErrScreenControlled = errors.New("a person controls the screen")

func (a *Agent) registerTakeover() {
	a.Tools.Add(Tool{
		Name:        "request_takeover",
		Description: "Ask a person to take over your screen, for example to solve a CAPTCHA or to sign in with a passkey. Your work waits until the person presses Done. Then take a new snapshot, because the page changed.",
		Class:       envelopes.ClassRead,
		NoSubagent:  true,
		Schema:      schema(`{"type":"object","properties":{"reason":{"type":"string","description":"What the person must do, in one sentence."}},"required":["reason"],"additionalProperties":false}`),
		// The run loop handles request_takeover, because it parks the run.
		Run: func(context.Context, *ToolEnv, json.RawMessage) (ToolOutput, error) {
			return ToolOutput{}, errors.New("request_takeover runs in the loop")
		},
	})
}

// requestTakeover posts a takeover card and parks the run until a person returns control.
func (x *exec) requestTakeover(ctx context.Context, st *state, call models.ToolCall) error {
	if x.run.Kind == "subagent" {
		_, err := x.append(ctx, nil, runs.New{Type: runs.EvToolCall, Data: evTool{CallID: call.ID, Tool: call.Name, Error: "A helper cannot ask for a takeover. Report the problem in your result."}})
		return err
	}
	if cardID, ok := st.takeoverCard[call.ID]; ok {
		ans := st.takeoverDone[cardID]
		if ans == nil {
			return x.wait(ctx, "card")
		}
		text := "The person finished on your screen and returned control. Take a new snapshot before you act."
		if v := strings.TrimSpace(ans.Value); v != "" {
			text += " The person wrote: " + v
		}
		_, err := x.append(ctx, nil,
			runs.New{Type: runs.EvTakeoverDone, Data: map[string]string{"call_id": call.ID, "card_id": cardID, "by": ans.By}},
			runs.New{Type: runs.EvToolCall, Data: evTool{CallID: call.ID, Tool: call.Name, Class: envelopes.ClassRead, Args: call.Args, Result: text, Origin: OriginTool}})
		return err
	}
	var in struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(call.Args, &in)
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		reason = "The bot needs a person on its screen."
	}
	cardID := uuid.NewString()
	if _, err := x.append(ctx, nil, runs.New{Type: runs.EvTakeoverRequested, Data: map[string]string{"call_id": call.ID, "card_id": cardID, "reason": reason}}); err != nil {
		return err
	}
	return x.postCard(ctx, chats.Card{ID: cardID, Kind: "takeover", Title: "Take over the screen", Detail: reason, Data: map[string]string{"call_id": call.ID, "bot_id": x.run.BotID.String()}}, runs.StatusWaiting)
}

// waitForScreen parks the run while a person controls the screen. Done wakes it; a timer
// retries after 30 s as well, because a control lease can expire without Done.
func (x *exec) waitForScreen(ctx context.Context, st *state) error {
	a := x.a
	if !st.computerWait {
		if _, err := x.append(ctx, nil, runs.New{Type: runs.EvComputerWait, Data: map[string]string{"reason": "Waiting while a person controls the screen"}}); err != nil {
			return err
		}
	}
	if err := a.Runs.ScheduleWake(ctx, x.run.ID(), x.queue, a.now().Add(30*time.Second)); err != nil {
		return err
	}
	return x.park(ctx, runs.StatusWaiting, "screen")
}

// WakeScreenWaiters wakes the runs of a bot that wait for its screen.
func (a *Agent) WakeScreenWaiters(ctx context.Context, botID uuid.UUID) error {
	rows, err := a.E.Query(ctx, "SELECT id FROM runs WHERE bot_id = $1 AND status = 'waiting' AND wait_reason = 'screen'", botID)
	if err != nil {
		return err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		r, err := a.Runs.Find(ctx, id)
		if err != nil {
			continue
		}
		if err := a.Runs.Wake(ctx, id, jobsQueueForRun(r)); err != nil {
			return err
		}
	}
	return nil
}

func (a *Agent) registerAskUser() {
	a.Tools.Add(Tool{
		Name:        "ask_user",
		Description: "Ask the user one question with a card and wait for the answer. Give options when the answer is one of a few choices.",
		Class:       envelopes.ClassRead,
		NoSubagent:  true,
		Schema:      schema(`{"type":"object","properties":{"question":{"type":"string"},"options":{"type":"array","items":{"type":"string"},"maxItems":6}},"required":["question"],"additionalProperties":false}`),
		// The run loop handles ask_user, because it parks the run.
		Run: func(context.Context, *ToolEnv, json.RawMessage) (ToolOutput, error) {
			return ToolOutput{}, errors.New("ask_user runs in the loop")
		},
	})
}

// askUser posts a question card and parks the run until the user answers it.
func (x *exec) askUser(ctx context.Context, st *state, call models.ToolCall) error {
	if cardID, ok := st.takeoverCard[call.ID]; ok {
		ans := st.takeoverDone[cardID]
		if ans == nil {
			return x.wait(ctx, "card")
		}
		_, err := x.append(ctx, nil, runs.New{Type: runs.EvToolCall, Data: evTool{CallID: call.ID, Tool: call.Name, Class: envelopes.ClassRead, Args: call.Args, Result: "The user answered: " + ans.Value, Origin: OriginUser}})
		return err
	}
	var in struct {
		Question string   `json:"question"`
		Options  []string `json:"options"`
	}
	_ = json.Unmarshal(call.Args, &in)
	if strings.TrimSpace(in.Question) == "" {
		_, err := x.append(ctx, nil, runs.New{Type: runs.EvToolCall, Data: evTool{CallID: call.ID, Tool: call.Name, Error: "Give the question."}})
		return err
	}
	var opts []string
	for _, o := range in.Options {
		if o = strings.TrimSpace(o); o != "" {
			opts = append(opts, truncate(o, 80))
		}
	}
	return x.postCard(ctx, chats.Card{Kind: "question", Title: truncate(strings.TrimSpace(in.Question), 300), Options: opts, Data: map[string]string{"call_id": call.ID}}, runs.StatusWaiting)
}
