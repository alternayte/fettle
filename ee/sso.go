//go:build ee

package ee

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	authall "github.com/alternayte/auth-all"
	"github.com/alternayte/auth-all/apierr"
	"github.com/alternayte/auth-all/hook"
	"github.com/alternayte/auth-all/oauth/oidc"
	"github.com/crewjam/saml"
	"github.com/crewjam/saml/samlsp"
	"github.com/go-chi/chi/v5"

	"github.com/alternayte/fettle/ee/licensekey"
	"github.com/alternayte/fettle/internal/app"
	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/auth"
	"github.com/alternayte/fettle/internal/settings"
)

// OIDCProviderID names the OpenID Connect provider in the auth-all routes:
// /api/auth/oauth/sso and /api/auth/oauth/sso/callback.
const OIDCProviderID = "sso"

const samlRequestCookie = "fettle_saml_request"

func init() {
	settings.Register(settings.Def{Key: "ee.sso_groups", Scope: "instance", Default: "", Internal: true, Doc: "The identity provider groups of each person at the last single sign-on, as JSON (set by Fettle)."})
}

// ssoConfig is the single sign-on of this server, from the environment.
type ssoConfig struct {
	OIDCIssuer, OIDCClientID, OIDCClientSecret, OIDCGroupsClaim, OIDCName string
	SAMLMetadata, SAMLGroupsAttribute, SAMLName                           string
}

func ssoFromEnv() ssoConfig {
	c := ssoConfig{
		OIDCIssuer: os.Getenv("FETTLE_OIDC_ISSUER"), OIDCClientID: os.Getenv("FETTLE_OIDC_CLIENT_ID"),
		OIDCClientSecret: os.Getenv("FETTLE_OIDC_CLIENT_SECRET"), OIDCGroupsClaim: os.Getenv("FETTLE_OIDC_GROUPS_CLAIM"),
		OIDCName:     os.Getenv("FETTLE_OIDC_NAME"),
		SAMLMetadata: os.Getenv("FETTLE_SAML_IDP_METADATA"), SAMLGroupsAttribute: os.Getenv("FETTLE_SAML_GROUPS_ATTRIBUTE"),
		SAMLName: os.Getenv("FETTLE_SAML_NAME"),
	}
	if c.OIDCGroupsClaim == "" {
		c.OIDCGroupsClaim = "groups"
	}
	if c.SAMLGroupsAttribute == "" {
		c.SAMLGroupsAttribute = "groups"
	}
	if c.OIDCName == "" {
		c.OIDCName = "single sign-on"
	}
	if c.SAMLName == "" {
		c.SAMLName = "single sign-on"
	}
	return c
}

func (c ssoConfig) oidc() bool { return c.OIDCIssuer != "" }
func (c ssoConfig) saml() bool { return c.SAMLMetadata != "" }

// sso holds the state of single sign-on.
type sso struct {
	cfg ssoConfig

	mu sync.Mutex
	// groups of an OpenID Connect sign-in, from the hook before the session to the hook after it
	pending map[string][]string
	sp      *saml.ServiceProvider
}

// SignInOptions implements app.SignInExtension: the OpenID Connect provider of
// FETTLE_OIDC_ISSUER, when it is set.
func (x *Enterprise) SignInOptions(cfg app.Config) ([]authall.Option, error) {
	x.sso.mu.Lock()
	x.sso.cfg, x.sso.sp, x.sso.pending = ssoFromEnv(), nil, nil
	x.sso.mu.Unlock()
	c := x.sso.cfg
	if !c.oidc() {
		return nil, nil
	}
	if c.OIDCClientID == "" || c.OIDCClientSecret == "" {
		return nil, errors.New("FETTLE_OIDC_ISSUER needs FETTLE_OIDC_CLIENT_ID and FETTLE_OIDC_CLIENT_SECRET")
	}
	p := oidc.New(oidc.WithIssuer(strings.TrimRight(c.OIDCIssuer, "/")), oidc.WithID(OIDCProviderID),
		oidc.WithClientID(c.OIDCClientID), oidc.WithClientSecret(c.OIDCClientSecret),
		oidc.WithScopes("openid", "email", "profile", c.OIDCGroupsClaim))
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("FETTLE_OIDC_ISSUER: %w", err)
	}
	return []authall.Option{authall.WithProvider(p)}, nil
}

// installSSO sets the sign-in hooks that map the groups of an OpenID Connect sign-in.
func (x *Enterprise) installSSO() {
	if x.a.SignIn == nil || !x.sso.cfg.oidc() {
		return
	}
	h := x.a.SignIn.Auth.Hooks()
	h.OnBeforeOAuthSignIn(func(ctx context.Context, ev *hook.OAuthSignIn) error {
		if ev.Provider != OIDCProviderID {
			return nil
		}
		if !x.Has(ctx, licensekey.FeatureSSO, false) {
			return apierr.ErrForbidden.WithMessage("Single sign-on needs an Enterprise licence with SSO.")
		}
		groups := []string{}
		if ev.Identity != nil {
			groups = ev.Identity.Claims.Strings(x.sso.cfg.OIDCGroupsClaim)
		}
		x.sso.mu.Lock()
		if x.sso.pending == nil {
			x.sso.pending = map[string][]string{}
		}
		x.sso.pending[ev.User.ID] = groups
		x.sso.mu.Unlock()
		return nil
	})
	h.OnAfterSignIn(func(ctx context.Context, ev *hook.SignIn) error {
		if ev.Method != OIDCProviderID {
			return nil
		}
		x.sso.mu.Lock()
		groups, ok := x.sso.pending[ev.User.ID]
		delete(x.sso.pending, ev.User.ID)
		x.sso.mu.Unlock()
		if !ok {
			return nil
		}
		if err := x.signedIn(ctx, ev.User.Email, ev.User.DisplayName, groups); err != nil {
			x.a.Log.Warn("single sign-on groups", "error", err)
		}
		return nil
	})
}

// signedIn makes the Fettle user of a single sign-on and makes its spaces follow its groups.
func (x *Enterprise) signedIn(ctx context.Context, email, name string, groups []string) error {
	u, err := x.a.SignIn.Users().Ensure(ctx, email, name, false)
	if err != nil {
		return err
	}
	x.groupMu.Lock()
	defer x.groupMu.Unlock()
	all := x.ssoGroups(ctx)
	if groups == nil {
		groups = []string{}
	}
	all[u.ID().String()] = groups
	b, _ := json.Marshal(all)
	if _, err := x.a.Settings.Set(ctx, audit.Actor{Kind: "system", ID: "sso"}, nil, "ee.sso_groups", string(b)); err != nil {
		return err
	}
	return x.applyGroupRolesLocked(ctx)
}

// ssoGroups returns the groups of each person at the last single sign-on: user ID -> groups.
func (x *Enterprise) ssoGroups(ctx context.Context) map[string][]string {
	out := map[string][]string{}
	_ = json.Unmarshal([]byte(x.a.Settings.String(ctx, nil, "ee.sso_groups")), &out)
	return out
}

// --- SAML

// serviceProvider returns the SAML service provider of this server. It reads the identity
// provider metadata once, from a URL or a file.
func (x *Enterprise) serviceProvider(ctx context.Context) (*saml.ServiceProvider, error) {
	x.sso.mu.Lock()
	defer x.sso.mu.Unlock()
	if x.sso.sp != nil {
		return x.sso.sp, nil
	}
	src := x.sso.cfg.SAMLMetadata
	var md *saml.EntityDescriptor
	var err error
	if strings.HasPrefix(src, "https://") || strings.HasPrefix(src, "http://") {
		u, perr := url.Parse(src)
		if perr != nil {
			return nil, perr
		}
		fctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		md, err = samlsp.FetchMetadata(fctx, http.DefaultClient, *u)
	} else {
		var b []byte
		if b, err = os.ReadFile(src); err == nil {
			md, err = samlsp.ParseMetadata(b)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("read the SAML identity provider metadata: %w", err)
	}
	base, err := url.Parse(x.a.Cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	sp := &saml.ServiceProvider{
		EntityID:          base.ResolveReference(&url.URL{Path: app.APIPrefix + "/sso/saml/metadata"}).String(),
		MetadataURL:       *base.ResolveReference(&url.URL{Path: app.APIPrefix + "/sso/saml/metadata"}),
		AcsURL:            *base.ResolveReference(&url.URL{Path: app.APIPrefix + "/sso/saml/acs"}),
		IDPMetadata:       md,
		AllowIDPInitiated: false,
		AuthnNameIDFormat: saml.EmailAddressNameIDFormat,
	}
	x.sso.sp = sp
	return sp, nil
}

// SSOMethod is one way to sign in with single sign-on, for the sign-in page.
type SSOMethod struct {
	Kind string `json:"kind" enum:"oidc,saml"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

func (x *Enterprise) ssoMethods(ctx context.Context) []SSOMethod {
	out := []SSOMethod{}
	if x.a.SignIn == nil || !x.Has(ctx, licensekey.FeatureSSO, false) {
		return out
	}
	if x.sso.cfg.oidc() {
		out = append(out, SSOMethod{Kind: "oidc", Name: x.sso.cfg.OIDCName, URL: "/api/auth/oauth/" + OIDCProviderID})
	}
	if x.sso.cfg.saml() {
		out = append(out, SSOMethod{Kind: "saml", Name: x.sso.cfg.SAMLName, URL: app.APIPrefix + "/sso/saml/start"})
	}
	return out
}

// publicSSO mounts the single sign-on routes that need no Fettle sign-in.
func (x *Enterprise) publicSSO(r chi.Router) {
	r.Get(app.APIPrefix+"/sso", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{"methods": x.ssoMethods(r.Context())})
	})
	r.Get(app.APIPrefix+"/sso/saml/metadata", func(w http.ResponseWriter, r *http.Request) {
		sp, err := x.samlReady(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		b, _ := xmlMarshal(sp.Metadata())
		w.Header().Set("Content-Type", "application/samlmetadata+xml")
		_, _ = w.Write(b)
	})
	r.Get(app.APIPrefix+"/sso/saml/start", func(w http.ResponseWriter, r *http.Request) {
		sp, err := x.samlReady(r.Context())
		if err != nil {
			x.ssoFail(w, r, sentence(err))
			return
		}
		req, err := sp.MakeAuthenticationRequest(sp.GetSSOBindingLocation(saml.HTTPRedirectBinding), saml.HTTPRedirectBinding, saml.HTTPPostBinding)
		if err != nil {
			x.ssoFail(w, r, "Fettle could not start the SAML sign-in: "+err.Error())
			return
		}
		to, err := req.Redirect("", sp)
		if err != nil {
			x.ssoFail(w, r, "Fettle could not start the SAML sign-in: "+err.Error())
			return
		}
		// The identity provider posts the answer from its own site, so the cookie must
		// travel on a cross-site POST.
		secure := strings.HasPrefix(x.a.Cfg.BaseURL, "https://")
		same := http.SameSiteLaxMode
		if secure {
			same = http.SameSiteNoneMode
		}
		http.SetCookie(w, &http.Cookie{Name: samlRequestCookie, Value: req.ID, Path: app.APIPrefix + "/sso/saml/acs",
			MaxAge: 600, HttpOnly: true, Secure: secure, SameSite: same})
		http.Redirect(w, r, to.String(), http.StatusFound)
	})
	r.Post(app.APIPrefix+"/sso/saml/acs", x.samlACS)
}

func (x *Enterprise) samlReady(ctx context.Context) (*saml.ServiceProvider, error) {
	if x.a.SignIn == nil || !x.sso.cfg.saml() {
		return nil, errors.New("SAML sign-in is not set up on this server")
	}
	if !x.Has(ctx, licensekey.FeatureSSO, false) {
		return nil, errors.New("single sign-on needs an Enterprise licence with SSO")
	}
	return x.serviceProvider(ctx)
}

// samlACS checks the signed answer of the identity provider, signs the person in, and makes
// their spaces follow their groups.
func (x *Enterprise) samlACS(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sp, err := x.samlReady(ctx)
	if err != nil {
		x.ssoFail(w, r, sentence(err))
		return
	}
	var ids []string
	if c, err := r.Cookie(samlRequestCookie); err == nil && c.Value != "" {
		ids = []string{c.Value}
	}
	http.SetCookie(w, &http.Cookie{Name: samlRequestCookie, Value: "", Path: app.APIPrefix + "/sso/saml/acs", MaxAge: -1})
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		x.ssoFail(w, r, "Fettle could not read the SAML answer.")
		return
	}
	assertion, err := sp.ParseResponse(r, ids)
	if err != nil {
		reason := err.Error()
		var ire *saml.InvalidResponseError
		if errors.As(err, &ire) && ire.PrivateErr != nil {
			x.a.Log.Warn("SAML response refused", "error", ire.PrivateErr)
			reason = "the identity provider's answer is not valid"
		}
		x.ssoFail(w, r, "Fettle refused the SAML sign-in: "+reason+".")
		return
	}
	email, name, groups := samlPerson(assertion, x.sso.cfg.SAMLGroupsAttribute)
	if email == "" {
		x.ssoFail(w, r, "The identity provider sent no email address.")
		return
	}
	fu, err := x.a.SignIn.Provision(ctx, email, name)
	if err != nil {
		x.ssoFail(w, r, "Fettle could not make the account: "+err.Error())
		return
	}
	if fu.Status != "active" {
		x.ssoFail(w, r, "This account is disabled.")
		return
	}
	au, err := x.a.SignIn.Auth.GetUserByEmail(ctx, email)
	if err != nil {
		x.ssoFail(w, r, "Fettle could not find the account.")
		return
	}
	prev := ""
	if c, err := r.Cookie(auth.SessionCookie); err == nil {
		prev = c.Value
	}
	res, err := x.a.SignIn.Auth.IssueSession(ctx, authall.IssueSessionInput{UserID: au.ID, Method: "saml", PreviousSessionToken: prev})
	if err != nil {
		if _, msg, ok := auth.PublicError(err); ok {
			x.ssoFail(w, r, msg)
			return
		}
		x.ssoFail(w, r, "Fettle could not sign you in.")
		return
	}
	if res.MFARequired || res.Session == nil {
		x.ssoFail(w, r, "This account needs a second factor that single sign-on cannot give.")
		return
	}
	if err := x.signedIn(ctx, email, name, groups); err != nil {
		x.ssoFail(w, r, "Fettle could not apply your groups: "+err.Error())
		return
	}
	x.a.SignIn.Auth.SetSessionCookie(w, res.Token, res.Session.ExpiresAt)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// samlPerson reads the address, the name and the groups of an assertion.
func samlPerson(a *saml.Assertion, groupsAttr string) (email, name string, groups []string) {
	if a.Subject != nil && a.Subject.NameID != nil && strings.Contains(a.Subject.NameID.Value, "@") {
		email = a.Subject.NameID.Value
	}
	emailNames := []string{"email", "mail", "emailaddress", "urn:oid:0.9.2342.19200300.100.1.3",
		"http://schemas.xmlsoap.org/ws/2005/05/identity/claims/emailaddress"}
	nameNames := []string{"name", "displayname", "cn", "urn:oid:2.5.4.3", "urn:oid:2.16.840.1.113730.3.1.241",
		"http://schemas.xmlsoap.org/ws/2005/05/identity/claims/name"}
	groups = []string{}
	for _, st := range a.AttributeStatements {
		for _, at := range st.Attributes {
			n, f := strings.ToLower(at.Name), strings.ToLower(at.FriendlyName)
			is := func(names ...string) bool { return slices.Contains(names, n) || (f != "" && slices.Contains(names, f)) }
			switch {
			case strings.EqualFold(at.Name, groupsAttr) || strings.EqualFold(at.FriendlyName, groupsAttr):
				for _, v := range at.Values {
					if g := strings.TrimSpace(v.Value); g != "" && !slices.Contains(groups, g) {
						groups = append(groups, g)
					}
				}
			case email == "" && is(emailNames...) && len(at.Values) > 0:
				email = at.Values[0].Value
			case name == "" && is(nameNames...) && len(at.Values) > 0:
				name = at.Values[0].Value
			}
		}
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if name == "" {
		name, _, _ = strings.Cut(email, "@")
	}
	return email, name, groups
}

// sentence makes an error into a sentence for the sign-in page.
func sentence(err error) string {
	m := err.Error()
	if m == "" {
		return m
	}
	return strings.ToUpper(m[:1]) + m[1:] + "."
}

func xmlMarshal(v any) ([]byte, error) {
	b, err := xml.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), b...), nil
}

// ssoFail sends the browser to the sign-in page with the reason.
func (x *Enterprise) ssoFail(w http.ResponseWriter, r *http.Request, reason string) {
	http.Redirect(w, r, "/?sso_error="+url.QueryEscape(reason), http.StatusSeeOther)
}
