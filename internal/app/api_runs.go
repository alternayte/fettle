package app

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/alternayte/drel"
	"github.com/alternayte/fettle/internal/agent"
	"github.com/alternayte/fettle/internal/runs"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
)

// EventView is one journal event for the timeline. Large payloads show their preview.
type EventView struct {
	Seq       int64           `json:"seq"`
	At        time.Time       `json:"at"`
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data"`
	Truncated bool            `json:"truncated"`
}

// RunView is a run with its line and, on request, its events.
type RunView struct {
	Line        agent.RunLine `json:"line"`
	BotName     string        `json:"bot_name"`
	Kind        string        `json:"kind"`
	ParentRunID string        `json:"parent_run_id,omitempty"`
	Goal        string        `json:"goal"`
	TokensIn    int64         `json:"tokens_in"`
	TokensOut   int64         `json:"tokens_out"`
	StartedAt   *time.Time    `json:"started_at"`
	FinishedAt  *time.Time    `json:"finished_at"`
	Children    []string      `json:"children"`
	Events      []EventView   `json:"events"`
}

type runPath struct {
	SpacePath
	Run uuid.UUID `path:"run"`
}

func (a *App) runView(ctx context.Context, r *runs.Run) RunView {
	line, _ := a.Agent.RunLineFor(ctx, r)
	v := RunView{Line: line, Kind: r.Kind, TokensIn: r.TokensIn, TokensOut: r.TokensOut, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt, Children: []string{}, Events: []EventView{}}
	if r.ParentRunID != nil {
		v.ParentRunID = r.ParentRunID.String()
	}
	if b, err := a.Bots.Get(ctx, r.SpaceID, r.BotID); err == nil {
		v.BotName = b.Name
	}
	if e, err := a.Envs.Get(ctx, r.SpaceID, r.EnvelopeID); err == nil {
		v.Goal = e.Goal
	}
	return v
}

func (a *App) registerRuns(api huma.API) {
	huma.Register(api, op("list-runs", http.MethodGet, "/spaces/{space}/runs", "Recent owner runs of a space", "runs"), func(ctx context.Context, in *struct {
		SpacePath
		Bot   string `query:"bot"`
		Limit int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
	}) (*struct {
		Body struct {
			Runs []RunView `json:"runs"`
		}
	}, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, false); err != nil {
			return nil, err
		}
		q := drel.NewRepository(a.DB.Engine, runs.RunMeta).Where(runs.Runs.SpaceID.Eq(in.Space)).Where(runs.Runs.Kind.Eq("owner"))
		if in.Bot != "" {
			id, err := uuid.Parse(in.Bot)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity("bot must be an ID")
			}
			q = q.Where(runs.Runs.BotID.Eq(id))
		}
		list, err := q.OrderBy(runs.Runs.CreatedAt.Desc()).Limit(in.Limit).All(ctx)
		if err != nil {
			return nil, err
		}
		out := &struct {
			Body struct {
				Runs []RunView `json:"runs"`
			}
		}{}
		out.Body.Runs = []RunView{}
		for _, r := range list {
			out.Body.Runs = append(out.Body.Runs, a.runView(ctx, r))
		}
		return out, nil
	})

	huma.Register(api, op("get-run", http.MethodGet, "/spaces/{space}/runs/{run}", "A run with its timeline events", "runs"), func(ctx context.Context, in *struct {
		runPath
		After int64 `query:"after" doc:"Return events after this sequence number."`
	}) (*struct{ Body RunView }, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, false); err != nil {
			return nil, err
		}
		r, err := a.Runs.Get(ctx, in.Space, in.Run)
		if err != nil {
			return nil, apiError(err)
		}
		v := a.runView(ctx, r)
		children, err := a.Runs.Children(ctx, r.ID())
		if err != nil {
			return nil, err
		}
		for _, c := range children {
			v.Children = append(v.Children, c.ID().String())
		}
		evs, err := a.Journal.Page(ctx, r.ID(), in.After, 10000)
		if err != nil {
			return nil, err
		}
		for _, e := range evs {
			v.Events = append(v.Events, EventView{Seq: e.Seq, At: e.At, Type: e.Type, Data: json.RawMessage(e.Data), Truncated: e.PayloadKey != nil})
		}
		return &struct{ Body RunView }{Body: v}, nil
	})

	huma.Register(api, op("stop-run", http.MethodPost, "/spaces/{space}/runs/{run}/stop", "Stop a run and its sub-agent runs", "runs"), func(ctx context.Context, in *runPath) (*struct{}, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, false)
		if err != nil {
			return nil, err
		}
		return nil, apiError(a.Agent.Stop(ctx, actorOf(p), in.Space, in.Run))
	})
}
