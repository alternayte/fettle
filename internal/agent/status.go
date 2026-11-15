package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/alternayte/fettle/internal/runs"
)

// statusWaits are the waits during which a status question gets a direct reply from the
// run's events (§7.4). A run that waits for a card or a timer gets the question as steer.
var statusWaits = map[string]bool{"subagents": true, "computer": true, "handoff": true}

var statusQuestion = regexp.MustCompile(`(?i)^\W*(how far|how is it going|how's it going|how long|status|progress|any (news|update|progress)|are you (done|finished|there yet)|what are you doing|where are you|eta|update)\b`)

// isStatusQuestion reports whether a message only asks how the work goes. It must be short:
// a longer message carries instructions and goes to the run as a steer event.
func isStatusQuestion(body string) bool {
	b := strings.TrimSpace(body)
	return len(strings.Fields(b)) <= 8 && statusQuestion.MatchString(b)
}

// statusReply builds the reply to a status question from the run's events, without a
// model call and without a change to the run.
func (a *Agent) statusReply(ctx context.Context, r *runs.Run) (string, error) {
	evs, err := a.Journal.Load(ctx, r.ID())
	if err != nil {
		return "", err
	}
	var helpers, done, failed int
	var goals []string
	var computer, handoff string
	for _, ev := range evs {
		switch ev.Type {
		case runs.EvSubagentsStarted:
			var d evSubStarted
			_ = json.Unmarshal([]byte(ev.Data), &d)
			helpers += len(d.Runs)
			for _, s := range d.Runs {
				goals = append(goals, s.Goal)
			}
		case runs.EvSubagentResult:
			var d evSubResult
			_ = json.Unmarshal([]byte(ev.Data), &d)
			done++
			if d.Status != "" && d.Status != runs.StatusDone {
				failed++
			}
		case runs.EvComputerWait:
			var d struct {
				Reason string `json:"reason"`
			}
			_ = json.Unmarshal([]byte(ev.Data), &d)
			computer = d.Reason
		case runs.EvHandoffSent:
			var d evHandoffSent
			_ = json.Unmarshal([]byte(ev.Data), &d)
			handoff = d.BotName
		}
	}
	var b strings.Builder
	switch r.WaitReason {
	case "subagents":
		fmt.Fprintf(&b, "I wait for my helpers: %d of %d are done", done, helpers)
		if failed > 0 {
			fmt.Fprintf(&b, ", %d of them failed", failed)
		}
		b.WriteString(".")
		if len(goals) > 0 {
			b.WriteString(" They work on:")
			for _, g := range goals {
				b.WriteString("\n- " + firstLine(g, 120))
			}
		}
	case "computer":
		if computer == "" {
			computer = "Waiting for a free computer"
		}
		b.WriteString(computer + ".")
	case "handoff":
		if handoff != "" {
			fmt.Fprintf(&b, "I wait for %s to finish the part that I handed off.", handoff)
		} else {
			b.WriteString("I wait for another bot to finish the part that I handed off.")
		}
	}
	if r.StartedAt != nil {
		fmt.Fprintf(&b, "\nI started %s ago.", a.now().Sub(*r.StartedAt).Round(time.Second))
	}
	b.WriteString("\nI tell you when the result is ready.")
	return b.String(), nil
}

func firstLine(s string, n int) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
