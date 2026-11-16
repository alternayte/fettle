package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// API types (C-07).
const (
	APIOpenAIChat      = "openai-chat"
	APIOpenAIResponses = "openai-responses"
	APIAnthropic       = "anthropic-messages"
)

// Roles (§8.3).
const (
	RoleRouter      = "router"
	RoleOwner       = "owner"
	RoleVerifier    = "verifier"
	RoleComputerUse = "computer_use"
	RoleSummarizer  = "summarizer"
	// RoleTranscribe turns spoken teach notes into text (§13.3). It is optional and never
	// uses the owner targets.
	RoleTranscribe = "transcribe"
)

// AllRoles lists the roles in display order.
var AllRoles = []string{RoleOwner, RoleRouter, RoleVerifier, RoleComputerUse, RoleSummarizer, RoleTranscribe}

// Part is one piece of message content.
type Part struct {
	Type      string `json:"type"` // text or image
	Text      string `json:"text,omitempty"`
	MediaType string `json:"media_type,omitempty"`
	Data      []byte `json:"data,omitempty"` // image bytes
}

// TextPart returns a text part.
func TextPart(s string) Part { return Part{Type: "text", Text: s} }

// ToolCall is one call that the model asks for.
type ToolCall struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// Message is one model message. Role is user, assistant or tool.
type Message struct {
	Role       string     `json:"role"`
	Parts      []Part     `json:"parts,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolName   string     `json:"tool_name,omitempty"`
}

// Text returns the joined text parts.
func (m Message) Text() string {
	s := ""
	for _, p := range m.Parts {
		if p.Type == "text" {
			s += p.Text
		}
	}
	return s
}

// ToolDef describes a tool to the model.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}

// Schema asks for structured output that validates against a JSON Schema.
type Schema struct {
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
}

// Request is one model call.
type Request struct {
	Model       string    `json:"model"`
	System      string    `json:"system"`
	Messages    []Message `json:"messages"`
	Tools       []ToolDef `json:"tools,omitempty"`
	Output      *Schema   `json:"output,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature *float64  `json:"temperature,omitempty"`
}

// Usage is the token use of one call.
type Usage struct {
	In        int64    `json:"in"`
	Out       int64    `json:"out"`
	CacheRead int64    `json:"cache_read"`
	Cost      *float64 `json:"cost,omitempty"` // reported by the provider (OpenRouter)
}

// Response is the result of one model call.
type Response struct {
	Text       string          `json:"text"`
	ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
	JSON       json.RawMessage `json:"json,omitempty"`
	Usage      Usage           `json:"usage"`
	StopReason string          `json:"stop_reason"`
	Model      string          `json:"model"`
}

// Conn is a resolved connection to a provider.
type Conn struct {
	APIType    string
	BaseURL    string
	AuthStyle  string
	AuthHeader string
	Key        string
	Headers    map[string]string
}

// CallError is a failed model call.
type CallError struct {
	Status     int
	Message    string
	FirstToken bool // true when the stream failed after the first token
	Timeout    bool
}

func (e *CallError) Error() string {
	switch {
	case e.Timeout:
		return "model call timed out: " + e.Message
	case e.Status != 0:
		return fmt.Sprintf("model call failed with status %d: %s", e.Status, e.Message)
	default:
		return "model call failed: " + e.Message
	}
}

// Retryable reports whether fallback rules may retry the call (§8.3): 429, 5xx or a
// timeout before the first token.
func (e *CallError) Retryable() bool {
	if e.FirstToken {
		return false
	}
	return e.Timeout || e.Status == 429 || e.Status >= 500 || e.Status == 0
}

// IsRetryable reports whether err allows a retry.
func IsRetryable(err error) bool {
	var ce *CallError
	if errors.As(err, &ce) {
		return ce.Retryable()
	}
	return false
}

// FirstTokenTimeout is the time a call may take before its first token.
var FirstTokenTimeout = 60 * time.Second
