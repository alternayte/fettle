package envelopes

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"reflect"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/google/uuid"
)

// a2aSchema resolves the vendored A2A bundle. Its $refs name files such as
// lf.a2a.v1.TaskStatus.jsonschema.json; each maps to the definition titled "Task Status".
func a2aSchema(t *testing.T, title string) *jsonschema.Resolved {
	t.Helper()
	raw, err := os.ReadFile("testdata/a2a/a2a-v1.0.1.json")
	if err != nil {
		t.Fatal(err)
	}
	var bundle struct {
		Definitions map[string]json.RawMessage `json:"definitions"`
	}
	if err := json.Unmarshal(raw, &bundle); err != nil {
		t.Fatal(err)
	}
	byFile := map[string]json.RawMessage{}
	for name, def := range bundle.Definitions {
		key := strings.ReplaceAll(name, " ", "")
		byFile["lf.a2a.v1."+key+".jsonschema.json"] = def
		byFile["google.protobuf."+key+".jsonschema.json"] = def
	}
	load := func(u *url.URL) (*jsonschema.Schema, error) {
		def, ok := byFile[path.Base(u.Path)]
		if !ok {
			return nil, fmt.Errorf("no definition for %s", u)
		}
		var s jsonschema.Schema
		if err := json.Unmarshal(def, &s); err != nil {
			return nil, err
		}
		return &s, nil
	}
	var root jsonschema.Schema
	if err := json.Unmarshal(bundle.Definitions[title], &root); err != nil {
		t.Fatal(err)
	}
	rs, err := root.Resolve(&jsonschema.ResolveOptions{BaseURI: "file:///a2a/" + "lf.a2a.v1." + strings.ReplaceAll(title, " ", "") + ".jsonschema.json", Loader: load})
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

func TestSCN_TEAM_011_EnvelopesConvertToA2ATasks(t *testing.T) {
	task := a2aSchema(t, "Task")
	parent := uuid.New()
	step := "fanout"
	cases := []struct {
		status   string
		waiting  bool
		question string
		want     string
	}{
		{"created", false, "", A2ASubmitted},
		{"routed", false, "", A2ASubmitted},
		{"running", false, "", A2AWorking},
		{"verifying", false, "", A2AWorking},
		{"running", true, "Which account should I use?", A2AInputRequired},
		{"escalated", false, "The result failed its checks.", A2AInputRequired},
		{"done", false, "", A2ACompleted},
		{"failed", false, "", A2AFailed},
		{"cancelled", false, "", A2ACanceled},
	}
	for _, c := range cases {
		e := &Envelope{ParentID: &parent, RootID: uuid.New(), SpaceID: uuid.New(), Kind: "handoff", Origin: "bot", OwnerBotID: ptrUUID(uuid.New()),
			Goal: "Compare the three Acme desks.", Acceptance: []Check{DefaultCheck, {Kind: "citations", Spec: "Each fact links its page."}},
			ContextRefs:  []ContextRef{{Type: "file", Ref: "shared/notes.txt"}, {Type: "message", Ref: uuid.NewString()}},
			Capabilities: []string{ClassRead, ClassWriteLocal}, Budget: Budget{USD: 1.5, Tokens: 100000, WallMS: 600000, HopsRemaining: 2, MaxSubagents: 3},
			Step: &step, Status: c.status}
		e.SetID(uuid.New())
		if c.status == "done" {
			e.Result = &Result{Summary: "| Desk | Price |\n|---|---|\n| Alpha | $420 |", Artifacts: []string{"shared/desks.md"}, Evidence: []string{"http://docs.test/alpha"}, OpenItems: []string{"Check the lamp."}}
		}
		tk := ToA2A(e, c.waiting, c.question)
		if tk.Status.State != c.want {
			t.Fatalf("%s: state %s, want %s", c.status, tk.Status.State, c.want)
		}
		raw, _ := json.Marshal(tk)
		var inst map[string]any
		_ = json.Unmarshal(raw, &inst)
		if err := task.Validate(inst); err != nil {
			t.Fatalf("%s: the task is not valid A2A: %v\n%s", c.status, err, raw)
		}
		if c.question != "" && (tk.Status.Message == nil || tk.Status.Message.Parts[0].Text != c.question) {
			t.Fatalf("%s: no question in the status message", c.status)
		}
		var back A2ATask
		if err := json.Unmarshal(raw, &back); err != nil {
			t.Fatal(err)
		}
		mraw, _ := json.Marshal(back.Metadata["fettle"])
		var meta fettleMeta
		_ = json.Unmarshal(mraw, &meta)
		if got, want := FromA2A(back, meta), e.Fields(); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: round trip\n got %+v\nwant %+v", c.status, got, want)
		}
	}
}

func ptrUUID(u uuid.UUID) *uuid.UUID { return &u }
