package app_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/alternayte/fettle/internal/apptest"
	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
	fakeweb "github.com/alternayte/fettle/internal/fakes/web"
	"github.com/alternayte/fettle/internal/web"
)

// TestSCN_WEB_001_BotWithNoZoneSearchesAndReadsTheWeb: a bot with no zone answers with
// web_search and web_fetch from the fake provider, cites a URL, and each search is one
// usage row with its cost.
func TestSCN_WEB_001_BotWithNoZoneSearchesAndReadsTheWeb(t *testing.T) {
	srv := httptest.NewServer(fakeweb.New())
	t.Cleanup(srv.Close)
	t.Setenv(web.HostsEnv, "docs.test="+srv.Listener.Addr().String()+",search.test="+srv.Listener.Addr().String())
	old := web.Endpoints
	web.Endpoints = map[string]string{web.KindTavily: "http://search.test", web.KindExa: "http://search.test", web.KindBrave: "http://search.test"}
	t.Cleanup(func() { web.Endpoints = old })

	e := apptest.Start(t, "sqlite")
	e.SetupModel("openai-chat", "", 128000)
	chief := e.Chief()
	if _, err := e.App.DB.Engine.Exec(context.Background(), "UPDATE bots SET zone_id = NULL WHERE id = $1", chief.ID); err != nil {
		t.Fatal(err)
	}

	// With no provider, the run has web_fetch but not web_search.
	e.Fake.AddScript(fakemodel.Script{Name: "web-off", Match: fakemodel.Match{User: "Which tools do you have"}, Steps: say("I can read pages.")})
	run, _ := e.Post(chief.ChatID, "Which tools do you have?")
	e.WaitRun(run, "done")
	tools, system := lastRequest(e, "Which tools do you have")
	if slices.Contains(tools, "web_search") || !slices.Contains(tools, "web_fetch") || strings.Contains(system, "Use web_search and web_fetch for facts") {
		t.Fatalf("with no provider: tools %v", tools)
	}

	// Settings, Web: the key is write-only, and the test search answers.
	var set struct {
		Provider struct {
			Kind     string `json:"kind"`
			HasKey   bool   `json:"has_key"`
			KeyLast4 string `json:"key_last4"`
		} `json:"provider"`
	}
	code, body := e.Do("PUT", e.SP("/web-search"), map[string]any{"kind": "tavily"}, nil)
	if code != 422 || !strings.Contains(body, "API key") {
		t.Fatalf("Tavily with no key = %d %s", code, body)
	}
	e.MustDo("PUT", e.SP("/web-search"), map[string]any{"kind": "tavily", "key": fakeweb.SearchKey}, &set)
	if set.Provider.Kind != "tavily" || !set.Provider.HasKey || set.Provider.KeyLast4 != "4417" {
		t.Fatalf("provider %+v", set.Provider)
	}
	var tested struct {
		Results []web.Hit `json:"results"`
	}
	e.MustDo("POST", e.SP("/web-search/test"), map[string]any{"query": "desk"}, &tested)
	if len(tested.Results) == 0 || !strings.HasPrefix(tested.Results[0].URL, "http://docs.test/") {
		t.Fatalf("test search %+v", tested)
	}

	e.Fake.AddScript(fakemodel.Script{Name: "web-on", Match: fakemodel.Match{User: "What top does the Alpha Desk have"}, Steps: []fakemodel.Step{
		toolStep("web_search", map[string]any{"query": "Alpha Desk top", "count": 3}),
		toolStep("web_fetch", map[string]any{"url": "http://docs.test/alpha"}),
		{Text: "The Alpha Desk has a bamboo top. Source: http://docs.test/alpha"},
	}})
	run, _ = e.Post(chief.ChatID, "What top does the Alpha Desk have?")
	r := e.WaitRun(run, "done")
	tools, system = lastRequest(e, "What top does the Alpha Desk have")
	if !slices.Contains(tools, "web_search") || !strings.Contains(system, "Use web_search and web_fetch for facts; use the browser for sign-in, clicks and forms.") {
		t.Fatalf("with a provider: tools %v", tools)
	}
	results := map[string]string{}
	for _, ev := range r.Events {
		if ev.Type != "tool_call" {
			continue
		}
		var d struct {
			Tool, Result, Origin, Error string
		}
		_ = json.Unmarshal(ev.Data, &d)
		if d.Error != "" || d.Origin != "web" {
			t.Fatalf("%s: origin %q error %q", d.Tool, d.Origin, d.Error)
		}
		results[d.Tool] = d.Result
	}
	if !strings.Contains(results["web_search"], "http://docs.test/alpha") || !strings.Contains(results["web_fetch"], "bamboo top") {
		t.Fatalf("tool results %v", results)
	}
	msgs := e.Messages(chief.ChatID)
	if b, _ := msgs[len(msgs)-1]["body"].(string); !strings.Contains(b, "http://docs.test/alpha") {
		t.Fatalf("answer %q", b)
	}

	// Two searches (the test and the run), one usage row each, under the provider name.
	var view struct {
		Rows []struct {
			Key      string  `json:"key"`
			Calls    int64   `json:"calls"`
			Searches int64   `json:"searches"`
			CostUSD  float64 `json:"cost_usd"`
		} `json:"rows"`
	}
	e.MustDo("GET", e.SP("/usage?by=model"), nil, &view)
	found := false
	for _, row := range view.Rows {
		if row.Key == "tavily" {
			found = true
			if row.Searches != 2 || row.Calls != 2 || row.CostUSD < 0.0159 || row.CostUSD > 0.0161 {
				t.Fatalf("tavily usage %+v", row)
			}
		}
	}
	if !found {
		t.Fatalf("no tavily row in %+v", view.Rows)
	}
	if r.Line.CostUSD < 0.008 {
		t.Fatalf("the run cost %v does not hold the search", r.Line.CostUSD)
	}

	// Without a provider, web_search is off again.
	e.MustDo("DELETE", e.SP("/web-search"), nil, nil)
	var none struct {
		Provider *struct{} `json:"provider"`
	}
	e.MustDo("GET", e.SP("/web-search"), nil, &none)
	if none.Provider != nil {
		t.Fatal("the provider is still set")
	}
}

// lastRequest returns the tools and the system prompt of the last model request for a user
// message.
func lastRequest(e *apptest.Env, user string) ([]string, string) {
	e.T.Helper()
	var tools []string
	system := ""
	for _, r := range e.Fake.Requests() {
		if strings.Contains(r.LastUser, user) && len(r.Tools) > 0 {
			tools, system = r.Tools, r.System
		}
	}
	return tools, system
}
