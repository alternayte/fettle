package app

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/auth"
	"github.com/alternayte/fettle/internal/settings"
	"github.com/alternayte/fettle/internal/usage"
)

// SettingView is one setting.
type SettingView struct {
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	Revision  int             `json:"revision"`
	IsDefault bool            `json:"is_default"`
	Doc       string          `json:"doc"`
}

// AuditView is one audit row.
type AuditView struct {
	At        time.Time         `json:"at"`
	ActorKind string            `json:"actor_kind"`
	ActorID   string            `json:"actor_id"`
	Action    string            `json:"action"`
	Target    string            `json:"target"`
	Details   map[string]string `json:"details"`
}

func (a *App) registerSettings(api huma.API) {
	huma.Register(api, op("list-settings", http.MethodGet, "/spaces/{space}/settings", "Space settings with revisions", "settings"), func(ctx context.Context, in *SpacePath) (*struct {
		Body struct {
			Settings []SettingView `json:"settings"`
		}
	}, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, true); err != nil {
			return nil, err
		}
		out := &struct {
			Body struct {
				Settings []SettingView `json:"settings"`
			}
		}{}
		out.Body.Settings = []SettingView{}
		for _, d := range settings.Defs() {
			if d.Scope != "space" {
				continue
			}
			v, err := a.Settings.Get(ctx, &in.Space, d.Key)
			if err != nil {
				return nil, err
			}
			out.Body.Settings = append(out.Body.Settings, SettingView{Key: d.Key, Value: v.Value, Revision: v.Revision, IsDefault: v.IsDefault, Doc: d.Doc})
		}
		return out, nil
	})

	huma.Register(api, op("set-setting", http.MethodPut, "/spaces/{space}/settings/{key}", "Change a space setting", "settings"), func(ctx context.Context, in *struct {
		SpacePath
		Key  string `path:"key"`
		Body struct {
			Value any `json:"value"`
		}
	}) (*struct{ Body SettingView }, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		if in.Key == "approval.mode" {
			return nil, huma.Error422UnprocessableEntity("The approval mode changes only through its card. Use PUT /spaces/{space}/approval-mode.")
		}
		v, err := a.Settings.Set(ctx, actorOf(p), &in.Space, in.Key, in.Body.Value)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		return &struct{ Body SettingView }{Body: SettingView{Key: v.Key, Value: v.Value, Revision: v.Revision}}, nil
	})

	huma.Register(api, op("setting-history", http.MethodGet, "/spaces/{space}/settings/{key}/history", "The change history of a setting", "settings"), func(ctx context.Context, in *struct {
		SpacePath
		Key string `path:"key"`
	}) (*struct {
		Body struct {
			History []settings.HistoryRow `json:"history"`
		}
	}, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, true); err != nil {
			return nil, err
		}
		h, err := a.Settings.History(ctx, &in.Space, in.Key)
		if err != nil {
			return nil, apiError(err)
		}
		out := &struct {
			Body struct {
				History []settings.HistoryRow `json:"history"`
			}
		}{}
		out.Body.History = h
		if out.Body.History == nil {
			out.Body.History = []settings.HistoryRow{}
		}
		return out, nil
	})

	huma.Register(api, op("usage-view", http.MethodGet, "/spaces/{space}/usage", "Cost view grouped by run, bot, step, role, model, day or month", "usage"), func(ctx context.Context, in *struct {
		SpacePath
		By   string `query:"by" enum:"run,bot,step,role,model,day,month" default:"day"`
		Days int    `query:"days" minimum:"1" maximum:"400" default:"30"`
	}) (*struct {
		Body struct {
			Rows []usage.Row `json:"rows"`
		}
	}, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, true); err != nil {
			return nil, err
		}
		rows, err := a.Usage.View(ctx, in.Space, in.By, time.Now().UTC().AddDate(0, 0, -in.Days))
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		out := &struct {
			Body struct {
				Rows []usage.Row `json:"rows"`
			}
		}{}
		out.Body.Rows = rows
		if out.Body.Rows == nil {
			out.Body.Rows = []usage.Row{}
		}
		return out, nil
	})

	huma.Register(api, op("list-audit", http.MethodGet, "/spaces/{space}/audit", "The audit log of a space, newest first. The API has no route that changes it.", "audit"), func(ctx context.Context, in *struct {
		SpacePath
		Action string `query:"action"`
		Limit  int    `query:"limit" minimum:"1" maximum:"500" default:"200"`
	}) (*struct {
		Body struct {
			Entries []AuditView `json:"entries"`
		}
	}, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, true); err != nil {
			return nil, err
		}
		rows, err := a.Audit.List(ctx, audit.Filter{SpaceID: &in.Space, Action: in.Action, Limit: in.Limit})
		if err != nil {
			return nil, err
		}
		out := &struct {
			Body struct {
				Entries []AuditView `json:"entries"`
			}
		}{}
		out.Body.Entries = []AuditView{}
		for _, r := range rows {
			out.Body.Entries = append(out.Body.Entries, AuditView{At: r.CreatedAt(), ActorKind: r.ActorKind, ActorID: r.ActorID, Action: r.Action, Target: r.Target, Details: r.Details})
		}
		return out, nil
	})
}

func (a *App) registerAdmin(api huma.API) {
	requireAdmin := func(ctx context.Context) (*auth.Principal, error) {
		p := auth.From(ctx)
		if p == nil || !p.IsAdmin {
			return nil, huma.Error403Forbidden("Only an instance admin can do this.")
		}
		return p, nil
	}
	type telemetryView struct {
		Enabled  bool            `json:"enabled"`
		Endpoint string          `json:"endpoint"`
		Fields   []string        `json:"fields"`
		Preview  TelemetryCounts `json:"preview"`
	}
	view := func(ctx context.Context) telemetryView {
		return telemetryView{Enabled: a.Telemetry.Enabled(ctx), Endpoint: a.Telemetry.URL, Fields: TelemetryFields, Preview: a.Telemetry.Counts(ctx)}
	}
	huma.Register(api, op("get-telemetry", http.MethodGet, "/admin/telemetry", "Telemetry state and the exact counts it sends", "admin"), func(ctx context.Context, _ *struct{}) (*struct{ Body telemetryView }, error) {
		if _, err := requireAdmin(ctx); err != nil {
			return nil, err
		}
		return &struct{ Body telemetryView }{Body: view(ctx)}, nil
	})
	huma.Register(api, op("set-telemetry", http.MethodPut, "/admin/telemetry", "Turn telemetry on or off", "admin"), func(ctx context.Context, in *struct {
		Body struct {
			Enabled bool `json:"enabled"`
		}
	}) (*struct{ Body telemetryView }, error) {
		p, err := requireAdmin(ctx)
		if err != nil {
			return nil, err
		}
		if _, err := a.Settings.Set(ctx, actorOf(p), nil, "telemetry.enabled", in.Body.Enabled); err != nil {
			return nil, err
		}
		return &struct{ Body telemetryView }{Body: view(ctx)}, nil
	})
}
