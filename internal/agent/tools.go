package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/envelopes"
	"github.com/alternayte/fettle/internal/models"
)

// Tool is one tool that a bot can call.
type Tool struct {
	Name        string
	Description string
	Class       string
	Schema      json.RawMessage
	// HardAsk marks actions that need a card in every mode (§11.2).
	HardAsk bool
	// NeedsComputer marks tools that act on the zone's computer; the run gets the computer
	// before it writes an intent, so a full host makes the run wait instead of fail.
	NeedsComputer bool
	// Screen marks tools that act on the bot's screen. They wait while a person controls
	// the screen (§10.8).
	Screen bool
	// MemoryWrite marks remember and forget (§11.3).
	MemoryWrite bool
	// Connector names the connector of a connector tool, for rules (§11.2).
	Connector string
	// Target returns the site and the recipient that a call touches, for rules.
	Target func(args json.RawMessage) (site, recipient string)
	// ClassOf returns the class of one call when it depends on the arguments (browser_act).
	ClassOf func(ctx context.Context, env *ToolEnv, args json.RawMessage) string
	Run     func(ctx context.Context, env *ToolEnv, args json.RawMessage) (ToolOutput, error)
}

// classOf returns the class of one call.
func (t Tool) classOf(ctx context.Context, env *ToolEnv, args json.RawMessage) string {
	if t.ClassOf != nil {
		return t.ClassOf(ctx, env, args)
	}
	return t.Class
}

// ToolOutput is the result of a tool call.
type ToolOutput struct {
	Text   string
	Origin string // tool, web, email, connector, memory
	// Images go to the owner model after the tool result, when the model has vision.
	Images []models.Part
}

// ToolEnv is the context of one tool call.
type ToolEnv struct {
	SpaceID  uuid.UUID
	BotID    uuid.UUID
	RunID    uuid.UUID
	ChatID   *uuid.UUID
	Actor    audit.Actor
	Origin   string // origin of the run's envelope
	ReadData bool   // the run already read web, email or connector content
	Seq      int64  // the journal sequence of this call, for output file names
	// Checked holds what ClassOf saw (for example the element of a browser action), so that
	// Run acts only on the same thing and the card shows it.
	Checked map[string]string
}

// Registry holds the built-in tools.
type Registry struct{ tools map[string]Tool }

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{tools: map[string]Tool{}} }

// Add registers a tool.
func (r *Registry) Add(t Tool) {
	if !slices.Contains(envelopes.AllClasses, t.Class) {
		panic("agent: tool " + t.Name + " has unknown class " + t.Class)
	}
	r.tools[t.Name] = t
}

// Get returns a tool.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Names returns the registered tool names in order.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.tools))
	for n := range r.tools {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Allowed reports whether a run with these capabilities may use a class (REQ-AGT-004):
// spend never (SI-08), a sub-agent only read (SI-09), else the class must be a capability.
func Allowed(capabilities []string, subagent bool, class string) bool {
	switch {
	case class == envelopes.ClassSpend:
		return false
	case subagent && class != envelopes.ClassRead:
		return false
	}
	return slices.Contains(capabilities, class)
}

// For returns the tools of a run filtered by the envelope's capabilities (REQ-AGT-004).
// A tool whose class depends on the call (browser_act) is checked again per call.
func (r *Registry) For(capabilities []string, subagent bool) []Tool {
	var out []Tool
	for _, n := range r.Names() {
		if t := r.tools[n]; Allowed(capabilities, subagent, t.Class) {
			out = append(out, t)
		}
	}
	return out
}

var errBadArgs = errors.New("the tool arguments do not match the schema")

func decode[T any](args json.RawMessage) (T, error) {
	var v T
	if err := json.Unmarshal(args, &v); err != nil {
		return v, fmt.Errorf("%w: %w", errBadArgs, err)
	}
	return v, nil
}

func schema(s string) json.RawMessage { return json.RawMessage(strings.TrimSpace(s)) }
