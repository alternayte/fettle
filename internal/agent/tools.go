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
	// MemoryWrite marks remember and forget (§11.3).
	MemoryWrite bool
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

// For returns the tools of a run filtered by the envelope's capabilities (REQ-AGT-004).
// subagent keeps read tools only (SI-09). spend is never offered (SI-08).
func (r *Registry) For(capabilities []string, subagent bool) []Tool {
	var out []Tool
	for _, n := range r.Names() {
		t := r.tools[n]
		if t.Class == envelopes.ClassSpend {
			continue
		}
		if subagent && t.Class != envelopes.ClassRead {
			continue
		}
		if !slices.Contains(capabilities, t.Class) {
			continue
		}
		out = append(out, t)
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
