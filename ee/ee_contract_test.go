//go:build ee && contract

package ee_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/testcontainers/testcontainers-go/modules/minio"

	"github.com/alternayte/fettle/ee"
	"github.com/alternayte/fettle/ee/licensekey"
	"github.com/alternayte/fettle/internal/app"
	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/objects"
	"github.com/alternayte/fettle/internal/testenv"
)

const (
	ownerEmail    = "owner@acme.test"
	ownerPassword = "acme-owner-pass-4410"
	baseURL       = "http://fettle.test"
)

// licensed sets a licence with every Enterprise feature, signed by a test issuer.
func licensed(t *testing.T) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	ee.SetTestPublicKey(pub)
	t.Cleanup(func() { ee.SetTestPublicKey(nil) })
	key, err := licensekey.Sign(priv, licensekey.License{ID: "lic-acme", Customer: "Acme", Seats: 50, Features: []string{licensekey.FeatureAllInOne}, IssuedAt: time.Now().AddDate(0, -1, 0), Expires: time.Now().AddDate(1, 0, 0)})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FETTLE_LICENSE", key)
}

// serverApp starts a server-mode app with an admin owner and the space acme.
func serverApp(t *testing.T, dialect string) (*app.App, string, uuid.UUID) {
	t.Helper()
	t.Setenv("FETTLE_NO_KEYCHAIN", "1")
	t.Setenv("FETTLE_DEFAULT_PROVIDER", "docker")
	cfg := testenv.Config(t, dialect)
	cfg.Mode, cfg.BaseURL, cfg.Version = app.ModeServer, baseURL, "test"
	ctx := context.Background()
	a, err := app.New(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(ctx, a.Handler()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Shutdown(context.Background()) })
	u, err := a.SignIn.CreateUser(ctx, ownerEmail, "Owner", ownerPassword, true)
	if err != nil {
		t.Fatal(err)
	}
	sp, err := a.CreateSpace(ctx, audit.System, "acme", "UTC", u.ID())
	if err != nil {
		t.Fatal(err)
	}
	return a, "http://" + a.Addr(), sp.ID()
}

type client struct {
	t      *testing.T
	url    string
	cookie string
	bearer string
}

func (c *client) do(method, path string, body any) (int, string) {
	c.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.url+path, r)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", baseURL)
	if c.cookie != "" {
		req.Header.Set("Cookie", c.cookie)
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (c *client) must(method, path string, body, out any) {
	c.t.Helper()
	code, b := c.do(method, path, body)
	if code/100 != 2 {
		c.t.Fatalf("%s %s = %d: %s", method, path, code, b)
	}
	if out != nil {
		if err := json.Unmarshal([]byte(b), out); err != nil {
			c.t.Fatalf("%s %s: %v: %s", method, path, err, b)
		}
	}
}

func signIn(t *testing.T, url, email, password string) *client {
	t.Helper()
	c := &client{t: t, url: url}
	b, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req, _ := http.NewRequest("POST", url+"/api/auth/sign-in/email", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", baseURL)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	for _, sc := range resp.Header.Values("Set-Cookie") {
		if strings.HasPrefix(sc, "fettle_session=") {
			c.cookie = strings.SplitN(sc, ";", 2)[0]
		}
	}
	if resp.StatusCode != 200 || c.cookie == "" {
		t.Fatalf("sign-in %s: %d", email, resp.StatusCode)
	}
	return c
}

func roles(t *testing.T, a *app.App, space uuid.UUID) map[string]string {
	t.Helper()
	ms, err := a.Spaces.Members(context.Background(), space)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, m := range ms {
		out[m.Email] = m.Role
	}
	return out
}

func TestSCN_EE_004_SCIMProvisionsUsersAndGroups(t *testing.T) {
	testenv.Each(t, func(t *testing.T, dialect string) {
		licensed(t)
		a, url, space := serverApp(t, dialect)
		admin := signIn(t, url, ownerEmail, ownerPassword)

		var tok struct{ Token, URL string }
		admin.must("POST", "/api/v1/admin/scim/token", nil, &tok)
		if !strings.HasPrefix(tok.Token, "scim_") || tok.URL != baseURL+"/scim/v2" {
			t.Fatalf("token: %+v", tok)
		}
		admin.must("PUT", "/api/v1/admin/group-roles", map[string]any{"mappings": []map[string]string{
			{"group": "Engineering", "space": space.String(), "role": "member"},
			{"group": "Leads", "space": space.String(), "role": "owner"},
		}}, nil)
		scim := &client{t: t, url: url, bearer: tok.Token}
		if code, _ := (&client{t: t, url: url, bearer: "scim_wrong"}).do("GET", "/scim/v2/Users", nil); code != 401 {
			t.Fatalf("a wrong SCIM token: %d", code)
		}

		// Create a user; a second create of the same userName conflicts.
		var dev struct {
			ID     string `json:"id"`
			Active bool   `json:"active"`
		}
		newUser := map[string]any{"schemas": []string{"urn:ietf:params:scim:schemas:core:2.0:User"}, "userName": "dev@acme.test", "name": map[string]string{"givenName": "Dana", "familyName": "Dev"}, "emails": []map[string]any{{"value": "dev@acme.test", "primary": true}}, "active": true}
		scim.must("POST", "/scim/v2/Users", newUser, &dev)
		if dev.ID == "" || !dev.Active {
			t.Fatalf("created user: %+v", dev)
		}
		if code, _ := scim.do("POST", "/scim/v2/Users", newUser); code != 409 {
			t.Fatalf("a duplicate user: %d", code)
		}
		scim.must("PUT", "/scim/v2/Users/"+dev.ID, map[string]any{"userName": "dev@acme.test", "displayName": "Dana Developer", "active": true}, nil)
		if u, _ := a.SignIn.Users().ByEmail(context.Background(), "dev@acme.test"); u == nil || u.Name != "Dana Developer" {
			t.Fatalf("the update did not change the name: %+v", u)
		}

		// A person with a password, found by the filter that identity providers send.
		if _, err := a.SignIn.CreateUser(context.Background(), "ops@acme.test", "Olli Ops", "acme-ops-pass-5521", false); err != nil {
			t.Fatal(err)
		}
		var list struct {
			TotalResults int `json:"totalResults"`
			Resources    []struct {
				ID string `json:"id"`
			} `json:"Resources"`
		}
		scim.must("GET", `/scim/v2/Users?filter=userName%20eq%20%22ops@acme.test%22`, nil, &list)
		if list.TotalResults != 1 {
			t.Fatalf("filter: %+v", list)
		}
		ops := list.Resources[0].ID

		// Groups give roles in the mapped space.
		var eng struct{ ID string }
		scim.must("POST", "/scim/v2/Groups", map[string]any{"displayName": "Engineering", "members": []map[string]string{{"value": dev.ID}, {"value": ops}}}, &eng)
		if r := roles(t, a, space); r["dev@acme.test"] != "member" || r["ops@acme.test"] != "member" || r[ownerEmail] != "owner" {
			t.Fatalf("after Engineering: %v", r)
		}
		scim.must("POST", "/scim/v2/Groups", map[string]any{"displayName": "Leads", "members": []map[string]string{{"value": ops}}}, nil)
		if r := roles(t, a, space); r["ops@acme.test"] != "owner" {
			t.Fatalf("after Leads: %v", r)
		}
		scim.must("PATCH", "/scim/v2/Groups/"+eng.ID, map[string]any{"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"}, "Operations": []map[string]any{{"op": "remove", "path": `members[value eq "` + dev.ID + `"]`}}}, nil)
		if r := roles(t, a, space); r["dev@acme.test"] != "" || r[ownerEmail] != "owner" {
			t.Fatalf("after the remove, dev stays or the hand-added owner left: %v", r)
		}

		// A deactivated person loses access at once.
		opsWeb := signIn(t, url, "ops@acme.test", "acme-ops-pass-5521")
		if code, _ := opsWeb.do("GET", "/api/v1/me", nil); code != 200 {
			t.Fatalf("ops before deactivation: %d", code)
		}
		scim.must("PATCH", "/scim/v2/Users/"+ops, map[string]any{"schemas": []string{"urn:ietf:params:scim:api:messages:2.0:PatchOp"}, "Operations": []map[string]any{{"op": "replace", "value": map[string]any{"active": false}}}}, nil)
		if code, _ := opsWeb.do("GET", "/api/v1/me", nil); code != 401 && code != 403 {
			t.Fatalf("a deactivated person still has access: %d", code)
		}
		// A new sign-in either fails or gives a session that Fettle refuses.
		fresh := &client{t: t, url: url}
		if code, _ := fresh.do("POST", "/api/auth/sign-in/email", map[string]string{"email": "ops@acme.test", "password": "acme-ops-pass-5521"}); code == 200 {
			if code, _ := signIn(t, url, "ops@acme.test", "acme-ops-pass-5521").do("GET", "/api/v1/me", nil); code != 403 {
				t.Fatalf("a new session of a deactivated person: %d", code)
			}
		}
		scim.must("DELETE", "/scim/v2/Users/"+dev.ID, nil, nil)
		if u, _ := a.SignIn.Users().ByEmail(context.Background(), "dev@acme.test"); u == nil || u.Status != "disabled" {
			t.Fatalf("DELETE did not deactivate: %+v", u)
		}
	})
}

func TestSCN_EE_005_AuditExportToBucketAndWebhook(t *testing.T) {
	ctx := context.Background()
	c, err := minio.Run(ctx, "cgr.dev/chainguard/minio:latest", minio.WithUsername("acmeadmin"), minio.WithPassword("acme-minio-secret"))
	if err != nil {
		t.Fatalf("minio: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })
	endpoint, err := c.ConnectionString(ctx)
	if err != nil {
		t.Fatal(err)
	}
	endpoint = "http://" + endpoint

	var mu sync.Mutex
	var deliveries [][]byte
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get("X-Fettle-Signature") != ee.SignWebhook("acme-hook-secret", r.Header.Get("X-Fettle-Timestamp"), body) {
			http.Error(w, "bad signature", http.StatusUnauthorized)
			return
		}
		mu.Lock()
		deliveries = append(deliveries, body)
		mu.Unlock()
	}))
	t.Cleanup(hook.Close)

	testenv.Each(t, func(t *testing.T, dialect string) {
		licensed(t)
		bucket := "audit-" + dialect
		for k, v := range map[string]string{"FETTLE_AUDIT_S3_ENDPOINT": endpoint, "FETTLE_AUDIT_S3_BUCKET": bucket, "FETTLE_AUDIT_S3_REGION": "us-east-1", "FETTLE_AUDIT_S3_ACCESS_KEY": "acmeadmin", "FETTLE_AUDIT_S3_SECRET_KEY": "acme-minio-secret", "FETTLE_AUDIT_S3_PATH_STYLE": "true", "FETTLE_AUDIT_WEBHOOK_URL": hook.URL, "FETTLE_AUDIT_WEBHOOK_SECRET": "acme-hook-secret"} {
			t.Setenv(k, v)
		}
		mu.Lock()
		deliveries = nil
		mu.Unlock()
		a, url, _ := serverApp(t, dialect)
		admin := signIn(t, url, ownerEmail, ownerPassword)
		store, err := objects.NewS3(ctx, objects.S3Config{Endpoint: endpoint, Region: "us-east-1", Bucket: bucket, AccessKey: "acmeadmin", SecretKey: "acme-minio-secret", PathStyle: true})
		if err != nil {
			t.Fatal(err)
		}
		old := "audit/2020/01/01/000000.000000000-old.ndjson"
		if err := store.Put(ctx, old, strings.NewReader("{}\n"), 3); err != nil {
			t.Fatal(err)
		}
		if _, err := a.Settings.Set(ctx, audit.System, nil, "ee.audit_retention_days", 30); err != nil {
			t.Fatal(err)
		}
		if err := a.Audit.Record(ctx, audit.Actor{Kind: "user", ID: "acme-auditor"}, nil, "acme.test_event", "acme:1", map[string]string{"k": "v"}); err != nil {
			t.Fatal(err)
		}
		admin.must("POST", "/api/v1/admin/audit-export/run", nil, nil)

		keys, err := store.List(ctx, "audit/")
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, k := range keys {
			if k == old {
				t.Fatal("retention kept a file older than 30 days")
			}
			got = append(got, k)
		}
		if len(got) == 0 {
			t.Fatal("no audit file in the bucket")
		}
		rc, err := store.Get(ctx, got[0])
		if err != nil {
			t.Fatal(err)
		}
		file, _ := io.ReadAll(rc)
		_ = rc.Close()
		if !strings.Contains(string(file), `"action":"acme.test_event"`) {
			t.Fatalf("the audit file misses the event: %s", file)
		}
		mu.Lock()
		n := len(deliveries)
		joined := string(bytes.Join(deliveries, nil))
		mu.Unlock()
		if n == 0 || !strings.Contains(joined, `"action":"acme.test_event"`) {
			t.Fatalf("no signed webhook delivery with the event: %d %s", n, joined)
		}

		// A second run sends only new rows.
		admin.must("POST", "/api/v1/admin/audit-export/run", nil, nil)
		mu.Lock()
		again := len(deliveries)
		mu.Unlock()
		var st struct {
			Cursor string `json:"cursor"`
		}
		admin.must("GET", "/api/v1/admin/audit-export", nil, &st)
		if st.Cursor == "" {
			t.Fatal("no cursor after the export")
		}
		if again > n+1 {
			t.Fatalf("a second run sent the old rows again: %d -> %d", n, again)
		}
	})
}
