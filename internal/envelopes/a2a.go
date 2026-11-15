package envelopes

import (
	"fmt"
	"time"
)

// A2A task states (A2A 1.x, Appendix C.1).
const (
	A2ASubmitted     = "TASK_STATE_SUBMITTED"
	A2AWorking       = "TASK_STATE_WORKING"
	A2AInputRequired = "TASK_STATE_INPUT_REQUIRED"
	A2ACompleted     = "TASK_STATE_COMPLETED"
	A2AFailed        = "TASK_STATE_FAILED"
	A2ACanceled      = "TASK_STATE_CANCELED"
)

// A2ATask is an A2A 1.x Task.
type A2ATask struct {
	ID        string         `json:"id"`
	ContextID string         `json:"contextId"`
	Status    A2AStatus      `json:"status"`
	History   []A2AMessage   `json:"history,omitempty"`
	Artifacts []A2AArtifact  `json:"artifacts,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// A2AStatus is the status of an A2A task.
type A2AStatus struct {
	State     string      `json:"state"`
	Message   *A2AMessage `json:"message,omitempty"`
	Timestamp string      `json:"timestamp,omitempty"`
}

// A2AMessage is an A2A message.
type A2AMessage struct {
	MessageID string    `json:"messageId"`
	ContextID string    `json:"contextId,omitempty"`
	TaskID    string    `json:"taskId,omitempty"`
	Role      string    `json:"role"`
	Parts     []A2APart `json:"parts"`
}

// A2APart is one part of a message or an artifact: text, a file URL, or data.
type A2APart struct {
	Text      string `json:"text,omitempty"`
	URL       string `json:"url,omitempty"`
	Filename  string `json:"filename,omitempty"`
	MediaType string `json:"mediaType,omitempty"`
	Data      any    `json:"data,omitempty"`
}

// A2AArtifact is an output of a task.
type A2AArtifact struct {
	ArtifactID string    `json:"artifactId"`
	Name       string    `json:"name,omitempty"`
	Parts      []A2APart `json:"parts"`
}

// A2AState maps an envelope status to an A2A task state. waiting means the owner run
// waits for a person (a question, an approval or a takeover).
func A2AState(status string, waiting bool) string {
	switch {
	case status == "done":
		return A2ACompleted
	case status == "failed":
		return A2AFailed
	case status == "cancelled":
		return A2ACanceled
	case status == "escalated" || waiting:
		return A2AInputRequired
	case status == "running" || status == "verifying":
		return A2AWorking
	}
	return A2ASubmitted
}

// fettleMeta is the Fettle extension in A2A metadata: the fields that A2A has no place
// for, so a task converts back without loss.
type fettleMeta struct {
	Status       string       `json:"status"`
	Kind         string       `json:"kind"`
	Origin       string       `json:"origin"`
	ParentID     string       `json:"parent_id,omitempty"`
	SpaceID      string       `json:"space_id"`
	OwnerBotID   string       `json:"owner_bot_id,omitempty"`
	Step         string       `json:"step,omitempty"`
	Budget       Budget       `json:"budget"`
	Capabilities []string     `json:"capabilities"`
	Acceptance   []Check      `json:"acceptance"`
	ContextRefs  []ContextRef `json:"context_refs,omitempty"`
	Evidence     []string     `json:"evidence,omitempty"`
	OpenItems    []string     `json:"open_items,omitempty"`
	NextOwner    string       `json:"next_owner,omitempty"`
}

// ToA2A converts an envelope to an A2A Task (Appendix C.1). question is the text of a card
// that waits for a person, shown as the status message of an input-required task.
func ToA2A(e *Envelope, waiting bool, question string) A2ATask {
	id, ctxID := e.ID().String(), e.RootID.String()
	t := A2ATask{ID: id, ContextID: ctxID, Status: A2AStatus{State: A2AState(e.Status, waiting), Timestamp: e.UpdatedAt().UTC().Format(time.RFC3339Nano)}}
	goal := A2AMessage{MessageID: id + ":goal", ContextID: ctxID, TaskID: id, Role: "ROLE_USER", Parts: []A2APart{{Text: e.Goal, MediaType: "text/plain"}}}
	for _, r := range e.ContextRefs {
		if r.Type == "file" {
			goal.Parts = append(goal.Parts, A2APart{URL: "file:///home/fettle/" + r.Ref, Filename: r.Ref})
		} else {
			goal.Parts = append(goal.Parts, A2APart{Data: map[string]any{"type": r.Type, "ref": r.Ref}, MediaType: "application/json"})
		}
	}
	t.History = []A2AMessage{goal}
	if question != "" && t.Status.State == A2AInputRequired {
		t.Status.Message = &A2AMessage{MessageID: id + ":question", ContextID: ctxID, TaskID: id, Role: "ROLE_AGENT", Parts: []A2APart{{Text: question, MediaType: "text/plain"}}}
	}
	m := fettleMeta{Status: e.Status, Kind: e.Kind, Origin: e.Origin, SpaceID: e.SpaceID.String(), Budget: e.Budget, Capabilities: e.Capabilities, Acceptance: e.Acceptance, ContextRefs: e.ContextRefs}
	if e.ParentID != nil {
		m.ParentID = e.ParentID.String()
	}
	if e.OwnerBotID != nil {
		m.OwnerBotID = e.OwnerBotID.String()
	}
	if e.Step != nil {
		m.Step = *e.Step
	}
	if e.Result != nil {
		t.Artifacts = append(t.Artifacts, A2AArtifact{ArtifactID: "summary", Name: "summary", Parts: []A2APart{{Text: e.Result.Summary, MediaType: "text/markdown"}}})
		for i, a := range e.Result.Artifacts {
			t.Artifacts = append(t.Artifacts, A2AArtifact{ArtifactID: fmt.Sprintf("artifact-%d", i+1), Name: a, Parts: []A2APart{{URL: "file:///home/fettle/" + a, Filename: a}}})
		}
		m.Evidence, m.OpenItems, m.NextOwner = e.Result.Evidence, e.Result.OpenItems, e.Result.NextOwner
	}
	t.Metadata = map[string]any{"fettle": m}
	return t
}

// A2AFields are the envelope fields that Appendix C.1 maps.
type A2AFields struct {
	ID, RootID, ParentID, SpaceID, OwnerBotID string
	Kind, Origin, Status, Step, Goal          string
	ContextRefs                               []ContextRef
	Budget                                    Budget
	Capabilities                              []string
	Acceptance                                []Check
	Result                                    *Result
}

// FromA2A reads the mapped envelope fields back from an A2A Task that ToA2A made.
func FromA2A(t A2ATask, meta fettleMeta) A2AFields {
	f := A2AFields{ID: t.ID, RootID: t.ContextID, ParentID: meta.ParentID, SpaceID: meta.SpaceID, OwnerBotID: meta.OwnerBotID,
		Kind: meta.Kind, Origin: meta.Origin, Status: meta.Status, Step: meta.Step, ContextRefs: meta.ContextRefs,
		Budget: meta.Budget, Capabilities: meta.Capabilities, Acceptance: meta.Acceptance}
	if len(t.History) > 0 && len(t.History[0].Parts) > 0 {
		f.Goal = t.History[0].Parts[0].Text
	}
	for _, a := range t.Artifacts {
		if f.Result == nil {
			f.Result = &Result{Artifacts: []string{}, Evidence: meta.Evidence, OpenItems: meta.OpenItems, NextOwner: meta.NextOwner}
		}
		if a.ArtifactID == "summary" && len(a.Parts) > 0 {
			f.Result.Summary = a.Parts[0].Text
		} else {
			f.Result.Artifacts = append(f.Result.Artifacts, a.Name)
		}
	}
	return f
}

// Fields returns the mapped fields of an envelope, for comparison with FromA2A.
func (e *Envelope) Fields() A2AFields {
	f := A2AFields{ID: e.ID().String(), RootID: e.RootID.String(), SpaceID: e.SpaceID.String(), Kind: e.Kind, Origin: e.Origin, Status: e.Status,
		Goal: e.Goal, ContextRefs: e.ContextRefs, Budget: e.Budget, Capabilities: e.Capabilities, Acceptance: e.Acceptance, Result: e.Result}
	if e.ParentID != nil {
		f.ParentID = e.ParentID.String()
	}
	if e.OwnerBotID != nil {
		f.OwnerBotID = e.OwnerBotID.String()
	}
	if e.Step != nil {
		f.Step = *e.Step
	}
	if len(f.ContextRefs) == 0 {
		f.ContextRefs = nil
	}
	return f
}
