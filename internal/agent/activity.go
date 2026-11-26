package agent

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Activity is the current step of a run as the chat shows it: one short label that
// changes at each step (docs/specs/live-activity.md). An empty label clears the line. It
// is not a journal event.
type Activity struct {
	RunID      string    `json:"run_id"`
	EnvelopeID string    `json:"envelope_id"`
	BotID      string    `json:"bot_id"`
	Label      string    `json:"label"`
	Tool       string    `json:"tool,omitempty"`
	StartedAt  time.Time `json:"started_at"`
}

// Activity labels.
const (
	actThinking = "Thinking"
	actRouting  = "Choosing how to do the work"
)

// activityOf returns the current activity of a run, if any.
func (a *Agent) activityOf(runID uuid.UUID) *Activity {
	v, ok := a.acts.Load(runID)
	if !ok {
		return nil
	}
	act := v.(Activity)
	return &act
}

// setActivity keeps the current activity of a run and sends it to the run's chat. An
// empty label clears it.
func (a *Agent) setActivity(ctx context.Context, spaceID, runID, envID, botID uuid.UUID, chatID *uuid.UUID, label, tool string) {
	act := Activity{RunID: runID.String(), EnvelopeID: envID.String(), BotID: botID.String(), Label: label, Tool: tool, StartedAt: a.now().UTC()}
	if label != "" && a.Vault != nil {
		act.Label = a.Vault.Redactor.String(ctx, spaceID, label)
	}
	if label == "" {
		if _, had := a.acts.LoadAndDelete(runID); !had {
			return
		}
	} else {
		a.acts.Store(runID, act)
	}
	if chatID != nil {
		a.Chats.Publish(*chatID, "activity", act)
	}
}

// activity sets the current activity of this run. Only owner runs with a chat show one.
func (x *exec) activity(ctx context.Context, label, tool string) {
	if x.run.Kind != "owner" || x.env.ChatID == nil {
		return
	}
	x.a.setActivity(ctx, x.run.SpaceID, x.run.ID(), x.env.ID(), x.run.BotID, x.env.ChatID, label, tool)
}

// waitActivity is the label of a run that waits for a reason, or "" when the chat
// already shows why (a card) or the wait is long (a timer).
func waitActivity(reason string) string {
	switch reason {
	case "computer":
		return "Waiting for a computer"
	case "subagents":
		return "Waiting for helpers"
	case "handoff":
		return "Waiting for a teammate"
	case "screen":
		return "Waiting for the screen"
	}
	return ""
}

// cut returns the first line of s, at most n characters.
func cut(s string, n int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = strings.TrimSpace(s[:i]) + " …"
	}
	if utf8.RuneCountInString(s) > n {
		r := []rune(s)
		return strings.TrimSpace(string(r[:n-1])) + "…"
	}
	return s
}

// hostOf returns the host of a URL, or "" when it has none.
func hostOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Hostname()
}

// toolLabel is the activity label of a tool call. It never holds the arguments in full:
// the host of a URL, the first line of a command cut to 60 characters, or a file path.
func toolLabel(name string, args json.RawMessage) string {
	var in struct {
		Command string `json:"command"`
		Path    string `json:"path"`
		URL     string `json:"url"`
		Bot     string `json:"bot"`
	}
	_ = json.Unmarshal(args, &in)
	path := cut(in.Path, 60)
	with := func(verb, what string) string {
		if what == "" {
			return verb
		}
		return verb + " " + what
	}
	switch name {
	case "exec":
		return with("Running", cut(in.Command, 60))
	case "read_file":
		return with("Reading", path)
	case "list_files":
		return with("Listing files in", path)
	case "search_files":
		return "Searching files"
	case "write_file", "edit_file":
		return with("Editing", path)
	case "delete_file":
		return with("Deleting", path)
	case "web_search":
		return "Searching the web"
	case "web_fetch":
		return with("Reading", hostOf(in.URL))
	case "browser_navigate":
		return with("Opening", hostOf(in.URL))
	case "browser_snapshot":
		return "Reading the page"
	case "browser_screenshot", "computer_mode":
		return "Looking at the screen"
	case "browser_act":
		return "Using the page"
	case "browser_downloads":
		return "Checking downloads"
	case "fill_login", "totp_code":
		return "Signing in"
	case "use_secret":
		return "Using a secret"
	case "remember", "forget":
		return "Updating memory"
	case "search_history":
		return "Searching past chats"
	case "load_skill":
		return "Loading a skill"
	case "handoff":
		return "Handing off to a teammate"
	case "attach_file":
		return "Attaching a file"
	}
	return "Using " + name
}

// endActivity sets the activity of a run at the end of a job attempt: a label for a
// short wait that the chat does not show otherwise, else none.
func (x *exec) endActivity(ctx context.Context) {
	r, err := x.a.Runs.Find(ctx, x.run.ID())
	if err != nil {
		return
	}
	label := ""
	if r.Status == "waiting" {
		label = waitActivity(r.WaitReason)
	}
	if label == "" || (x.a.activityOf(r.ID()) != nil && x.a.activityOf(r.ID()).Label == label) {
		if label == "" {
			x.activity(ctx, "", "")
		}
		return
	}
	x.activity(ctx, label, "")
}

// queueMS is the wait from the creation of the run to its first claim.
func (x *exec) queueMS() int64 {
	if x.run.StartedAt == nil {
		return 0
	}
	return max(x.run.StartedAt.Sub(x.run.CreatedAt()).Milliseconds(), 0)
}
