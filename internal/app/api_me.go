package app

import (
	"context"
	"crypto/subtle"
	"net/http"

	"github.com/alternayte/fettle/internal/auth"
	"github.com/danielgtaylor/huma/v2"
)

// SpaceView is a space with the caller's role.
type SpaceView struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	TimeZone string `json:"time_zone"`
	Role     string `json:"role"`
}

// MeView is the caller.
type MeView struct {
	ID      string      `json:"id"`
	Email   string      `json:"email"`
	Name    string      `json:"name"`
	IsAdmin bool        `json:"is_admin"`
	Mode    string      `json:"mode"`
	Version string      `json:"version"`
	Spaces  []SpaceView `json:"spaces"`
	// Setup tells the UI what the first run still needs.
	Setup SetupState `json:"setup"`
}

// SetupState lists the open first-run steps.
type SetupState struct {
	NeedsModel bool `json:"needs_model"`
	NeedsName  bool `json:"needs_name"`
}

type meOut struct{ Body MeView }

type localAuthIn struct {
	Body struct {
		Token string `json:"token" minLength:"1"`
	}
}

type localAuthOut struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      struct {
		OK bool `json:"ok"`
	}
}

type spaceIn struct {
	SpacePath
	Body struct {
		Name     string `json:"name,omitempty" maxLength:"80"`
		TimeZone string `json:"time_zone,omitempty" maxLength:"64"`
	}
}

type spaceOut struct{ Body SpaceView }

func (a *App) registerMe(api huma.API) {
	huma.Register(api, op("local-auth", http.MethodPost, "/auth/local", "Exchange the local token for a UI cookie", "auth"), func(ctx context.Context, in *localAuthIn) (*localAuthOut, error) {
		if a.Cfg.Mode != ModeLocal || subtle.ConstantTimeCompare([]byte(in.Body.Token), []byte(a.LocalToken)) != 1 {
			return nil, huma.Error401Unauthorized("The local token is wrong.")
		}
		out := &localAuthOut{}
		out.SetCookie = http.Cookie{Name: auth.LocalCookie, Value: auth.CookieValue(a.LocalToken), Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 30 * 24 * 3600}
		out.Body.OK = true
		return out, nil
	})

	huma.Register(api, op("get-me", http.MethodGet, "/me", "The caller, the spaces and the first-run state", "auth"), func(ctx context.Context, _ *struct{}) (*meOut, error) {
		p := auth.From(ctx)
		u, err := a.Users.Get(ctx, p.UserID)
		if err != nil || u == nil {
			return nil, huma.Error401Unauthorized("Sign in first.")
		}
		ms, err := a.Spaces.ForUser(ctx, u.ID())
		if err != nil {
			return nil, err
		}
		v := MeView{ID: u.ID().String(), Email: u.Email, Name: u.Name, IsAdmin: u.IsAdmin, Mode: string(a.Cfg.Mode), Version: a.Cfg.Version, Spaces: []SpaceView{}}
		for _, m := range ms {
			v.Spaces = append(v.Spaces, SpaceView{ID: m.Space.ID().String(), Name: m.Space.Name, TimeZone: m.Space.TimeZone, Role: m.Role})
		}
		v.Setup.NeedsName = u.Name == ""
		if len(ms) > 0 {
			ps, err := a.Models.List(ctx, ms[0].Space.ID())
			if err != nil {
				return nil, err
			}
			ready := false
			for _, pr := range ps {
				if pr.Smoke.Tools {
					ready = true
				}
			}
			roles, _ := a.Models.Roles(ctx, ms[0].Space.ID(), nil)
			hasOwner := false
			for _, r := range roles {
				if r.Role == "owner" && len(r.Targets) > 0 {
					hasOwner = true
				}
			}
			v.Setup.NeedsModel = !ready || !hasOwner
		}
		return &meOut{Body: v}, nil
	})

	huma.Register(api, op("update-me", http.MethodPatch, "/me", "Change the caller's name", "auth"), func(ctx context.Context, in *struct {
		Body struct {
			Name string `json:"name" minLength:"1" maxLength:"80"`
		}
	}) (*meOut, error) {
		p := auth.From(ctx)
		if err := a.Users.SetName(ctx, p.UserID, in.Body.Name); err != nil {
			return nil, err
		}
		u, _ := a.Users.Get(ctx, p.UserID)
		return &meOut{Body: MeView{ID: u.ID().String(), Email: u.Email, Name: u.Name, IsAdmin: u.IsAdmin, Mode: string(a.Cfg.Mode), Version: a.Cfg.Version, Spaces: []SpaceView{}}}, nil
	})

	huma.Register(api, op("get-space", http.MethodGet, "/spaces/{space}", "A space", "spaces"), func(ctx context.Context, in *SpacePath) (*spaceOut, error) {
		_, role, err := a.ctxSpace(ctx, in.Space, false)
		if err != nil {
			return nil, err
		}
		sp, err := a.Spaces.Get(ctx, in.Space)
		if err != nil {
			return nil, apiError(err)
		}
		return &spaceOut{Body: SpaceView{ID: sp.ID().String(), Name: sp.Name, TimeZone: sp.TimeZone, Role: role}}, nil
	})

	huma.Register(api, op("update-space", http.MethodPatch, "/spaces/{space}", "Change the name or time zone of a space", "spaces"), func(ctx context.Context, in *spaceIn) (*spaceOut, error) {
		p, role, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		sp, err := a.Spaces.Update(ctx, actorOf(p), in.Space, in.Body.Name, in.Body.TimeZone)
		if err != nil {
			return nil, apiError(err)
		}
		return &spaceOut{Body: SpaceView{ID: sp.ID().String(), Name: sp.Name, TimeZone: sp.TimeZone, Role: role}}, nil
	})
}
