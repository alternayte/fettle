package models

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/alternayte/drel"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/vault"
)

// Errors of the models service.
var (
	ErrNotFound      = errors.New("model provider not found")
	ErrNoToolsOwner  = errors.New("this provider failed the tool call check, so it cannot fill the owner role")
	ErrUntestedOwner = errors.New("test this provider before you use it for the owner role")
	ErrRoleNoTargets = errors.New("no model is set for this role")
	ErrUnknownRole   = errors.New("unknown model role")
	// ErrNoTranscribeAPI means a provider cannot transcribe audio.
	ErrNoTranscribeAPI = errors.New("the transcribe role needs an OpenAI-compatible provider with /audio/transcriptions")
	ErrInvalidBaseURL  = errors.New("the base URL must start with http:// or https://")
)

// Service manages model providers and roles of a space.
type Service struct {
	e     *drel.Engine
	vault *vault.Vault
	audit *audit.Log
	HTTP  *http.Client
	// AllowModel is an instance-wide model allowlist (an Enterprise policy pack). It
	// returns an error for a model that the pack does not allow. Nil allows every model.
	AllowModel func(ctx context.Context, model string) error
}

// NewService returns the models service. hc must honour the proxy and CA settings.
func NewService(e *drel.Engine, v *vault.Vault, a *audit.Log, hc *http.Client) *Service {
	return &Service{e: e, vault: v, audit: a, HTTP: hc}
}

// ProviderInput creates or changes a provider. An empty Key keeps the stored key.
type ProviderInput struct {
	Name       string
	Preset     string
	APIType    string
	BaseURL    string
	AuthStyle  string
	AuthHeader string
	Key        string
	Headers    map[string]string
}

func (in *ProviderInput) applyPreset() {
	if p, ok := PresetByID(in.Preset); ok {
		if in.APIType == "" {
			in.APIType = p.APIType
		}
		if in.BaseURL == "" {
			in.BaseURL = p.BaseURL
		}
		if in.AuthStyle == "" {
			in.AuthStyle = p.AuthStyle
		}
		if in.Name == "" {
			in.Name = p.Name
		}
	}
	if in.AuthStyle == "" {
		in.AuthStyle = "bearer"
	}
}

func (in ProviderInput) validate() error {
	if strings.TrimSpace(in.Name) == "" {
		return errors.New("name is required")
	}
	if _, err := AdapterFor(in.APIType); err != nil {
		return err
	}
	u, err := url.Parse(in.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ErrInvalidBaseURL
	}
	switch in.AuthStyle {
	case "bearer", "x_api_key", "none":
	case "header":
		if in.AuthHeader == "" {
			return errors.New("the header authentication style needs a header name")
		}
	default:
		return fmt.Errorf("unknown authentication style %q", in.AuthStyle)
	}
	return nil
}

// Create stores a provider. Its key goes into the vault.
func (s *Service) Create(ctx context.Context, actor audit.Actor, spaceID uuid.UUID, in ProviderInput) (*ModelProvider, error) {
	in.applyPreset()
	if err := in.validate(); err != nil {
		return nil, err
	}
	p := &ModelProvider{SpaceID: spaceID, Name: strings.TrimSpace(in.Name), Preset: in.Preset, APIType: in.APIType, BaseURL: strings.TrimRight(in.BaseURL, "/"), AuthStyle: in.AuthStyle, AuthHeader: in.AuthHeader, Headers: in.Headers, Models: []ModelInfo{}}
	if p.Headers == nil {
		p.Headers = map[string]string{}
	}
	err := s.e.WithTx(ctx, func(ctx context.Context) error {
		if in.Key != "" {
			item, err := s.vault.Create(ctx, actor, spaceID, vault.Input{Name: "model key: " + p.Name, Kind: vault.KindSecret, Value: in.Key})
			if err != nil {
				return err
			}
			id := item.ID()
			p.SecretItemID = &id
		}
		drel.Repo(drel.MustFromContext(ctx), ModelProviderMeta).Add(p)
		return s.audit.Record(ctx, actor, &spaceID, "model_provider.create", "model_provider:"+p.ID().String(), map[string]string{"name": p.Name, "api_type": p.APIType, "base_url": p.BaseURL})
	})
	if err != nil {
		return nil, err
	}
	return p, nil
}

// Get returns one provider of a space.
func (s *Service) Get(ctx context.Context, spaceID, id uuid.UUID) (*ModelProvider, error) {
	p, err := drel.NewRepository(s.e, ModelProviderMeta).Where(ModelProviders.ID.Eq(id)).Where(ModelProviders.SpaceID.Eq(spaceID)).FirstOrNil(ctx)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, ErrNotFound
	}
	return p, nil
}

// List returns the providers of a space.
func (s *Service) List(ctx context.Context, spaceID uuid.UUID) ([]*ModelProvider, error) {
	return drel.NewRepository(s.e, ModelProviderMeta).Where(ModelProviders.SpaceID.Eq(spaceID)).OrderBy(ModelProviders.Name.Asc()).All(ctx)
}

// Update changes a provider. A change of URL, API type or key clears the test result.
func (s *Service) Update(ctx context.Context, actor audit.Actor, spaceID, id uuid.UUID, in ProviderInput) (*ModelProvider, error) {
	in.applyPreset()
	if err := in.validate(); err != nil {
		return nil, err
	}
	var out *ModelProvider
	err := s.e.WithTx(ctx, func(ctx context.Context) error {
		repo := drel.Repo(drel.MustFromContext(ctx), ModelProviderMeta)
		p, err := repo.Where(ModelProviders.ID.Eq(id)).Where(ModelProviders.SpaceID.Eq(spaceID)).FirstOrNil(ctx)
		if err != nil {
			return err
		}
		if p == nil {
			return ErrNotFound
		}
		changed := p.BaseURL != strings.TrimRight(in.BaseURL, "/") || p.APIType != in.APIType || in.Key != ""
		p.Name, p.Preset, p.APIType, p.BaseURL, p.AuthStyle, p.AuthHeader = strings.TrimSpace(in.Name), in.Preset, in.APIType, strings.TrimRight(in.BaseURL, "/"), in.AuthStyle, in.AuthHeader
		if in.Headers != nil {
			p.Headers = in.Headers
		}
		if in.Key != "" {
			if p.SecretItemID != nil {
				if _, err := s.vault.Update(ctx, actor, spaceID, *p.SecretItemID, vault.Input{Value: in.Key}); err != nil {
					return err
				}
			} else {
				item, err := s.vault.Create(ctx, actor, spaceID, vault.Input{Name: "model key: " + p.Name, Kind: vault.KindSecret, Value: in.Key})
				if err != nil {
					return err
				}
				iid := item.ID()
				p.SecretItemID = &iid
			}
		}
		if changed {
			p.Smoke, p.SmokeAt = Smoke{}, nil
		}
		out = p
		return s.audit.Record(ctx, actor, &spaceID, "model_provider.update", "model_provider:"+id.String(), map[string]string{"name": p.Name})
	})
	return out, err
}

// Delete removes a provider and its key.
func (s *Service) Delete(ctx context.Context, actor audit.Actor, spaceID, id uuid.UUID) error {
	return s.e.WithTx(ctx, func(ctx context.Context) error {
		p, err := s.Get(ctx, spaceID, id)
		if err != nil {
			return err
		}
		tx := drel.MustFromContext(ctx)
		if _, err := tx.Exec(ctx, "DELETE FROM model_providers WHERE id = $1", id); err != nil {
			return err
		}
		if p.SecretItemID != nil {
			if err := s.vault.Delete(ctx, actor, spaceID, *p.SecretItemID); err != nil && !errors.Is(err, vault.ErrNotFound) {
				return err
			}
		}
		return s.audit.Record(ctx, actor, &spaceID, "model_provider.delete", "model_provider:"+id.String(), map[string]string{"name": p.Name})
	})
}

// Conn resolves the connection of a provider, with its key from the vault.
func (s *Service) Conn(ctx context.Context, p *ModelProvider) (Conn, error) {
	c := Conn{APIType: p.APIType, BaseURL: p.BaseURL, AuthStyle: p.AuthStyle, AuthHeader: p.AuthHeader, Headers: p.Headers}
	if p.SecretItemID != nil {
		k, err := s.vault.Reveal(ctx, p.SpaceID, *p.SecretItemID)
		if err != nil {
			return Conn{}, fmt.Errorf("read the provider key: %w", err)
		}
		c.Key = k
		s.vault.Redactor.RegisterGlobal("model key "+p.Name, k)
	}
	return c, nil
}

// Test lists the models when possible and runs the four checks with model.
func (s *Service) Test(ctx context.Context, actor audit.Actor, spaceID, id uuid.UUID, model string) (*ModelProvider, error) {
	p, err := s.Get(ctx, spaceID, id)
	if err != nil {
		return nil, err
	}
	c, err := s.Conn(ctx, p)
	if err != nil {
		return nil, err
	}
	ad, _ := AdapterFor(p.APIType)
	lctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	if list, err := ad.ListModels(lctx, s.HTTP, c); err == nil && len(list) > 0 {
		p.Models = list
	}
	cancel()
	if model == "" {
		if pr, ok := PresetByID(p.Preset); ok && pr.DefaultModel != "" {
			model = pr.DefaultModel
		} else if len(p.Models) > 0 {
			model = p.Models[0].ID
		} else {
			return nil, errors.New("name a model to test")
		}
	}
	smoke := Probe(ctx, s.HTTP, c, model)
	for i := range smoke.Checks {
		smoke.Checks[i].Detail = s.vault.Redactor.Global(smoke.Checks[i].Detail)
	}
	now := time.Now().UTC()
	err = s.e.WithTx(ctx, func(ctx context.Context) error {
		repo := drel.Repo(drel.MustFromContext(ctx), ModelProviderMeta)
		row, err := repo.Where(ModelProviders.ID.Eq(id)).FirstOrNil(ctx)
		if err != nil || row == nil {
			return ErrNotFound
		}
		row.Smoke, row.SmokeAt, row.Models = smoke, &now, p.Models
		p = row
		return s.audit.Record(ctx, actor, &spaceID, "model_provider.test", "model_provider:"+id.String(), map[string]string{"model": model, "tools": fmt.Sprint(smoke.Tools), "vision": fmt.Sprint(smoke.Vision)})
	})
	return p, err
}

// Roles returns the space roles, or the bot overrides when botID is set.
func (s *Service) Roles(ctx context.Context, spaceID uuid.UUID, botID *uuid.UUID) ([]*ModelRole, error) {
	key := ""
	if botID != nil {
		key = botID.String()
	}
	return drel.NewRepository(s.e, ModelRoleMeta).Where(ModelRoles.SpaceID.Eq(spaceID)).Where(ModelRoles.BotKey.Eq(key)).All(ctx)
}

// SetRole sets the ordered targets of a role at space level (botID nil) or for a bot.
// A provider that failed the tool call check cannot fill the owner role (REQ-MOD-002).
func (s *Service) SetRole(ctx context.Context, actor audit.Actor, spaceID uuid.UUID, botID *uuid.UUID, role string, targets []Target) error {
	known := false
	for _, r := range AllRoles {
		if r == role {
			known = true
		}
	}
	if !known {
		return ErrUnknownRole
	}
	for i := range targets {
		p, err := s.Get(ctx, spaceID, targets[i].ProviderID)
		if err != nil {
			return err
		}
		if strings.TrimSpace(targets[i].Model) == "" {
			return errors.New("each target needs a model")
		}
		if s.AllowModel != nil {
			if err := s.AllowModel(ctx, targets[i].Model); err != nil {
				return err
			}
		}
		if role == RoleTranscribe && !transcribeAPI(p.APIType) {
			return ErrNoTranscribeAPI
		}
		if role == RoleOwner {
			if p.SmokeAt == nil {
				return ErrUntestedOwner
			}
			if !p.Smoke.Tools {
				return ErrNoToolsOwner
			}
		}
		for _, m := range p.Models {
			if m.ID == targets[i].Model {
				if targets[i].ContextWindow == 0 {
					targets[i].ContextWindow = m.ContextWindow
				}
				if targets[i].PriceIn == 0 && targets[i].PriceOut == 0 {
					targets[i].PriceIn, targets[i].PriceOut = m.PriceIn, m.PriceOut
				}
			}
		}
		if targets[i].ContextWindow == 0 {
			targets[i].ContextWindow = 128000
		}
	}
	key := ""
	if botID != nil {
		key = botID.String()
	}
	return s.e.WithTx(ctx, func(ctx context.Context) error {
		repo := drel.Repo(drel.MustFromContext(ctx), ModelRoleMeta)
		row, err := repo.Where(ModelRoles.SpaceID.Eq(spaceID)).Where(ModelRoles.BotKey.Eq(key)).Where(ModelRoles.Role.Eq(role)).FirstOrNil(ctx)
		if err != nil {
			return err
		}
		if len(targets) == 0 {
			if row != nil {
				if _, err := drel.MustFromContext(ctx).Exec(ctx, "DELETE FROM model_roles WHERE id = $1", row.ID()); err != nil {
					return err
				}
			}
		} else if row == nil {
			repo.Add(&ModelRole{SpaceID: spaceID, BotID: botID, BotKey: key, Role: role, Targets: targets})
		} else {
			row.Targets = targets
		}
		return s.audit.Record(ctx, actor, &spaceID, "model_role.set", "model_role:"+role, map[string]string{"bot": key, "targets": fmt.Sprint(len(targets))})
	})
}

// Resolve returns the targets of a role for a bot: the bot override when it has one,
// else the space role. Each target carries its connection.
func (s *Service) Resolve(ctx context.Context, spaceID uuid.UUID, botID *uuid.UUID, role string) ([]Resolved, error) {
	var row *ModelRole
	repo := drel.NewRepository(s.e, ModelRoleMeta)
	if botID != nil {
		r, err := repo.Where(ModelRoles.SpaceID.Eq(spaceID)).Where(ModelRoles.BotKey.Eq(botID.String())).Where(ModelRoles.Role.Eq(role)).FirstOrNil(ctx)
		if err != nil {
			return nil, err
		}
		row = r
	}
	if row == nil {
		r, err := repo.Where(ModelRoles.SpaceID.Eq(spaceID)).Where(ModelRoles.BotKey.Eq("")).Where(ModelRoles.Role.Eq(role)).FirstOrNil(ctx)
		if err != nil {
			return nil, err
		}
		row = r
	}
	if row == nil && role != RoleOwner && role != RoleTranscribe {
		// Roles without targets use the owner targets. The transcribe role has none then.
		return s.Resolve(ctx, spaceID, botID, RoleOwner)
	}
	if row == nil || len(row.Targets) == 0 {
		return nil, ErrRoleNoTargets
	}
	out := make([]Resolved, 0, len(row.Targets))
	var refused error
	for _, t := range row.Targets {
		if s.AllowModel != nil {
			if err := s.AllowModel(ctx, t.Model); err != nil {
				refused = err
				continue
			}
		}
		p, err := s.Get(ctx, spaceID, t.ProviderID)
		if err != nil {
			continue
		}
		c, err := s.Conn(ctx, p)
		if err != nil {
			return nil, err
		}
		out = append(out, Resolved{Target: t, Conn: c, Name: p.Name})
	}
	if len(out) == 0 {
		if refused != nil {
			return nil, refused
		}
		return nil, ErrRoleNoTargets
	}
	return out, nil
}

// Vision reports whether the first owner target of a bot passed the image check.
func (s *Service) Vision(ctx context.Context, spaceID uuid.UUID, t Target) bool {
	p, err := s.Get(ctx, spaceID, t.ProviderID)
	return err == nil && p.Smoke.Vision && p.Smoke.Model == t.Model
}
