// Package model is the fake model server for tests (§24.4). It speaks openai-chat,
// openai-responses and anthropic-messages with streaming, tool calls, JSON Schema
// output, image input, usage and cost. YAML scripts, chosen by markers in the system
// prompt and the last user message, decide the replies. A path prefix
// /mode/<flags>/ turns on failures: notools, novision, fail429=N, fail500=N, cut,
// slow=<ms>, chunkms=<ms>, cost=<usd>.
package model

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

//go:embed scripts/*.yaml
var builtinScripts embed.FS

// Script is one scripted conversation.
type Script struct {
	Name  string `yaml:"name"`
	Match struct {
		System string `yaml:"system"`
		User   string `yaml:"user"`
	} `yaml:"match"`
	Steps []Step `yaml:"steps"`
}

// Step is one model reply. The step index is the number of assistant turns after the last user message.
type Step struct {
	Text      string         `yaml:"text"`
	ToolCalls []ScriptCall   `yaml:"tool_calls"`
	JSON      map[string]any `yaml:"json"`
	DelayMS   int            `yaml:"delay_ms"`
	Usage     *struct {
		In  int64 `yaml:"in"`
		Out int64 `yaml:"out"`
	} `yaml:"usage"`
	Cost *float64 `yaml:"cost"`
	// Repeat makes the step answer every later turn too.
	Repeat bool `yaml:"repeat"`
}

// ScriptCall is one scripted tool call.
type ScriptCall struct {
	Name string         `yaml:"name"`
	Args map[string]any `yaml:"args"`
}

// Recorded is one request that the fake received.
type Recorded struct {
	API      string
	Flags    string
	Model    string
	System   string
	LastUser string
	Messages []NMessage
	Tools    []string
	Schema   bool
	Images   int
	At       time.Time
}

// Server is the fake model.
type Server struct {
	mu       sync.Mutex
	scripts  []Script
	requests []Recorded
	counters map[string]int
	// FirstToken records when each response's first token was written, for latency tests.
	firstToken []time.Time
	// Hold, when set, blocks each streamed response before its first token until it is closed.
	Hold chan struct{}
}

// New loads the built-in scripts plus scripts from extra.
func New(extra ...fs.FS) (*Server, error) {
	s := &Server{counters: map[string]int{}}
	sub, _ := fs.Sub(builtinScripts, "scripts")
	for _, f := range append([]fs.FS{sub}, extra...) {
		if err := s.load(f); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Server) load(f fs.FS) error {
	ents, err := fs.ReadDir(f, ".")
	if err != nil {
		return err
	}
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		b, err := fs.ReadFile(f, e.Name())
		if err != nil {
			return err
		}
		var sc Script
		if err := yaml.Unmarshal(b, &sc); err != nil {
			return fmt.Errorf("%s: %w", e.Name(), err)
		}
		if sc.Name == "" {
			sc.Name = strings.TrimSuffix(e.Name(), ".yaml")
		}
		s.scripts = append(s.scripts, sc)
	}
	return nil
}

// AddScript adds a script at run time (tests).
func (s *Server) AddScript(sc Script) {
	s.mu.Lock()
	s.scripts = append([]Script{sc}, s.scripts...)
	s.mu.Unlock()
}

// Requests returns a copy of the recorded requests.
func (s *Server) Requests() []Recorded {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Recorded(nil), s.requests...)
}

// FirstTokens returns when each streamed response sent its first token.
func (s *Server) FirstTokens() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Time(nil), s.firstToken...)
}

// Reset forgets requests and failure counters.
func (s *Server) Reset() {
	s.mu.Lock()
	s.requests, s.counters, s.firstToken = nil, map[string]int{}, nil
	s.mu.Unlock()
}

// NMessage is a normalized message.
type NMessage struct {
	Role       string
	Text       string
	Images     int
	ToolCalls  []string
	ToolResult string
}

// nreq is a request normalized across the three APIs.
type nreq struct {
	api      string
	model    string
	system   string
	messages []NMessage
	tools    []toolSpec
	schema   json.RawMessage
}

type toolSpec struct {
	Name   string
	Schema json.RawMessage
}

func (r nreq) lastUser() string {
	for i := len(r.messages) - 1; i >= 0; i-- {
		if r.messages[i].Role == "user" && r.messages[i].ToolResult == "" && r.messages[i].Text != "" {
			return r.messages[i].Text
		}
	}
	return ""
}

func (r nreq) lastToolResult() string {
	if n := len(r.messages); n > 0 && r.messages[n-1].ToolResult != "" {
		return r.messages[n-1].ToolResult
	}
	return ""
}

// stepIndex counts assistant turns after the last real user message.
func (r nreq) stepIndex() int {
	n := 0
	for i := len(r.messages) - 1; i >= 0; i-- {
		m := r.messages[i]
		if m.Role == "user" && m.ToolResult == "" {
			break
		}
		if m.Role == "assistant" {
			n++
		}
	}
	return n
}

func (r nreq) images() int {
	n := 0
	for _, m := range r.messages {
		n += m.Images
	}
	return n
}

type flags struct {
	raw      string
	noTools  bool
	noVision bool
	fail429  int
	fail500  int
	cut      bool
	slow     time.Duration
	chunk    time.Duration
	cost     *float64
}

func parseFlags(raw string) flags {
	f := flags{raw: raw}
	for _, p := range strings.Split(raw, ",") {
		k, v, _ := strings.Cut(p, "=")
		n, _ := strconv.Atoi(v)
		switch k {
		case "notools":
			f.noTools = true
		case "novision":
			f.noVision = true
		case "fail429":
			f.fail429 = n
		case "fail500":
			f.fail500 = n
		case "cut":
			f.cut = true
		case "slow":
			f.slow = time.Duration(n) * time.Millisecond
		case "chunkms":
			f.chunk = time.Duration(n) * time.Millisecond
		case "cost":
			c, _ := strconv.ParseFloat(v, 64)
			f.cost = &c
		}
	}
	return f
}

// ServeHTTP routes /[mode/<flags>/]v1/{models,chat/completions,responses,messages}.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	fl := flags{}
	if strings.HasPrefix(path, "/mode/") {
		rest := strings.TrimPrefix(path, "/mode/")
		i := strings.Index(rest, "/")
		if i < 0 {
			http.NotFound(w, r)
			return
		}
		fl = parseFlags(rest[:i])
		path = rest[i:]
	}
	path = strings.TrimPrefix(path, "/v1")
	switch {
	case r.Method == http.MethodGet && path == "/models":
		s.models(w, fl)
	case r.Method == http.MethodPost && path == "/chat/completions":
		s.handle(w, r, "openai-chat", fl)
	case r.Method == http.MethodPost && path == "/responses":
		s.handle(w, r, "openai-responses", fl)
	case r.Method == http.MethodPost && path == "/messages":
		s.handle(w, r, "anthropic-messages", fl)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) models(w http.ResponseWriter, fl flags) {
	type m struct {
		ID            string            `json:"id"`
		ContextLength int               `json:"context_length"`
		Pricing       map[string]string `json:"pricing"`
	}
	data := []m{
		{ID: "fake/large", ContextLength: 200000, Pricing: map[string]string{"prompt": "0.000003", "completion": "0.000015"}},
		{ID: "fake/small", ContextLength: 4000, Pricing: map[string]string{"prompt": "0.0000005", "completion": "0.0000015"}},
	}
	if fl.noVision {
		data = data[1:]
	}
	writeJSON(w, 200, map[string]any{"data": data})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func apiError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": map[string]any{"message": msg, "type": "fake_error"}})
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request, api string, fl flags) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		apiError(w, 400, "bad JSON")
		return
	}
	req := normalize(api, body)
	rec := Recorded{API: api, Flags: fl.raw, Model: req.model, System: req.system, LastUser: req.lastUser(), Messages: req.messages, Schema: req.schema != nil, Images: req.images(), At: time.Now()}
	for _, t := range req.tools {
		rec.Tools = append(rec.Tools, t.Name)
	}
	s.mu.Lock()
	s.requests = append(s.requests, rec)
	key := fl.raw
	s.counters[key]++
	count := s.counters[key]
	s.mu.Unlock()

	if fl.fail429 > 0 && count <= fl.fail429 {
		apiError(w, 429, "rate limited by the fake model")
		return
	}
	if fl.fail500 > 0 && count <= fl.fail500 {
		apiError(w, 500, "the fake model failed")
		return
	}
	if fl.noTools && len(req.tools) > 0 && api != "anthropic-messages" {
		apiError(w, 400, "this model does not support tools")
		return
	}
	if fl.noTools && api == "anthropic-messages" && len(req.tools) > 0 && req.schema == nil {
		apiError(w, 400, "this model does not support tools")
		return
	}
	if fl.noVision && req.images() > 0 {
		apiError(w, 400, "this model does not support image input")
		return
	}
	step := s.reply(req)
	if step.DelayMS > 0 {
		time.Sleep(time.Duration(step.DelayMS) * time.Millisecond)
	}
	if fl.slow > 0 {
		time.Sleep(fl.slow)
	}
	if s.Hold != nil {
		<-s.Hold
	}
	in := int64(0)
	for _, m := range req.messages {
		in += int64(len(m.Text)+len(m.ToolResult)) / 4
	}
	in += int64(len(req.system))/4 + 1
	out := int64(len(step.Text))/4 + 1
	if step.Usage != nil {
		in, out = step.Usage.In, step.Usage.Out
	}
	cost := step.Cost
	if fl.cost != nil {
		cost = fl.cost
	}
	st := &streamer{w: w, api: api, model: req.model, chunk: fl.chunk, cut: fl.cut, onFirst: func() {
		s.mu.Lock()
		s.firstToken = append(s.firstToken, time.Now())
		s.mu.Unlock()
	}}
	st.write(step, req.schema != nil, in, out, cost)
}

// reply picks the scripted step for a request, or a built-in reply.
func (s *Server) reply(req nreq) Step {
	s.mu.Lock()
	scripts := append([]Script(nil), s.scripts...)
	s.mu.Unlock()
	user := req.lastUser()
	for _, sc := range scripts {
		if sc.Match.System == "" && sc.Match.User == "" {
			continue
		}
		if sc.Match.System != "" && !strings.Contains(req.system, sc.Match.System) {
			continue
		}
		if sc.Match.User != "" && !strings.Contains(user, sc.Match.User) {
			continue
		}
		i := req.stepIndex()
		if i < len(sc.Steps) {
			return s.fill(sc.Steps[i], req)
		}
		if n := len(sc.Steps); n > 0 && sc.Steps[n-1].Repeat {
			return s.fill(sc.Steps[n-1], req)
		}
		return Step{Text: "Done."}
	}
	return s.builtin(req)
}

func (s *Server) fill(st Step, req nreq) Step {
	rep := strings.NewReplacer("{{user}}", req.lastUser(), "{{tool_result}}", req.lastToolResult())
	st.Text = rep.Replace(st.Text)
	calls := make([]ScriptCall, len(st.ToolCalls))
	for i, tc := range st.ToolCalls {
		args := map[string]any{}
		for k, v := range tc.Args {
			if s, ok := v.(string); ok {
				v = rep.Replace(s)
			}
			args[k] = v
		}
		calls[i] = ScriptCall{Name: tc.Name, Args: args}
	}
	st.ToolCalls = calls
	if st.JSON == nil && req.schema != nil && len(st.ToolCalls) == 0 {
		st.JSON = synth(req.schema)
	}
	return st
}

func (s *Server) builtin(req nreq) Step {
	if req.schema != nil {
		return Step{JSON: synth(req.schema)}
	}
	user := req.lastUser()
	if tr := req.lastToolResult(); tr != "" {
		return Step{Text: "The tool returned: " + tr}
	}
	if req.images() > 0 {
		return Step{Text: "Red."}
	}
	low := strings.ToLower(user)
	for _, t := range req.tools {
		if strings.Contains(low, "use the "+strings.ToLower(t.Name)+" tool") {
			return Step{ToolCalls: []ScriptCall{{Name: t.Name, Args: synthArgs(t.Schema, user)}}}
		}
	}
	if strings.Contains(low, "reply with the word ready") {
		return Step{Text: "ready"}
	}
	return Step{Text: "Fake reply: " + user}
}

// synth builds a value that matches a JSON Schema.
func synth(schema json.RawMessage) map[string]any {
	var sc map[string]any
	_ = json.Unmarshal(schema, &sc)
	v, _ := synthValue(sc).(map[string]any)
	if v == nil {
		v = map[string]any{}
	}
	return v
}

func synthValue(sc map[string]any) any {
	if enum, ok := sc["enum"].([]any); ok && len(enum) > 0 {
		return enum[0]
	}
	t, _ := sc["type"].(string)
	if ts, ok := sc["type"].([]any); ok && len(ts) > 0 {
		t, _ = ts[0].(string)
	}
	switch t {
	case "object":
		out := map[string]any{}
		props, _ := sc["properties"].(map[string]any)
		keys := make([]string, 0, len(props))
		for k := range props {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			p, _ := props[k].(map[string]any)
			out[k] = synthValue(p)
		}
		return out
	case "array":
		return []any{}
	case "integer":
		return 1
	case "number":
		return 1.0
	case "boolean":
		return true
	case "null":
		return nil
	default:
		return "ok"
	}
}

func synthArgs(schema json.RawMessage, user string) map[string]any {
	args := synth(schema)
	// Fill the first string property with the last word before "then", or the last word.
	var sc map[string]any
	_ = json.Unmarshal(schema, &sc)
	props, _ := sc["properties"].(map[string]any)
	for k, p := range props {
		pm, _ := p.(map[string]any)
		if pm["type"] == "string" {
			words := strings.Fields(strings.NewReplacer(",", " ", ".", " ").Replace(user))
			for i, w := range words {
				if strings.EqualFold(w, "for") && i+1 < len(words) {
					args[k] = words[i+1]
				}
			}
		}
	}
	return args
}
