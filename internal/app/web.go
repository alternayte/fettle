package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/alternayte/drel"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/agent"
	"github.com/alternayte/fettle/internal/envelopes"
	"github.com/alternayte/fettle/internal/usage"
	"github.com/alternayte/fettle/internal/web"
)

// setupWeb adds web_search and web_fetch. Both run in the server with the server HTTP
// client, so they need no computer. web_search is present only when the space has a
// search provider; web_fetch needs no provider.
func (a *App) setupWeb() {
	a.Web = web.New(a.DB.Engine, a.Vault, a.Audit)
	r := a.Agent.Tools
	r.Add(agent.Tool{
		Name:        "web_search",
		Description: "Search the web. Returns titles, URLs and snippets. Read a result with web_fetch, and cite the URLs that you use.",
		Class:       envelopes.ClassRead,
		Schema:      json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","minLength":1,"maxLength":400},"count":{"type":"integer","minimum":1,"maximum":20,"description":"Number of results, 5 when not set."}},"required":["query"],"additionalProperties":false}`),
		Available: func(ctx context.Context, spaceID, _ uuid.UUID) bool {
			_, err := a.Web.Get(ctx, spaceID)
			return err == nil
		},
		Run: func(ctx context.Context, env *agent.ToolEnv, args json.RawMessage) (agent.ToolOutput, error) {
			var in struct {
				Query string `json:"query"`
				Count int    `json:"count"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return agent.ToolOutput{}, err
			}
			res, err := a.webSearch(ctx, env.SpaceID, &env.BotID, &env.RunID, in.Query, in.Count)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			return agent.ToolOutput{Text: web.Format(in.Query, res.Hits), Origin: agent.OriginWeb}, nil
		},
	})
	r.Add(agent.Tool{
		Name:        "web_fetch",
		Description: "Read one web page or PDF by its URL and get its text as Markdown, at most 64 KB. Use the browser for pages that need sign-in, clicks or forms.",
		Class:       envelopes.ClassRead,
		Schema:      json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","minLength":1,"maxLength":4000}},"required":["url"],"additionalProperties":false}`),
		Target: func(args json.RawMessage) (string, string) {
			var in struct {
				URL string `json:"url"`
			}
			_ = json.Unmarshal(args, &in)
			return in.URL, ""
		},
		Run: func(ctx context.Context, env *agent.ToolEnv, args json.RawMessage) (agent.ToolOutput, error) {
			var in struct {
				URL string `json:"url"`
			}
			if err := json.Unmarshal(args, &in); err != nil {
				return agent.ToolOutput{}, err
			}
			pol, err := a.webPolicy(ctx, env.SpaceID, env.BotID)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			text, err := web.Fetch(ctx, a.HTTP, pol, in.URL)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			return agent.ToolOutput{Text: text, Origin: agent.OriginWeb}, nil
		},
	})
}

// webPolicy returns the network policy of a bot's zone. A bot with no zone gets the open
// policy.
func (a *App) webPolicy(ctx context.Context, spaceID, botID uuid.UUID) (web.Policy, error) {
	b, err := a.Bots.Get(ctx, spaceID, botID)
	if err != nil {
		return web.Policy{}, err
	}
	if b.ZoneID == nil {
		return web.Policy{}, nil
	}
	z, err := a.Zones.Get(ctx, spaceID, *b.ZoneID)
	if err != nil {
		return web.Policy{}, err
	}
	return web.Policy{Allowlist: z.Network == "allowlist", Hosts: z.AllowHosts}, nil
}

// webSearch runs one search with the provider of the space and records one usage row.
// A search in a run adds its cost to the run, so it counts toward the envelope budget.
func (a *App) webSearch(ctx context.Context, spaceID uuid.UUID, botID, runID *uuid.UUID, query string, count int) (web.Result, error) {
	p, err := a.Web.Get(ctx, spaceID)
	if errors.Is(err, web.ErrNoProvider) {
		return web.Result{}, errors.New("web_search is off, because no search provider is set in Settings, Web. Use web_fetch or the browser")
	}
	if err != nil {
		return web.Result{}, err
	}
	conn, err := a.Web.Conn(ctx, p)
	if err != nil {
		return web.Result{}, err
	}
	res, err := web.Search(ctx, web.Client(a.HTTP), conn, query, count)
	if err != nil {
		return web.Result{}, err
	}
	ev := &usage.UsageEvent{SpaceID: spaceID, Kind: "search", BotID: botID, RunID: runID, ModelName: p.Kind, Quantity: 1, Unit: "calls", CostUSD: res.CostUSD, Reported: p.Kind == web.KindExa}
	if botID != nil {
		if b, err := a.Bots.Get(ctx, spaceID, *botID); err == nil {
			ev.ZoneID = b.ZoneID
		}
	}
	err = a.DB.Engine.WithTx(ctx, func(ctx context.Context) error {
		if err := a.Usage.Record(ctx, ev); err != nil {
			return err
		}
		if runID == nil || res.CostUSD == 0 {
			return nil
		}
		_, err := drel.MustFromContext(ctx).Exec(ctx, "UPDATE runs SET cost_usd = cost_usd + $1 WHERE id = $2", res.CostUSD, *runID)
		return err
	})
	return res, err
}

// WebSearchView is the search provider of a space without its key.
type WebSearchView struct {
	Kind     string `json:"kind" enum:"tavily,exa,searxng,brave"`
	Name     string `json:"name"`
	BaseURL  string `json:"base_url"`
	HasKey   bool   `json:"has_key"`
	KeyLast4 string `json:"key_last4"`
}

func (a *App) webSearchView(ctx context.Context, p *web.SearchProvider) *WebSearchView {
	v := &WebSearchView{Kind: p.Kind, Name: web.Names[p.Kind], BaseURL: p.BaseURL}
	if p.SecretItemID != nil {
		v.HasKey = true
		if it, err := a.Vault.Get(ctx, p.SpaceID, *p.SecretItemID); err == nil {
			v.KeyLast4 = it.Last4
		}
	}
	return v
}

type webSearchOut struct {
	Body struct {
		Provider *WebSearchView `json:"provider,omitempty" doc:"Absent when the space has no search provider."`
	}
}

func (a *App) registerWeb(api huma.API) {
	huma.Register(api, op("get-web-search", http.MethodGet, "/spaces/{space}/web-search", "The web search provider of the space", "web"), func(ctx context.Context, in *SpacePath) (*webSearchOut, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, true); err != nil {
			return nil, err
		}
		out := &webSearchOut{}
		p, err := a.Web.Get(ctx, in.Space)
		if errors.Is(err, web.ErrNoProvider) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		out.Body.Provider = a.webSearchView(ctx, p)
		return out, nil
	})

	huma.Register(api, op("set-web-search", http.MethodPut, "/spaces/{space}/web-search", "Set the web search provider of the space. The key goes into the vault.", "web"), func(ctx context.Context, in *struct {
		SpacePath
		Body struct {
			Kind    string `json:"kind,omitempty" enum:"tavily,exa,searxng,brave"`
			BaseURL string `json:"base_url,omitempty" maxLength:"500" doc:"The SearXNG URL. Other kinds ignore it."`
			Key     string `json:"key,omitempty" maxLength:"4000" doc:"Write only. Empty keeps the stored key of the same kind."`
		}
	}) (*webSearchOut, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		sp, err := a.Web.Set(ctx, actorOf(p), in.Space, web.Input{Kind: in.Body.Kind, BaseURL: in.Body.BaseURL, Key: in.Body.Key})
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		out := &webSearchOut{}
		out.Body.Provider = a.webSearchView(ctx, sp)
		return out, nil
	})

	huma.Register(api, op("delete-web-search", http.MethodDelete, "/spaces/{space}/web-search", "Remove the web search provider and its key. web_search is then off.", "web"), func(ctx context.Context, in *SpacePath) (*struct{}, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		if err := a.Web.Delete(ctx, actorOf(p), in.Space); err != nil {
			if errors.Is(err, web.ErrNoProvider) {
				return nil, huma.Error404NotFound(err.Error())
			}
			return nil, err
		}
		return nil, nil
	})

	huma.Register(api, op("test-web-search", http.MethodPost, "/spaces/{space}/web-search/test", "Run one search with the provider of the space. It counts as one search in usage.", "web"), func(ctx context.Context, in *struct {
		SpacePath
		Body struct {
			Query string `json:"query,omitempty" maxLength:"400"`
		}
	}) (*struct {
		Body struct {
			Results []web.Hit `json:"results"`
			CostUSD float64   `json:"cost_usd"`
			MS      int64     `json:"ms"`
		}
	}, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, true); err != nil {
			return nil, err
		}
		start := time.Now()
		res, err := a.webSearch(ctx, in.Space, nil, nil, in.Body.Query, 5)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(a.Vault.Redactor.String(ctx, in.Space, err.Error()))
		}
		out := &struct {
			Body struct {
				Results []web.Hit `json:"results"`
				CostUSD float64   `json:"cost_usd"`
				MS      int64     `json:"ms"`
			}
		}{}
		out.Body.Results = res.Hits
		if out.Body.Results == nil {
			out.Body.Results = []web.Hit{}
		}
		out.Body.CostUSD, out.Body.MS = res.CostUSD, time.Since(start).Milliseconds()
		return out, nil
	})
}
