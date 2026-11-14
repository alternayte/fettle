//go:build contract

package models_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
	"github.com/alternayte/fettle/internal/models"
)

func fake(t *testing.T) (*fakemodel.Server, string) {
	t.Helper()
	f, err := fakemodel.New()
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv.URL
}

var apiTypes = []string{models.APIOpenAIChat, models.APIOpenAIResponses, models.APIAnthropic}

func TestSCN_MOD_003_AdaptersParseStreamsToolsSchemaImagesUsage(t *testing.T) {
	f, base := fake(t)
	f.AddScript(fakemodel.Script{Name: "tools", Match: struct {
		System string `yaml:"system"`
		User   string `yaml:"user"`
	}{User: "[[tool-test]]"}, Steps: []fakemodel.Step{{Text: "Let me look.", ToolCalls: []fakemodel.ScriptCall{{Name: "lookup", Args: map[string]any{"q": "fettle", "n": 2}}}}}})
	for _, api := range apiTypes {
		t.Run(api, func(t *testing.T) {
			ad, _ := models.AdapterFor(api)
			c := models.Conn{APIType: api, BaseURL: base + "/v1", Key: "k-test"}
			ctx := context.Background()
			hc := http.DefaultClient

			var deltas []string
			r, err := ad.Call(ctx, hc, c, models.Request{Model: "fake/large", System: "sys", Messages: []models.Message{{Role: "user", Parts: []models.Part{models.TextPart("hello there friend")}}}}, func(d string) { deltas = append(deltas, d) })
			if err != nil {
				t.Fatal(err)
			}
			if r.Text != "Fake reply: hello there friend" || len(deltas) < 3 || strings.Join(deltas, "") != r.Text {
				t.Fatalf("text %q deltas %q", r.Text, deltas)
			}
			if r.Usage.In <= 0 || r.Usage.Out <= 0 {
				t.Fatalf("usage %+v", r.Usage)
			}

			tool := models.ToolDef{Name: "lookup", Description: "Look up.", Schema: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"},"n":{"type":"integer"}}}`)}
			r, err = ad.Call(ctx, hc, c, models.Request{Model: "fake/large", Tools: []models.ToolDef{tool}, Messages: []models.Message{{Role: "user", Parts: []models.Part{models.TextPart("[[tool-test]]")}}}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(r.ToolCalls) != 1 || r.ToolCalls[0].Name != "lookup" || r.ToolCalls[0].ID == "" {
				t.Fatalf("tool calls %+v", r.ToolCalls)
			}
			var args map[string]any
			if err := json.Unmarshal(r.ToolCalls[0].Args, &args); err != nil || args["q"] != "fettle" || args["n"] != float64(2) {
				t.Fatalf("args %s", r.ToolCalls[0].Args)
			}
			// Round trip: the tool result goes back and the model answers.
			msgs := []models.Message{
				{Role: "user", Parts: []models.Part{models.TextPart("what is fettle")}},
				{Role: "assistant", ToolCalls: r.ToolCalls},
				{Role: "tool", ToolCallID: r.ToolCalls[0].ID, ToolName: "lookup", Parts: []models.Part{models.TextPart("a pottery word")}},
			}
			r, err = ad.Call(ctx, hc, c, models.Request{Model: "fake/large", Tools: []models.ToolDef{tool}, Messages: msgs}, nil)
			if err != nil || r.Text != "The tool returned: a pottery word" {
				t.Fatalf("round trip %q %v", r.Text, err)
			}

			schema := json.RawMessage(`{"type":"object","properties":{"step":{"type":"string","enum":["direct","single"]},"confidence":{"type":"number"}},"required":["step","confidence"]}`)
			r, err = ad.Call(ctx, hc, c, models.Request{Model: "fake/large", Output: &models.Schema{Name: "route", Schema: schema}, Messages: []models.Message{{Role: "user", Parts: []models.Part{models.TextPart("route this")}}}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			var out struct {
				Step       string  `json:"step"`
				Confidence float64 `json:"confidence"`
			}
			if err := json.Unmarshal(r.JSON, &out); err != nil || out.Step != "direct" || out.Confidence != 1 {
				t.Fatalf("json %s %v", r.JSON, err)
			}

			r, err = ad.Call(ctx, hc, c, models.Request{Model: "fake/large", Messages: []models.Message{{Role: "user", Parts: []models.Part{{Type: "image", MediaType: "image/png", Data: []byte{1, 2, 3}}, models.TextPart("colour?")}}}}, nil)
			if err != nil || r.Text != "Red." {
				t.Fatalf("image %q %v", r.Text, err)
			}
			last := f.Requests()[len(f.Requests())-1]
			if last.Images != 1 || last.API != api {
				t.Fatalf("recorded %+v", last)
			}

			// Reported cost (OpenRouter style) is parsed for the OpenAI types.
			cc := c
			cc.BaseURL = base + "/mode/cost=0.0123/v1"
			r, err = ad.Call(ctx, hc, cc, models.Request{Model: "fake/large", Messages: []models.Message{{Role: "user", Parts: []models.Part{models.TextPart("hi")}}}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if api != models.APIAnthropic && (r.Usage.Cost == nil || *r.Usage.Cost != 0.0123) {
				t.Fatalf("cost %+v", r.Usage)
			}
		})
	}
}

func TestSCN_MOD_002_ProviderWithoutToolsOrVision(t *testing.T) {
	_, base := fake(t)
	for _, api := range apiTypes {
		t.Run(api, func(t *testing.T) {
			c := models.Conn{APIType: api, BaseURL: base + "/mode/notools,novision/v1", Key: "k"}
			s := models.Probe(context.Background(), http.DefaultClient, c, "fake/small")
			byKey := map[string]models.CheckResult{}
			for _, ch := range s.Checks {
				byKey[ch.Key] = ch
			}
			if !byKey["plain"].Pass || !byKey["schema"].Pass {
				t.Fatalf("plain or schema failed: %+v", s.Checks)
			}
			if byKey["tools"].Pass || s.Tools {
				t.Fatal("tool check passed on a provider without tools")
			}
			if byKey["vision"].Pass || s.Vision {
				t.Fatal("vision marked on a provider without vision")
			}
			full := models.Probe(context.Background(), http.DefaultClient, models.Conn{APIType: api, BaseURL: base + "/v1"}, "fake/large")
			if !full.Tools || !full.Vision || len(full.Checks) != 4 {
				t.Fatalf("full provider: %+v", full)
			}
		})
	}
}

func TestSCN_MOD_005_FallbackAfterThree429(t *testing.T) {
	_, base := fake(t)
	models.RetryBackoff = func(int) time.Duration { return time.Millisecond }
	targets := []models.Resolved{
		{Name: "one", Target: models.Target{Model: "fake/large"}, Conn: models.Conn{APIType: models.APIOpenAIChat, BaseURL: base + "/mode/fail429=3/v1"}},
		{Name: "two", Target: models.Target{Model: "fake/small"}, Conn: models.Conn{APIType: models.APIOpenAIChat, BaseURL: base + "/v1"}},
	}
	var sw []models.Switch
	r, idx, err := models.CallWithFallback(context.Background(), http.DefaultClient, targets, models.Request{Messages: []models.Message{{Role: "user", Parts: []models.Part{models.TextPart("hi")}}}}, nil, func(s models.Switch) { sw = append(sw, s) })
	if err != nil || idx != 1 || r.Text == "" {
		t.Fatalf("idx %d err %v", idx, err)
	}
	if len(sw) != 1 || !strings.Contains(sw[0].Reason, "429") || sw[0].From != "one/fake/large" || sw[0].To != "two/fake/small" {
		t.Fatalf("switch %+v", sw)
	}

	// A stream cut after its first token is not retried and does not switch.
	cut := []models.Resolved{
		{Name: "one", Target: models.Target{Model: "m"}, Conn: models.Conn{APIType: models.APIOpenAIChat, BaseURL: base + "/mode/cut/v1"}},
		{Name: "two", Target: models.Target{Model: "m"}, Conn: models.Conn{APIType: models.APIOpenAIChat, BaseURL: base + "/v1"}},
	}
	sw = nil
	var got string
	_, idx, err = models.CallWithFallback(context.Background(), http.DefaultClient, cut, models.Request{Messages: []models.Message{{Role: "user", Parts: []models.Part{models.TextPart("tell me a long story please")}}}}, func(d string) { got += d }, func(s models.Switch) { sw = append(sw, s) })
	var ce *models.CallError
	if err == nil || !errors.As(err, &ce) || !ce.FirstToken || idx != 0 || len(sw) != 0 || got == "" {
		t.Fatalf("cut stream: err %v idx %d switches %d got %q", err, idx, len(sw), got)
	}
}
