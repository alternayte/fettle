package app

import (
	"context"
	authall "github.com/alternayte/auth-all"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/alternayte/fettle/internal/auth"
)

// EnterpriseFeatures are the features of the ee build (§18.3), in the order the admin page
// shows them.
var EnterpriseFeatures = []string{"sso", "scim", "audit_export", "policy_packs", "org_budgets", "admin_console"}

// FeatureView is one Enterprise feature and whether it works on this server.
type FeatureView struct {
	Key       string `json:"key"`
	Available bool   `json:"available" doc:"The feature works: the build has it and the licence names it."`
	Writable  bool   `json:"writable" doc:"The feature can change data; false after the licence grace ends."`
}

// LicenseView is the licence of an Enterprise build.
type LicenseView struct {
	State    string `json:"state" enum:"missing,invalid,valid,grace,read_only"`
	Customer string `json:"customer,omitempty"`
	Seats    int    `json:"seats,omitempty"`
	Expires  string `json:"expires,omitempty"`
	DaysLeft int    `json:"days_left"`
	Reason   string `json:"reason,omitempty"`
}

// EditionView is the edition of this build (§18.1).
type EditionView struct {
	Name     string        `json:"name" enum:"community,enterprise"`
	License  *LicenseView  `json:"license,omitempty"`
	Features []FeatureView `json:"features"`
}

// Extension is the code of another edition (ee). Core never imports it (§18.1): the ee
// build registers it from its own package, and core calls it through these methods.
type Extension interface {
	// Install sets the hooks of the app (policy packs, budgets, allowlists).
	Install(a *App) error
	// Edition reports the licence and the features.
	Edition(ctx context.Context) EditionView
	// Public mounts routes that need no Fettle sign-in (SSO, SCIM with its own token).
	Public(r chi.Router)
	// Routes registers API routes behind the sign-in.
	Routes(api huma.API)
	// Tick runs on the maintenance tick (audit export, retention).
	Tick(ctx context.Context)
}

var extensions []Extension

// SignInExtension is the optional part of an Extension that adds sign-in methods to server
// mode, such as an OpenID Connect provider. Core calls it before it builds the sign-in.
type SignInExtension interface {
	SignInOptions(cfg Config) ([]authall.Option, error)
}

func signInOptions(cfg Config) ([]authall.Option, error) {
	var out []authall.Option
	for _, x := range extensions {
		if s, ok := x.(SignInExtension); ok {
			o, err := s.SignInOptions(cfg)
			if err != nil {
				return nil, err
			}
			out = append(out, o...)
		}
	}
	return out, nil
}

// Register adds an edition's extension. The ee package calls it from init.
func Register(x Extension) { extensions = append(extensions, x) }

// edition returns the edition of this build.
func (a *App) edition(ctx context.Context) EditionView {
	for _, x := range extensions {
		return x.Edition(ctx)
	}
	v := EditionView{Name: "community", Features: []FeatureView{}}
	for _, f := range EnterpriseFeatures {
		v.Features = append(v.Features, FeatureView{Key: f})
	}
	return v
}

func (a *App) installExtensions() error {
	for _, x := range extensions {
		if err := x.Install(a); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) extensionTick(ctx context.Context) {
	for _, x := range extensions {
		x.Tick(ctx)
	}
}

func (a *App) registerEdition(api huma.API) {
	huma.Register(api, op("get-edition", http.MethodGet, "/admin/edition", "The edition of this build, its licence and the Enterprise features (instance admins)", "admin"), func(ctx context.Context, _ *struct{}) (*struct{ Body EditionView }, error) {
		if p := auth.From(ctx); p == nil || !p.IsAdmin {
			return nil, huma.Error403Forbidden("Only an instance admin can do this.")
		}
		return &struct{ Body EditionView }{Body: a.edition(ctx)}, nil
	})
}
