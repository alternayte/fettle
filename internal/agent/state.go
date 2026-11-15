package agent

import (
	"encoding/json"
	"fmt"
	"strings"
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
		// Origin is bot for a result that another bot sent; it is data, not intent.
		Origin string `json:"origin,omitempty"`
	}
	evCard struct {
		CardID    string `json:"card_id"`
		Kind      string `json:"kind"`
		MessageID string `json:"message_id,omitempty"`
		CallID    string `json:"call_id,omitempty"`
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
	events         []*runs.RunEvent
	started        bool
	step           string
	turns          []TurnRecord
	modelTurns     int
	extraTurns     int
	lastSeq        int64
	lastModel      *evModelTurn
	pending        []models.ToolCall // calls of the last model turn without a result
	uncertain      []evTool          // intents without results
	steers         []string          // steer texts after the last model turn
	cancelled      bool
	finished       bool
	openCard       *evCard // a posted card without an answer
	answers        []evCard
	readData       bool
	summarySeq     int64
	finalText      string
	lastMsgID      string
	budgetHalts    int
	approvals      map[string]string // call ID -> requested, allowed, denied
	approvedAs     map[string]string // call ID -> the class that the decision saw
	intent         []string          // user items of this run: the goal and steer texts
	sleepUntil     *time.Time        // the last sleep_until time
	verifyStarted  int
	verifyResults  []evVerifyResult
	verifyAccepted bool
	verifyRetries  int
	humanAnswers   []string
	lastModelSeq   int64
	notes          []string // results from other bots since the last model turn
	handoffs       map[string]evHandoffSent
	handoffResults map[string]evHandoffResult
	forkBase       map[string]string
	merged         bool
	subStarted     bool
	subRuns        []evSubStart
	subResults     map[string]evSubResult
	reported       *PartResult        // the report_result of a sub-agent run
	takeoverCard   map[string]string  // call ID of request_takeover to its card ID
	takeoverDone   map[string]*evCard // card ID to the answer that returned control
	computer       bool               // a computer_acquired event exists
	computerWait   bool               // the last computer event is a wait
}

func rebuild(evs []*runs.RunEvent) *state {
	st := &state{events: evs, approvals: map[string]string{}, approvedAs: map[string]string{}, takeoverCard: map[string]string{}, takeoverDone: map[string]*evCard{}, subResults: map[string]evSubResult{}, handoffs: map[string]evHandoffSent{}, handoffResults: map[string]evHandoffResult{}}
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
			st.lastModelSeq = ev.Seq
			st.steers, st.notes = nil, nil
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
			if d.Origin == OriginBot {
				st.notes = append(st.notes, d.Text)
			} else {
				st.steers = append(st.steers, d.Text)
				st.intent = append(st.intent, d.Text)
			}
		case runs.EvCardPosted:
			var d evCard
			_ = json.Unmarshal([]byte(ev.Data), &d)
			st.openCard = &d
			if d.Kind == "takeover" && d.CallID != "" {
				st.takeoverCard[d.CallID] = d.CardID
			}
		case runs.EvCardAnswered:
			var d evCard
			_ = json.Unmarshal([]byte(ev.Data), &d)
			if st.openCard != nil && st.openCard.CardID == d.CardID {
				st.openCard = nil
			}
			st.answers = append(st.answers, d)
			if d.Kind == "takeover" {
				st.takeoverDone[d.CardID] = &d
			}
			switch {
			case d.Kind == "verification" && d.Action == "accept":
				st.verifyAccepted = true
			case d.Kind == "verification" && d.Action == "retry":
				st.verifyRetries++
			case d.Kind == "check":
				st.humanAnswers = append(st.humanAnswers, d.Action)
			}
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
		case runs.EvHandoffSent:
			var d evHandoffSent
			_ = json.Unmarshal([]byte(ev.Data), &d)
			st.handoffs[d.CallID] = d
		case runs.EvHandoffResult:
			var d evHandoffResult
			_ = json.Unmarshal([]byte(ev.Data), &d)
			st.handoffResults[d.CallID] = d
		case runs.EvMergeResult:
			var d evMerge
			_ = json.Unmarshal([]byte(ev.Data), &d)
			st.merged = true
			st.turns = append(st.turns, TurnRecord{Seq: ev.Seq, Kind: "merge", Text: renderMerge(d), Origin: OriginTool})
		case runs.EvVerifyStarted:
			st.verifyStarted++
		case runs.EvVerifyResult:
			var d evVerifyResult
			_ = json.Unmarshal([]byte(ev.Data), &d)
			d.Seq = ev.Seq
			st.verifyResults = append(st.verifyResults, d)
			if !d.Pass {
				var b strings.Builder
				for _, c := range d.Checks {
					if !c.Pass {
						fmt.Fprintf(&b, "- %s check %q failed: %s\n", c.Kind, c.Spec, c.Reason)
					}
				}
				st.turns = append(st.turns, TurnRecord{Seq: ev.Seq, Kind: "verify", Text: b.String()})
			}
		case runs.EvSubagentsStarted:
			var d evSubStarted
			_ = json.Unmarshal([]byte(ev.Data), &d)
			st.subStarted, st.subRuns, st.forkBase = true, d.Runs, d.Base
		case runs.EvSubagentResult:
			var d evSubResult
			_ = json.Unmarshal([]byte(ev.Data), &d)
			if _, dup := st.subResults[d.RunID]; !dup {
				st.subResults[d.RunID] = d
				st.turns = append(st.turns, TurnRecord{Seq: ev.Seq, Kind: "helper", Text: renderHelperResult(len(st.subResults), d), Origin: OriginWeb})
				// Helper results carry web content, so provenance applies (§11.3).
				st.readData = true
			}
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
	return len(st.lastModel.ToolCalls) > 0 || len(st.steers) > 0 || len(st.notes) > 0
}
