//go:build ee

// Package ee holds the Fettle Enterprise features (§18.3) under ee/LICENSE. The ee build
// (go build -tags ee) imports it; core never does (§18.1).
package ee

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/ee/licensekey"
	"github.com/alternayte/fettle/internal/app"
	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/auth"
	"github.com/alternayte/fettle/internal/policy"
	"github.com/alternayte/fettle/internal/settings"
	"github.com/alternayte/fettle/internal/zones"
)

func init() {
	settings.Register(settings.Def{Key: "ee.license", Scope: "instance", Default: "", Doc: "The Fettle Enterprise licence key. FETTLE_LICENSE overrides it."})
	settings.Register(settings.Def{Key: "ee.policy_pack", Scope: "instance", Default: "", Doc: "The organization policy pack as JSON: rules for every space, zone hosts, providers and models."})
	settings.Register(settings.Def{Key: "ee.org_month_usd", Scope: "instance", Default: 0.0, Doc: "The organization budget per calendar month in USD for all spaces. Zero has no limit."})
	settings.Register(settings.Def{Key: "ee.user_month_usd", Scope: "instance", Default: 0.0, Doc: "The quota per person per calendar month in USD, for the work that a person starts. Zero has no limit."})
	app.Register(&Enterprise{})
}

// testPublicKey lets tests of the ee build use their own issuer key. It is empty in a
// release; only a test sets it.
var testPublicKey ed25519.PublicKey

// SetTestPublicKey sets the issuer key for tests.
func SetTestPublicKey(k ed25519.PublicKey) { testPublicKey = k }

// Enterprise is the ee extension of the app.
type Enterprise struct {
	a *app.App

	mu    sync.Mutex
	cache *licensekey.Status
	until time.Time

	sso sso
	// groupMu orders the changes of groups and the memberships that follow them.
	groupMu sync.Mutex
}

// Pack is an organization policy pack (§18.3): rules for every space and allowlists.
type Pack struct {
	Rules []PackRule `json:"rules,omitempty"`
	// ZoneHosts, when set, makes every zone use an allowlist inside these host patterns.
	ZoneHosts []string `json:"zone_hosts,omitempty"`
	// Providers, when set, are the provider kinds that zones may use.
	Providers []string `json:"providers,omitempty"`
	// Models, when set, are the model name patterns (path.Match) that roles may use.
	Models []string `json:"models,omitempty"`
}

// PackRule is one approval rule for every space.
type PackRule struct {
	Tool      string `json:"tool,omitempty"`
	Connector string `json:"connector,omitempty"`
	Site      string `json:"site,omitempty"`
	Recipient string `json:"recipient,omitempty"`
	Effect    string `json:"effect"`
}

// Errors of the policy pack.
var (
	ErrZoneHosts = errors.New("the organization policy allows only zones with an allowlist inside its hosts")
	ErrProvider  = errors.New("the organization policy does not allow this provider")
	ErrModel     = errors.New("the organization policy does not allow this model")
	ErrReadOnly  = errors.New("the Enterprise licence expired more than 14 days ago; Enterprise settings are read-only")
	ErrFeature   = errors.New("the Enterprise licence does not include this feature")
)

func publicKey() ed25519.PublicKey {
	if testPublicKey != nil {
		return testPublicKey
	}
	k, err := base64.StdEncoding.DecodeString(licensekey.PublicKey)
	if err != nil {
		return nil
	}
	return k
}

// Status returns the licence status, cached for a minute.
func (x *Enterprise) Status(ctx context.Context) licensekey.Status {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.cache != nil && time.Now().Before(x.until) {
		return *x.cache
	}
	key := os.Getenv("FETTLE_LICENSE")
	if key == "" {
		key = x.a.Settings.String(ctx, nil, "ee.license")
	}
	st, _ := licensekey.Verify(key, publicKey(), time.Now())
	x.cache, x.until = &st, time.Now().Add(time.Minute)
	return st
}

func (x *Enterprise) forget() {
	x.mu.Lock()
	x.cache = nil
	x.mu.Unlock()
}

// Has reports whether a feature works; write is true for a change.
func (x *Enterprise) Has(ctx context.Context, feature string, write bool) bool {
	return x.Status(ctx).Has(feature, write)
}

// Pack returns the policy pack when the licence has policy packs.
func (x *Enterprise) Pack(ctx context.Context) Pack {
	var p Pack
	if !x.Has(ctx, licensekey.FeaturePolicy, false) {
		return p
	}
	_ = json.Unmarshal([]byte(x.a.Settings.String(ctx, nil, "ee.policy_pack")), &p)
	return p
}

// Install sets the hooks of the app.
func (x *Enterprise) Install(a *app.App) error {
	x.a = a
	x.forget()
	a.Policy.InstanceRules = func(ctx context.Context) []*policy.Rule {
		var out []*policy.Rule
		for _, r := range x.Pack(ctx).Rules {
			out = append(out, &policy.Rule{Tool: r.Tool, Connector: r.Connector, SitePattern: r.Site, RecipientPattern: r.Recipient, Effect: r.Effect, CreatedBy: "policy_pack"})
		}
		return out
	}
	prevZone := a.Zones.Policy
	a.Zones.Policy = func(ctx context.Context, in zones.Input) error {
		if prevZone != nil {
			if err := prevZone(ctx, in); err != nil {
				return err
			}
		}
		if err := x.checkZone(ctx, in); err != nil {
			return fmt.Errorf("%w: %w", zones.ErrPolicy, err)
		}
		return nil
	}
	a.Models.AllowModel = func(ctx context.Context, model string) error {
		p := x.Pack(ctx)
		if len(p.Models) == 0 {
			return nil
		}
		for _, pat := range p.Models {
			if ok, _ := path.Match(strings.ToLower(pat), strings.ToLower(model)); ok {
				return nil
			}
		}
		return fmt.Errorf("%w: %s", ErrModel, model)
	}
	a.Agent.OrgBudget = x.orgBudget
	x.installSSO()
	return nil
}

func (x *Enterprise) checkZone(ctx context.Context, in zones.Input) error {
	p := x.Pack(ctx)
	if len(p.ZoneHosts) > 0 {
		if in.Network != "allowlist" {
			return ErrZoneHosts
		}
		for _, h := range in.AllowHosts {
			ok := false
			for _, pat := range p.ZoneHosts {
				if m, _ := path.Match(strings.ToLower(pat), strings.ToLower(h)); m || strings.EqualFold(pat, h) {
					ok = true
				}
			}
			if !ok {
				return fmt.Errorf("%w: %s", ErrZoneHosts, h)
			}
		}
	}
	if len(p.Providers) > 0 {
		kind := x.providerKind(ctx, in.ProviderID)
		if !slices.Contains(p.Providers, kind) {
			return fmt.Errorf("%w: %s", ErrProvider, kind)
		}
	}
	return nil
}

func (x *Enterprise) providerKind(ctx context.Context, id *uuid.UUID) string {
	if id == nil {
		return x.a.Computers.Default
	}
	var kind string
	if err := x.a.DB.Engine.QueryRow(ctx, "SELECT kind FROM providers WHERE id = $1", *id).Scan(&kind); err != nil {
		return ""
	}
	return kind
}

// orgBudget checks the organization month budget and the quota of the person who started
// the work (§18.3).
func (x *Enterprise) orgBudget(ctx context.Context, _ uuid.UUID, userID string, estimate float64) (string, float64) {
	if !x.Has(ctx, licensekey.FeatureBudgets, false) {
		return "", 0
	}
	now := time.Now().UTC()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	if lim := x.a.Settings.Float(ctx, nil, "ee.org_month_usd"); lim > 0 {
		var spent float64
		_ = x.a.DB.Engine.QueryRow(ctx, "SELECT COALESCE(SUM(cost_usd), 0) FROM usage_events WHERE at >= $1", month).Scan(&spent)
		if spent+estimate > lim {
			return "organization_month", lim - spent
		}
	}
	if lim := x.a.Settings.Float(ctx, nil, "ee.user_month_usd"); lim > 0 && userID != "" {
		spent := x.userSpent(ctx, userID, month)
		if spent+estimate > lim {
			return "user_month", lim - spent
		}
	}
	return "", 0
}

// userSpent is the cost of the runs of the work that a person started since a time.
func (x *Enterprise) userSpent(ctx context.Context, userID string, since time.Time) float64 {
	req := "json_extract(e.requester, '$.type') = 'user' AND json_extract(e.requester, '$.id') = $2"
	if x.a.DB.Postgres {
		req = "e.requester->>'type' = 'user' AND e.requester->>'id' = $2"
	}
	var spent float64
	_ = x.a.DB.Engine.QueryRow(ctx, `SELECT COALESCE(SUM(u.cost_usd), 0) FROM usage_events u JOIN runs r ON r.id = u.run_id JOIN envelopes e ON e.id = r.envelope_id
		WHERE u.at >= $1 AND `+req, since, userID).Scan(&spent)
	return spent
}

// Edition reports the licence and the features.
func (x *Enterprise) Edition(ctx context.Context) app.EditionView {
	st := x.Status(ctx)
	v := app.EditionView{Name: "enterprise", License: &app.LicenseView{State: string(st.State), DaysLeft: st.DaysLeft, Reason: st.Reason}}
	if st.License != nil {
		v.License.Customer, v.License.Seats, v.License.Expires = st.License.Customer, st.License.Seats, st.License.Expires.Format("2006-01-02")
	}
	for _, f := range app.EnterpriseFeatures {
		v.Features = append(v.Features, app.FeatureView{Key: f, Available: st.Has(f, false), Writable: st.Has(f, true)})
	}
	return v
}

// Public mounts the routes that need no Fettle sign-in.
func (x *Enterprise) Public(r chi.Router) {
	x.publicSCIM(r)
	x.publicSSO(r)
}

// Tick runs on the maintenance tick.
func (x *Enterprise) Tick(ctx context.Context) {
	x.exportAudit(ctx, false)
}

func adminOnly(ctx context.Context) error {
	if p := auth.From(ctx); p == nil || !p.IsAdmin {
		return huma.Error403Forbidden("Only an instance admin can do this.")
	}
	return nil
}

func actor(ctx context.Context) audit.Actor {
	if p := auth.From(ctx); p != nil {
		return audit.Actor{Kind: "user", ID: p.UserID.String()}
	}
	return audit.System
}

// Routes registers the admin API of the Enterprise features.
func (x *Enterprise) Routes(api huma.API) {
	huma.Register(api, huma.Operation{OperationID: "set-license", Method: http.MethodPut, Path: app.APIPrefix + "/admin/license", Summary: "Set the Enterprise licence key (instance admins)", Tags: []string{"enterprise"}}, func(ctx context.Context, in *struct {
		Body struct {
			Key string `json:"key" maxLength:"8192"`
		}
	}) (*struct{ Body app.EditionView }, error) {
		if err := adminOnly(ctx); err != nil {
			return nil, err
		}
		if _, err := licensekey.Verify(in.Body.Key, publicKey(), time.Now()); err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		if _, err := x.a.Settings.Set(ctx, actor(ctx), nil, "ee.license", strings.TrimSpace(in.Body.Key)); err != nil {
			return nil, err
		}
		x.forget()
		return &struct{ Body app.EditionView }{Body: x.Edition(ctx)}, nil
	})
	huma.Register(api, huma.Operation{OperationID: "get-policy-pack", Method: http.MethodGet, Path: app.APIPrefix + "/admin/policy-pack", Summary: "The organization policy pack", Tags: []string{"enterprise"}}, func(ctx context.Context, _ *struct{}) (*struct{ Body Pack }, error) {
		if err := adminOnly(ctx); err != nil {
			return nil, err
		}
		var p Pack
		_ = json.Unmarshal([]byte(x.a.Settings.String(ctx, nil, "ee.policy_pack")), &p)
		return &struct{ Body Pack }{Body: p}, nil
	})
	huma.Register(api, huma.Operation{OperationID: "set-policy-pack", Method: http.MethodPut, Path: app.APIPrefix + "/admin/policy-pack", Summary: "Set the organization policy pack: rules for every space, zone hosts, providers and models", Tags: []string{"enterprise"}}, func(ctx context.Context, in *struct{ Body Pack }) (*struct{ Body Pack }, error) {
		if err := adminOnly(ctx); err != nil {
			return nil, err
		}
		if err := x.writable(ctx, licensekey.FeaturePolicy); err != nil {
			return nil, err
		}
		for _, r := range in.Body.Rules {
			if !slices.Contains([]string{"allow", "ask", "block"}, r.Effect) {
				return nil, huma.Error422UnprocessableEntity("a rule's effect is allow, ask or block")
			}
		}
		b, _ := json.Marshal(in.Body)
		if _, err := x.a.Settings.Set(ctx, actor(ctx), nil, "ee.policy_pack", string(b)); err != nil {
			return nil, err
		}
		return &struct{ Body Pack }{Body: in.Body}, nil
	})
	huma.Register(api, huma.Operation{OperationID: "set-org-budgets", Method: http.MethodPut, Path: app.APIPrefix + "/admin/org-budgets", Summary: "Set the organization month budget and the quota per person in USD", Tags: []string{"enterprise"}}, func(ctx context.Context, in *struct {
		Body struct {
			OrgMonthUSD  float64 `json:"org_month_usd" minimum:"0"`
			UserMonthUSD float64 `json:"user_month_usd" minimum:"0"`
		}
	}) (*struct{}, error) {
		if err := adminOnly(ctx); err != nil {
			return nil, err
		}
		if err := x.writable(ctx, licensekey.FeatureBudgets); err != nil {
			return nil, err
		}
		if _, err := x.a.Settings.Set(ctx, actor(ctx), nil, "ee.org_month_usd", in.Body.OrgMonthUSD); err != nil {
			return nil, err
		}
		_, err := x.a.Settings.Set(ctx, actor(ctx), nil, "ee.user_month_usd", in.Body.UserMonthUSD)
		return nil, err
	})
	x.consoleRoutes(api)
	x.auditRoutes(api)
	x.scimRoutes(api)
}

// writable refuses a change of a feature that the licence does not allow now.
func (x *Enterprise) writable(ctx context.Context, feature string) error {
	st := x.Status(ctx)
	if st.State == licensekey.ReadOnly {
		return huma.Error403Forbidden(ErrReadOnly.Error())
	}
	if !st.Has(feature, true) {
		return huma.Error403Forbidden(ErrFeature.Error())
	}
	return nil
}
