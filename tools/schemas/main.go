// Command schemas writes the JSON Schemas of the envelope (Appendix C) and of a run event
// (Appendix D) for the SDK. `just gen` runs it.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/alternayte/fettle/internal/runs"
)

// Envelope is the JSON form of an envelope (Appendix C).
type Envelope struct {
	ID             string       `json:"id" jsonschema:"UUID"`
	ParentID       *string      `json:"parent_id" jsonschema:"UUID of the parent envelope, or null for a root"`
	RootID         string       `json:"root_id" jsonschema:"UUID of the first envelope of the tree; equals id for a root"`
	SpaceID        string       `json:"space_id" jsonschema:"UUID; the same as the parent's"`
	Kind           string       `json:"kind"`
	Origin         string       `json:"origin" jsonschema:"sets the intent (provenance)"`
	Requester      Requester    `json:"requester"`
	OwnerBotID     *string      `json:"owner_bot_id" jsonschema:"UUID; set by the inbox or the router"`
	ChatID         *string      `json:"chat_id" jsonschema:"UUID of the chat where the result goes"`
	MessageID      *string      `json:"message_id" jsonschema:"UUID of the message that started the work"`
	Goal           string       `json:"goal"`
	Acceptance     []Check      `json:"acceptance" jsonschema:"at least one check"`
	ContextRefs    []ContextRef `json:"context_refs"`
	Capabilities   []string     `json:"capabilities" jsonschema:"capability classes; a child has a subset of its parent's"`
	Budget         Budget       `json:"budget" jsonschema:"a child's budget is at most the parent's remainder"`
	Step           *string      `json:"step" jsonschema:"ladder step; fixed for part and verify"`
	IdempotencyKey string       `json:"idempotency_key" jsonschema:"unique per space"`
	Deadline       *time.Time   `json:"deadline"`
	Status         string       `json:"status"`
	Result         *Result      `json:"result" jsonschema:"set at done"`
}

// Requester is who asked for the work.
type Requester struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// Check is one acceptance check.
type Check struct {
	Kind string `json:"kind"`
	Spec string `json:"spec"`
}

// ContextRef names context for the work.
type ContextRef struct {
	Type string `json:"type"`
	Ref  string `json:"ref" jsonschema:"files only from shared/"`
}

// Budget is the limit of the work.
type Budget struct {
	USD           float64 `json:"usd"`
	Tokens        int64   `json:"tokens"`
	WallMS        int64   `json:"wall_ms"`
	HopsRemaining int     `json:"hops_remaining"`
	MaxSubagents  int     `json:"max_subagents"`
}

// Result is the outcome of done work.
type Result struct {
	Summary   string   `json:"summary"`
	Artifacts []string `json:"artifacts"`
	Evidence  []string `json:"evidence"`
	OpenItems []string `json:"open_items"`
	NextOwner string   `json:"next_owner,omitempty"`
}

// Event is one run event of the journal, as the API returns it.
type Event struct {
	Seq       int64           `json:"seq" jsonschema:"position in the journal, from 1, without gaps"`
	At        time.Time       `json:"at"`
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data" jsonschema:"the data of the event type"`
	Truncated bool            `json:"truncated" jsonschema:"true when data is a preview of a payload in the object store"`
}

var (
	steps        = []any{"direct", "single", "fanout", "forked", "handoff", "hire"}
	capabilities = []any{"read", "write_local", "write_external", "send", "spend", "admin"}
)

func enum(vals ...string) []any {
	out := make([]any, len(vals))
	for i, v := range vals {
		out[i] = v
	}
	return out
}

func envelopeSchema() (*jsonschema.Schema, error) {
	s, err := jsonschema.For[Envelope](nil)
	if err != nil {
		return nil, err
	}
	s.Schema, s.ID, s.Title = "https://json-schema.org/draft/2020-12/schema", "https://github.com/alternayte/fettle/sdk/schemas/envelope.schema.json", "Fettle envelope"
	p := s.Properties
	p["kind"].Enum = enum("message", "routine", "webhook", "handoff", "part", "verify")
	p["origin"].Enum = enum("user", "routine", "webhook", "bot")
	p["status"].Enum = enum("created", "routed", "running", "verifying", "done", "failed", "escalated", "cancelled")
	p["requester"].Properties["type"].Enum = enum("user", "bot", "routine")
	p["acceptance"].Items.Properties["kind"].Enum = enum("llm", "file", "command", "citations", "human")
	p["context_refs"].Items.Properties["type"].Enum = enum("message", "file", "artifact", "run")
	p["capabilities"].Items.Enum = capabilities
	one, eight := 1, 8000
	p["goal"].MinLength, p["goal"].MaxLength = &one, &eight
	p["acceptance"].MinItems = &one
	p["step"].Enum = append(append([]any{}, steps...), nil)
	return s, nil
}

func eventSchema() (*jsonschema.Schema, error) {
	s, err := jsonschema.For[Event](nil)
	if err != nil {
		return nil, err
	}
	s.Schema, s.ID, s.Title = "https://json-schema.org/draft/2020-12/schema", "https://github.com/alternayte/fettle/sdk/schemas/run-event.schema.json", "Fettle run event"
	s.Properties["type"].Enum = enum(runs.EventTypes...)
	return s, nil
}

func write(dir, name string, s *jsonschema.Schema) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), append(b, '\n'), 0o644)
}

func main() {
	dir := "sdk/schemas"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	env, err := envelopeSchema()
	if err == nil {
		var ev *jsonschema.Schema
		if ev, err = eventSchema(); err == nil {
			if err = os.MkdirAll(dir, 0o755); err == nil {
				if err = write(dir, "envelope.schema.json", env); err == nil {
					err = write(dir, "run-event.schema.json", ev)
				}
			}
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "schemas:", err)
		os.Exit(1)
	}
}
