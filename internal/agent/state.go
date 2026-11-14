package agent

import (
	"encoding/json"
	"time"

	"github.com/alternayte/fettle/internal/models"
	"github.com/alternayte/fettle/internal/runs"
)

// Event payloads stored in the journal.
type (
	evRunStarted struct {
		Step string `json:"step"`
		Goal string `json:"goal"`
	}
	evModelTurn struct {
		Text      string            `json:"text"`
		ToolCalls []models.ToolCall `json:"tool_calls,omitempty"`
		Usage     models.Usage      `json:"usage"`
		CostUSD   float64           `json:"cost_usd"`
		Reported  bool              `json:"cost_reported"`
		Model     string            `json:"model"`
		Role      string            `json:"role"`
		MessageID string            `json:"message_id,omitempty"`
		Request   string            `json:"request"`
		Duration  int64             `json:"duration_ms"`
	}
	evTool struct {
		CallID string          `json:"call_id"`
		Tool   string          `json:"tool"`
		Class  string          `json:"class"`
		Args   json.RawMessage `json:"args,omitempty"`
		Result string          `json:"result,omitempty"`
		Origin string          `json:"origin,omitempty"`
		Error  string          `json:"error,omitempty"`
		Idem   string          `json:"idempotency_key,omitempty"`
		MS     int64           `json:"duration_ms,omitempty"`
		Images []models.Part   `json:"images,omitempty"`
	}
	evSteer struct {
		Text      string `json:"text"`
		MessageID string `json:"message_id,omitempty"`
	}
	evCard struct {
		CardID    string `json:"card_id"`
		Kind      string `json:"kind"`
		MessageID string `json:"message_id,omitempty"`
		Action    string `json:"action,omitempty"`
		Value     string `json:"value,omitempty"`
		By        string `json:"by,omitempty"`
	}
	evSummary struct {
		Through int64  `json:"through_seq"`
		Text    string `json:"text"`
	}
	evFinished struct {
		Status  string `json:"status"`
		Summary string `json:"summary,omitempty"`
		Error   string `json:"error,omitempty"`
		// Turn is the one model turn of a direct step, which writes no model_turn event.
		Turn *evModelTurn `json:"turn,omitempty"`
	}
	evSleep struct {
		CallID string    `json:"call_id"`
		Until  time.Time `json:"until"`
		Reason string    `json:"reason,omitempty"`
	}
	evBudget struct {
		Estimate float64 `json:"estimate_usd"`
		Left     float64 `json:"left_usd"`
		Scope    string  `json:"scope"`
	}
	evCancel struct {
		By string `json:"by"`
	}
	evApproval struct {
		CallID   string `json:"call_id"`
		CardID   string `json:"card_id,omitempty"`
		Tool     string `json:"tool"`
		Decision string `json:"decision,omitempty"` // allowed or denied
		Reason   string `json:"reason,omitempty"`
		Auto     bool   `json:"auto,omitempty"` // decided by the verifier in Auto mode
		Class    string `json:"class,omitempty"`
	}
)

// state is the run rebuilt from its journal.
type state struct {
	events       []*runs.RunEvent
	started      bool
	step         string
	turns        []TurnRecord
	modelTurns   int
	extraTurns   int
	lastSeq      int64
	lastModel    *evModelTurn
	pending      []models.ToolCall // calls of the last model turn without a result
	uncertain    []evTool          // intents without results
	steers       []string          // steer texts after the last model turn
	cancelled    bool
	finished     bool
	openCard     *evCard // a posted card without an answer
	answers      []evCard
	readData     bool
	summarySeq   int64
	finalText    string
	lastMsgID    string
	budgetHalts  int
	approvals    map[string]string // call ID -> requested, allowed, denied
	approvedAs   map[string]string // call ID -> the class that the decision saw
	intent       []string          // user items of this run: the goal and steer texts
	sleepUntil   *time.Time        // the last sleep_until time
	computer     bool              // a computer_acquired event exists
	computerWait bool              // the last computer event is a wait
}

func rebuild(evs []*runs.RunEvent) *state {
	st := &state{events: evs, approvals: map[string]string{}, approvedAs: map[string]string{}}
	done := map[string]bool{}
	intents := map[string]evTool{}
	var summary *evSummary
	for _, ev := range evs {
		st.lastSeq = ev.Seq
		switch ev.Type {
		case runs.EvRunStarted:
			var d evRunStarted
			_ = json.Unmarshal([]byte(ev.Data), &d)
			st.started, st.step = true, d.Step
			st.intent = append(st.intent, d.Goal)
		case runs.EvModelTurn:
			var d evModelTurn
			_ = json.Unmarshal([]byte(ev.Data), &d)
			st.modelTurns++
			st.lastModel = &d
			st.steers = nil
			st.turns = append(st.turns, TurnRecord{Seq: ev.Seq, Kind: "model", Text: d.Text, ToolCalls: d.ToolCalls})
			if d.Text != "" {
				st.finalText = d.Text
			}
			if d.MessageID != "" {
				st.lastMsgID = d.MessageID
			}
		case runs.EvToolCall, runs.EvToolResult, runs.EvToolUncertain:
			var d evTool
			_ = json.Unmarshal([]byte(ev.Data), &d)
			if done[d.CallID] {
				continue
			}
			done[d.CallID] = true
			delete(intents, d.CallID)
			text := d.Result
			if d.Error != "" {
				text = "Error: " + d.Error
			}
			if ev.Type == runs.EvToolUncertain {
				text = "Outcome unknown. Check the current state before you continue."
			}
			st.turns = append(st.turns, TurnRecord{Seq: ev.Seq, Kind: "tool", Text: text, ToolCallID: d.CallID, ToolName: d.Tool, Origin: d.Origin, Images: d.Images})
			if d.Origin == OriginWeb || d.Origin == OriginEmail || d.Origin == OriginConnector {
				st.readData = true
			}
		case runs.EvToolIntent:
			var d evTool
			_ = json.Unmarshal([]byte(ev.Data), &d)
			intents[d.CallID] = d
		case runs.EvSteer:
			var d evSteer
			_ = json.Unmarshal([]byte(ev.Data), &d)
			st.steers = append(st.steers, d.Text)
			st.intent = append(st.intent, d.Text)
		case runs.EvCardPosted:
			var d evCard
			_ = json.Unmarshal([]byte(ev.Data), &d)
			st.openCard = &d
		case runs.EvCardAnswered:
			var d evCard
			_ = json.Unmarshal([]byte(ev.Data), &d)
			if st.openCard != nil && st.openCard.CardID == d.CardID {
				st.openCard = nil
			}
			st.answers = append(st.answers, d)
			if d.Kind == "turn_limit" && d.Action == "continue" {
				st.extraTurns++
			}
		case runs.EvSummary:
			var d evSummary
			_ = json.Unmarshal([]byte(ev.Data), &d)
			summary = &d
			st.summarySeq = d.Through
		case runs.EvApprovalRequested:
			var d evApproval
			_ = json.Unmarshal([]byte(ev.Data), &d)
			st.approvals[d.CallID] = "requested"
		case runs.EvApprovalDecided:
			var d evApproval
			_ = json.Unmarshal([]byte(ev.Data), &d)
			st.approvals[d.CallID] = d.Decision
			st.approvedAs[d.CallID] = d.Class
		case runs.EvSleepUntil:
			var d evSleep
			_ = json.Unmarshal([]byte(ev.Data), &d)
			u := d.Until
			st.sleepUntil = &u
		case runs.EvComputerAcquired:
			st.computer, st.computerWait = true, false
		case runs.EvComputerWait:
			st.computerWait = true
		case runs.EvBudgetReached:
			st.budgetHalts++
		case runs.EvCancel:
			st.cancelled = true
		case runs.EvRunFinished:
			st.finished = true
		}
	}
	for _, it := range intents {
		st.uncertain = append(st.uncertain, it)
	}
	if st.lastModel != nil {
		for _, tc := range st.lastModel.ToolCalls {
			if !done[tc.ID] {
				if _, isIntent := intents[tc.ID]; !isIntent {
					st.pending = append(st.pending, tc)
				}
			}
		}
	}
	if summary != nil {
		kept := []TurnRecord{{Seq: summary.Through, Kind: "summary", Text: summary.Text}}
		for _, t := range st.turns {
			if t.Seq > summary.Through {
				kept = append(kept, t)
			}
		}
		st.turns = kept
	}
	return st
}

// needsModel reports whether the next step is a model turn.
func (st *state) needsModel() bool {
	if len(st.pending) > 0 || len(st.uncertain) > 0 {
		return false
	}
	if st.lastModel == nil {
		return true
	}
	return len(st.lastModel.ToolCalls) > 0 || len(st.steers) > 0
}
