package app

import (
	"context"
	"net/http"
	"time"

	"github.com/alternayte/fettle/internal/models"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
)

// ProviderView is a model provider without its key.
type ProviderView struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Preset     string             `json:"preset"`
	APIType    string             `json:"api_type"`
	BaseURL    string             `json:"base_url"`
	AuthStyle  string             `json:"auth_style"`
	AuthHeader string             `json:"auth_header"`
	HasKey     bool               `json:"has_key"`
	KeyLast4   string             `json:"key_last4"`
	Headers    map[string]string  `json:"headers"`
	Models     []models.ModelInfo `json:"models"`
	Smoke      models.Smoke       `json:"smoke"`
	TestedAt   *time.Time         `json:"tested_at"`
}

func (a *App) providerView(ctx context.Context, p *models.ModelProvider) ProviderView {
	v := ProviderView{ID: p.ID().String(), Name: p.Name, Preset: p.Preset, APIType: p.APIType, BaseURL: p.BaseURL, AuthStyle: p.AuthStyle, AuthHeader: p.AuthHeader, Headers: p.Headers, Models: p.Models, Smoke: p.Smoke, TestedAt: p.SmokeAt}
	if v.Headers == nil {
		v.Headers = map[string]string{}
	}
	if v.Models == nil {
		v.Models = []models.ModelInfo{}
	}
	if v.Smoke.Checks == nil {
		v.Smoke.Checks = []models.CheckResult{}
	}
	if p.SecretItemID != nil {
		v.HasKey = true
		if it, err := a.Vault.Get(ctx, p.SpaceID, *p.SecretItemID); err == nil {
			v.KeyLast4 = it.Last4
		}
	}
	return v
}

type providerBody struct {
	Name       string            `json:"name,omitempty" maxLength:"80"`
	Preset     string            `json:"preset,omitempty"`
	APIType    string            `json:"api_type,omitempty" enum:"openai-chat,openai-responses,anthropic-messages"`
	BaseURL    string            `json:"base_url,omitempty" maxLength:"500"`
	AuthStyle  string            `json:"auth_style,omitempty" enum:"bearer,x_api_key,header,none"`
	AuthHeader string            `json:"auth_header,omitempty" maxLength:"100"`
	Key        string            `json:"key,omitempty" maxLength:"4000" doc:"Write only. The API never returns it."`
	Headers    map[string]string `json:"headers,omitempty"`
}

func (b providerBody) input() models.ProviderInput {
	return models.ProviderInput{Name: b.Name, Preset: b.Preset, APIType: b.APIType, BaseURL: b.BaseURL, AuthStyle: b.AuthStyle, AuthHeader: b.AuthHeader, Key: b.Key, Headers: b.Headers}
}

type providerPath struct {
	SpacePath
	Provider uuid.UUID `path:"provider"`
}

type providerOut struct{ Body ProviderView }

// RoleView is the targets of one role.
type RoleView struct {
	Role    string          `json:"role"`
	BotID   string          `json:"bot_id,omitempty"`
	Targets []models.Target `json:"targets"`
}

func (a *App) registerModels(api huma.API) {
	huma.Register(api, op("list-model-presets", http.MethodGet, "/model-presets", "Provider presets for the form", "models"), func(ctx context.Context, _ *struct{}) (*struct {
		Body struct {
			Presets []models.Preset `json:"presets"`
		}
	}, error) {
		out := &struct {
			Body struct {
				Presets []models.Preset `json:"presets"`
			}
		}{}
		out.Body.Presets = models.Presets
		return out, nil
	})

	huma.Register(api, op("list-model-providers", http.MethodGet, "/spaces/{space}/model-providers", "List model providers", "models"), func(ctx context.Context, in *SpacePath) (*struct {
		Body struct {
			Providers []ProviderView `json:"providers"`
		}
	}, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, true); err != nil {
			return nil, err
		}
		ps, err := a.Models.List(ctx, in.Space)
		if err != nil {
			return nil, err
		}
		out := &struct {
			Body struct {
				Providers []ProviderView `json:"providers"`
			}
		}{}
		out.Body.Providers = []ProviderView{}
		for _, p := range ps {
			out.Body.Providers = append(out.Body.Providers, a.providerView(ctx, p))
		}
		return out, nil
	})

	huma.Register(api, op("create-model-provider", http.MethodPost, "/spaces/{space}/model-providers", "Add a model provider. The key goes into the vault.", "models"), func(ctx context.Context, in *struct {
		SpacePath
		Body providerBody
	}) (*providerOut, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		pr, err := a.Models.Create(ctx, actorOf(p), in.Space, in.Body.input())
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		return &providerOut{Body: a.providerView(ctx, pr)}, nil
	})

	huma.Register(api, op("update-model-provider", http.MethodPatch, "/spaces/{space}/model-providers/{provider}", "Change a model provider", "models"), func(ctx context.Context, in *struct {
		providerPath
		Body providerBody
	}) (*providerOut, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		pr, err := a.Models.Update(ctx, actorOf(p), in.Space, in.Provider, in.Body.input())
		if err != nil {
			if err == models.ErrNotFound {
				return nil, apiError(err)
			}
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		return &providerOut{Body: a.providerView(ctx, pr)}, nil
	})

	huma.Register(api, op("delete-model-provider", http.MethodDelete, "/spaces/{space}/model-providers/{provider}", "Delete a model provider and its key", "models"), func(ctx context.Context, in *providerPath) (*struct{}, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		return nil, apiError(a.Models.Delete(ctx, actorOf(p), in.Space, in.Provider))
	})

	huma.Register(api, op("test-model-provider", http.MethodPost, "/spaces/{space}/model-providers/{provider}/test", "Run the four provider checks", "models"), func(ctx context.Context, in *struct {
		providerPath
		Body struct {
			Model string `json:"model,omitempty" maxLength:"200"`
		}
	}) (*providerOut, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		pr, err := a.Models.Test(ctx, actorOf(p), in.Space, in.Provider, in.Body.Model)
		if err != nil {
			if err == models.ErrNotFound {
				return nil, apiError(err)
			}
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		return &providerOut{Body: a.providerView(ctx, pr)}, nil
	})

	huma.Register(api, op("list-model-roles", http.MethodGet, "/spaces/{space}/model-roles", "Role targets of the space, or of one bot", "models"), func(ctx context.Context, in *struct {
		SpacePath
		Bot string `query:"bot"`
	}) (*struct {
		Body struct {
			Roles []RoleView `json:"roles"`
		}
	}, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, false); err != nil {
			return nil, err
		}
		var bot *uuid.UUID
		if in.Bot != "" {
			id, err := uuid.Parse(in.Bot)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity("bot must be an ID")
			}
			bot = &id
		}
		rs, err := a.Models.Roles(ctx, in.Space, bot)
		if err != nil {
			return nil, err
		}
		out := &struct {
			Body struct {
				Roles []RoleView `json:"roles"`
			}
		}{}
		out.Body.Roles = []RoleView{}
		for _, r := range rs {
			rv := RoleView{Role: r.Role, Targets: r.Targets}
			if r.BotID != nil {
				rv.BotID = r.BotID.String()
			}
			out.Body.Roles = append(out.Body.Roles, rv)
		}
		return out, nil
	})

	huma.Register(api, op("set-model-role", http.MethodPut, "/spaces/{space}/model-roles/{role}", "Set the ordered targets of a role. An empty list removes a bot override.", "models"), func(ctx context.Context, in *struct {
		SpacePath
		Role string `path:"role" enum:"router,owner,verifier,computer_use,summarizer"`
		Body struct {
			BotID   string          `json:"bot_id,omitempty"`
			Targets []models.Target `json:"targets"`
		}
	}) (*struct{}, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		var bot *uuid.UUID
		if in.Body.BotID != "" {
			id, err := uuid.Parse(in.Body.BotID)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity("bot_id must be an ID")
			}
			if _, err := a.Bots.Get(ctx, in.Space, id); err != nil {
				return nil, apiError(err)
			}
			bot = &id
		}
		if err := a.Models.SetRole(ctx, actorOf(p), in.Space, bot, in.Role, in.Body.Targets); err != nil {
			if err == models.ErrNotFound {
				return nil, huma.Error422UnprocessableEntity("a target names a provider that does not exist")
			}
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		return nil, nil
	})
}
