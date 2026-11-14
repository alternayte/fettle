package app

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/routines"
)

// RoutineView is a routine without its webhook secret.
type RoutineView struct {
	ID         string     `json:"id"`
	BotID      string     `json:"bot_id"`
	Name       string     `json:"name"`
	Prompt     string     `json:"prompt"`
	Trigger    string     `json:"trigger" enum:"cron,webhook"`
	Cron       string     `json:"cron"`
	TZ         string     `json:"tz"`
	Notify     string     `json:"notify" enum:"always,on_failure,never"`
	Enabled    bool       `json:"enabled"`
	NextAt     *time.Time `json:"next_at,omitempty"`
	WebhookURL string     `json:"webhook_url,omitempty"`
}

// RoutineRunView is one firing or coalesced tick.
type RoutineRunView struct {
	DueAt      time.Time  `json:"due_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	EnvelopeID string     `json:"envelope_id,omitempty"`
	RunID      string     `json:"run_id,omitempty"`
	State      string     `json:"state,omitempty"`
	Coalesced  bool       `json:"coalesced"`
	Missed     bool       `json:"missed" doc:"The tick came due while Fettle was not running."`
	Source     string     `json:"source"`
}

type routineBody struct {
	BotID   uuid.UUID `json:"bot_id"`
	Name    string    `json:"name" minLength:"1" maxLength:"120"`
	Prompt  string    `json:"prompt" minLength:"1" maxLength:"8000"`
	Trigger string    `json:"trigger" enum:"cron,webhook"`
	Cron    string    `json:"cron,omitempty" maxLength:"120"`
	TZ      string    `json:"tz,omitempty" maxLength:"64"`
	Notify  string    `json:"notify,omitempty" enum:"always,on_failure,never"`
	Enabled bool      `json:"enabled"`
}

func (b routineBody) input() routines.Input {
	return routines.Input{BotID: b.BotID, Name: b.Name, Prompt: b.Prompt, Trigger: b.Trigger, Cron: b.Cron, TZ: b.TZ, Notify: b.Notify, Enabled: b.Enabled}
}

type RoutinePath struct {
	Space   uuid.UUID `path:"space" doc:"Space ID"`
	Routine uuid.UUID `path:"routine"`
}

func (a *App) routineView(r *routines.Routine) RoutineView {
	v := RoutineView{ID: r.ID().String(), BotID: r.BotID.String(), Name: r.Name, Prompt: r.Prompt, Trigger: r.Trigger, Cron: r.Cron, TZ: r.TZ, Notify: r.Notify, Enabled: r.Enabled, NextAt: r.NextAt}
	if r.Trigger == "webhook" {
		v.WebhookURL = a.URL() + "/hooks/" + r.ID().String()
	}
	return v
}

func routineError(err error) error {
	switch {
	case errors.Is(err, routines.ErrNotFound):
		return huma.Error404NotFound(err.Error())
	case errors.Is(err, routines.ErrLimit):
		return huma.Error409Conflict("This bot has 50 routines, the most it can have. Delete one first.")
	case errors.Is(err, routines.ErrBadCron), errors.Is(err, routines.ErrBadTZ), errors.Is(err, routines.ErrBadRoutine):
		return huma.Error422UnprocessableEntity(err.Error())
	}
	return err
}

func (a *App) registerRoutines(api huma.API) {
	type listOut struct {
		Body struct {
			Routines []RoutineView `json:"routines"`
		}
	}
	huma.Register(api, op("list-routines", http.MethodGet, "/spaces/{space}/routines", "List the routines of a space or of one bot", "routines"), func(ctx context.Context, in *struct {
		SpacePath
		Bot string `query:"bot"`
	}) (*listOut, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, false); err != nil {
			return nil, err
		}
		var bot *uuid.UUID
		if in.Bot != "" {
			id, err := uuid.Parse(in.Bot)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity("bot is not valid")
			}
			bot = &id
		}
		rs, err := a.Routines.List(ctx, in.Space, bot)
		if err != nil {
			return nil, err
		}
		out := &listOut{}
		out.Body.Routines = []RoutineView{}
		for _, r := range rs {
			out.Body.Routines = append(out.Body.Routines, a.routineView(r))
		}
		return out, nil
	})

	type createOut struct {
		Body struct {
			Routine RoutineView `json:"routine"`
			// Secret is the webhook secret. The API shows it once.
			Secret string `json:"secret,omitempty"`
		}
	}
	huma.Register(api, op("create-routine", http.MethodPost, "/spaces/{space}/routines", "Create a routine. A webhook routine returns its secret once.", "routines"), func(ctx context.Context, in *struct {
		SpacePath
		Body routineBody
	}) (*createOut, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		if _, err := a.Bots.Get(ctx, in.Space, in.Body.BotID); err != nil {
			return nil, apiError(err)
		}
		r, secret, err := a.Routines.Create(ctx, actorOf(p), in.Space, in.Body.input())
		if err != nil {
			return nil, routineError(err)
		}
		out := &createOut{}
		out.Body.Routine, out.Body.Secret = a.routineView(r), secret
		return out, nil
	})

	huma.Register(api, op("update-routine", http.MethodPatch, "/spaces/{space}/routines/{routine}", "Change a routine", "routines"), func(ctx context.Context, in *struct {
		RoutinePath
		Body routineBody
	}) (*struct{ Body RoutineView }, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		r, err := a.Routines.Update(ctx, actorOf(p), in.Space, in.Routine, in.Body.input())
		if err != nil {
			return nil, routineError(err)
		}
		return &struct{ Body RoutineView }{Body: a.routineView(r)}, nil
	})

	huma.Register(api, op("delete-routine", http.MethodDelete, "/spaces/{space}/routines/{routine}", "Delete a routine", "routines"), func(ctx context.Context, in *RoutinePath) (*struct{}, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		return nil, routineError(a.Routines.Delete(ctx, actorOf(p), in.Space, in.Routine))
	})

	huma.Register(api, op("run-routine", http.MethodPost, "/spaces/{space}/routines/{routine}/run", "Run a routine now", "routines"), func(ctx context.Context, in *RoutinePath) (*struct {
		Body struct {
			EnvelopeID string `json:"envelope_id"`
		}
	}, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		id, err := a.Routines.FireNow(ctx, actorOf(p), in.Space, in.Routine)
		if err != nil {
			return nil, routineError(err)
		}
		out := &struct {
			Body struct {
				EnvelopeID string `json:"envelope_id"`
			}
		}{}
		out.Body.EnvelopeID = id.String()
		return out, nil
	})

	type runsOut struct {
		Body struct {
			Runs []RoutineRunView `json:"runs"`
		}
	}
	huma.Register(api, op("routine-runs", http.MethodGet, "/spaces/{space}/routines/{routine}/runs", "The 20 newest firings of a routine, with missed and coalesced ticks", "routines"), func(ctx context.Context, in *RoutinePath) (*runsOut, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, false); err != nil {
			return nil, err
		}
		if _, err := a.Routines.Get(ctx, in.Space, in.Routine); err != nil {
			return nil, routineError(err)
		}
		rs, err := a.Routines.Runs(ctx, in.Routine)
		if err != nil {
			return nil, err
		}
		out := &runsOut{}
		out.Body.Runs = []RoutineRunView{}
		for _, rr := range rs {
			v := RoutineRunView{DueAt: rr.DueAt, StartedAt: rr.StartedAt, Coalesced: rr.Coalesced, Missed: rr.Missed, Source: rr.Source}
			if rr.EnvelopeID != nil {
				v.EnvelopeID = rr.EnvelopeID.String()
				if run, err := a.Runs.ForEnvelope(ctx, *rr.EnvelopeID); err == nil {
					v.RunID, v.State = run.ID().String(), run.Status
				}
			}
			out.Body.Runs = append(out.Body.Runs, v)
		}
		return out, nil
	})
}
