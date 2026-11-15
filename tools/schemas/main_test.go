package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"

	"github.com/alternayte/fettle/internal/envelopes"
	"github.com/alternayte/fettle/internal/runs"
)

// The schema must follow the stored envelope: a new column without a schema field fails.
func TestEnvelopeSchemaCoversEveryColumn(t *testing.T) {
	s, err := envelopeSchema()
	if err != nil {
		t.Fatal(err)
	}
	et := reflect.TypeOf(envelopes.Envelope{})
	for i := 0; i < et.NumField(); i++ {
		if et.Field(i).Anonymous {
			continue // drel.Model: id and times
		}
		col, _, _ := strings.Cut(et.Field(i).Tag.Get("db"), ",")
		if col == "" || col == "decision" {
			continue
		}
		if _, ok := s.Properties[col]; !ok {
			t.Errorf("column %s has no schema property", col)
		}
	}
	r, err := s.Resolve(nil)
	if err != nil {
		t.Fatal(err)
	}
	ok := map[string]any{"id": "e1", "parent_id": nil, "root_id": "e1", "space_id": "s1", "kind": "message", "origin": "user",
		"requester": map[string]any{"type": "user", "id": "u1"}, "owner_bot_id": nil, "chat_id": nil, "message_id": nil, "goal": "Compare the desks.",
		"acceptance": []any{map[string]any{"kind": "citations", "spec": "Each fact has a source."}}, "context_refs": []any{}, "capabilities": []any{"read"},
		"budget": map[string]any{"usd": 1.0, "tokens": 1000, "wall_ms": 60000, "hops_remaining": 4, "max_subagents": 5}, "step": "fanout",
		"idempotency_key": "k1", "deadline": nil, "status": "done", "result": nil}
	if err := r.Validate(ok); err != nil {
		t.Fatalf("a valid envelope fails: %v", err)
	}
	bad := map[string]any{}
	b, _ := json.Marshal(ok)
	_ = json.Unmarshal(b, &bad)
	bad["acceptance"] = []any{}
	if err := r.Validate(bad); err == nil {
		t.Fatal("an envelope without acceptance checks validates")
	}
}

// EventTypes must list every event constant of the journal.
func TestEventTypesListEveryConstant(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "../../internal/runs/journal.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	ast.Inspect(f, func(x ast.Node) bool {
		if v, ok := x.(*ast.ValueSpec); ok {
			for _, id := range v.Names {
				if strings.HasPrefix(id.Name, "Ev") && id.Name != "EventTypes" {
					n++
				}
			}
		}
		return true
	})
	if n != len(runs.EventTypes) {
		t.Fatalf("%d event constants, %d in EventTypes", n, len(runs.EventTypes))
	}
}
