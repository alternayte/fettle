//go:build ee && contract

package ee_test

import (
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/alternayte/fettle/internal/fakes/oauth"
	"github.com/alternayte/fettle/internal/testenv"
)

// browser follows redirects by hand and keeps every cookie, whatever host the server
// names in its links: the test server runs on a loopback port, not on baseURL.
type browser struct {
	t       *testing.T
	fettle  string // the address of the test server
	cookies map[string]string
}

func (b *browser) send(req *http.Request) *http.Response {
	b.t.Helper()
	var parts []string
	for k, v := range b.cookies {
		parts = append(parts, k+"="+v)
	}
	if len(parts) > 0 {
		req.Header.Set("Cookie", strings.Join(parts, "; "))
	}
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	for _, sc := range resp.Cookies() {
		if sc.MaxAge < 0 {
			delete(b.cookies, sc.Name)
		} else {
			b.cookies[sc.Name] = sc.Value
		}
	}
	return resp
}

// local points a link of baseURL at the test server.
func (b *browser) local(link string) string {
	if strings.HasPrefix(link, baseURL) {
		return b.fettle + strings.TrimPrefix(link, baseURL)
	}
	if strings.HasPrefix(link, "/") {
		return b.fettle + link
	}
	return link
}

// follow runs GET requests along redirects and returns the last response.
func (b *browser) follow(link string) *http.Response {
	b.t.Helper()
	for range 10 {
		req, _ := http.NewRequest("GET", b.local(link), nil)
		resp := b.send(req)
		if resp.StatusCode/100 != 3 {
			return resp
		}
		_ = resp.Body.Close()
		link = resp.Header.Get("Location")
		if strings.HasPrefix(link, "/") && !strings.HasPrefix(req.URL.String(), b.fettle) {
			link = req.URL.Scheme + "://" + req.URL.Host + link
		}
		if link == "/" || strings.HasPrefix(link, "/?") || strings.HasPrefix(link, baseURL+"/?") || link == baseURL+"/" {
			return resp
		}
	}
	b.t.Fatal("too many redirects")
	return nil
}

func (b *browser) client() *client {
	return &client{t: b.t, url: b.fettle, cookie: "fettle_session=" + b.cookies["fettle_session"]}
}

var formField = regexp.MustCompile(`name="(SAMLResponse|RelayState)" value="([^"]*)"`)

func TestSCN_EE_003_OIDCAndSAMLSignInFollowGroupMapping(t *testing.T) {
	testenv.Each(t, func(t *testing.T, dialect string) {
		licensed(t)
		idp := oauth.NewOIDC()
		defer idp.Close()
		sml := oauth.NewSAML()
		defer sml.Close()
		t.Setenv("FETTLE_OIDC_ISSUER", idp.URL)
		t.Setenv("FETTLE_OIDC_CLIENT_ID", idp.ClientID)
		t.Setenv("FETTLE_OIDC_CLIENT_SECRET", idp.ClientSecret)
		t.Setenv("FETTLE_OIDC_NAME", "Acme ID")
		t.Setenv("FETTLE_SAML_IDP_METADATA", sml.MetadataURL())
		a, url0, space := serverApp(t, dialect)
		sml.SPMetadataURL = url0 + "/api/v1/sso/saml/metadata"
		admin := signIn(t, url0, ownerEmail, ownerPassword)
		admin.must("PUT", "/api/v1/admin/group-roles", map[string]any{"mappings": []map[string]string{
			{"group": "Engineering", "space": space.String(), "role": "member"},
			{"group": "Leads", "space": space.String(), "role": "owner"},
		}}, nil)

		// The sign-in page lists both methods.
		var methods struct {
			Methods []struct{ Kind, Name, URL string } `json:"methods"`
		}
		(&client{t: t, url: url0}).must("GET", "/api/v1/sso", nil, &methods)
		if len(methods.Methods) != 2 || methods.Methods[0].Name != "Acme ID" || methods.Methods[1].Kind != "saml" {
			t.Fatalf("methods: %+v", methods)
		}

		// OpenID Connect: an Engineering person becomes a member of acme.
		idp.SignInAs(oauth.Person{Subject: "sub-dana", Email: "dana@acme.test", Name: "Dana Dev", Groups: []string{"Engineering"}})
		b := &browser{t: t, fettle: url0, cookies: map[string]string{}}
		resp := b.follow(methods.Methods[0].URL)
		_ = resp.Body.Close()
		if b.cookies["fettle_session"] == "" {
			t.Fatalf("the OIDC sign-in set no session: %d %s", resp.StatusCode, resp.Header.Get("Location"))
		}
		if code, body := b.client().do("GET", "/api/v1/me", nil); code != 200 || !strings.Contains(body, `"role":"member"`) {
			t.Fatalf("OIDC person /me: %d %s", code, body)
		}
		if r := roles(t, a, space); r["dana@acme.test"] != "member" || r[ownerEmail] != "owner" {
			t.Fatalf("after the OIDC sign-in: %v", r)
		}

		// The next sign-in with Leads makes Dana an owner; one with no mapped group removes her.
		idp.SignInAs(oauth.Person{Subject: "sub-dana", Email: "dana@acme.test", Name: "Dana Dev", Groups: []string{"Engineering", "Leads"}})
		b = &browser{t: t, fettle: url0, cookies: map[string]string{}}
		_ = b.follow(methods.Methods[0].URL).Body.Close()
		if r := roles(t, a, space); r["dana@acme.test"] != "owner" {
			t.Fatalf("after Leads: %v", r)
		}
		idp.SignInAs(oauth.Person{Subject: "sub-dana", Email: "dana@acme.test", Name: "Dana Dev", Groups: []string{"Sales"}})
		b = &browser{t: t, fettle: url0, cookies: map[string]string{}}
		_ = b.follow(methods.Methods[0].URL).Body.Close()
		if r := roles(t, a, space); r["dana@acme.test"] != "" || r[ownerEmail] != "owner" {
			t.Fatalf("after leaving the groups: %v", r)
		}

		// SAML: a Leads person becomes an owner of acme.
		sml.SignInAs(oauth.Person{Email: "lee@acme.test", Name: "Lee Lead", Groups: []string{"Leads"}})
		b = &browser{t: t, fettle: url0, cookies: map[string]string{}}
		resp = b.follow(methods.Methods[1].URL)
		page, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		form := url.Values{}
		for _, m := range formField.FindAllStringSubmatch(string(page), -1) {
			form.Set(m[1], html.UnescapeString(m[2]))
		}
		if form.Get("SAMLResponse") == "" {
			t.Fatalf("the identity provider sent no SAML response: %d %s", resp.StatusCode, page)
		}
		req, _ := http.NewRequest("POST", url0+"/api/v1/sso/saml/acs", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp = b.send(req)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" || b.cookies["fettle_session"] == "" {
			t.Fatalf("SAML ACS: %d %s", resp.StatusCode, resp.Header.Get("Location"))
		}
		if code, body := b.client().do("GET", "/api/v1/me", nil); code != 200 || !strings.Contains(body, `"role":"owner"`) {
			t.Fatalf("SAML person /me: %d %s", code, body)
		}
		if r := roles(t, a, space); r["lee@acme.test"] != "owner" {
			t.Fatalf("after the SAML sign-in: %v", r)
		}

		// A replayed response is refused: its request ID is spent.
		req, _ = http.NewRequest("POST", url0+"/api/v1/sso/saml/acs", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp = (&browser{t: t, fettle: url0, cookies: map[string]string{}}).send(req)
		_ = resp.Body.Close()
		if loc := resp.Header.Get("Location"); !strings.Contains(loc, "sso_error=") {
			t.Fatalf("a replayed SAML response: %d %s", resp.StatusCode, loc)
		}
	})
}
