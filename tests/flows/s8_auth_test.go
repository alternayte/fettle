//go:build e2e

package flows

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/alternayte/auth-all/plugins/passkeys/passkeystest"
)

// signInAs signs a person in to a server-mode server and keeps the session cookie.
func (s *serverMode) signInAs(email, password string) {
	s.t.Helper()
	s.cookie = ""
	code, h, body := s.do("POST", "/api/auth/sign-in/email", map[string]string{"email": email, "password": password}, s.baseURL)
	if code != 200 {
		s.t.Fatalf("sign-in of %s: %d %s", email, code, body)
	}
	s.cookie = sessionCookie(h)
}

func sessionCookie(h http.Header) string {
	for _, sc := range h.Values("Set-Cookie") {
		if strings.HasPrefix(sc, "fettle_session=") {
			return strings.SplitN(sc, ";", 2)[0]
		}
	}
	return ""
}

type meRow struct {
	Email string `json:"email"`
	Setup struct {
		NeedsModel bool `json:"needs_model"`
	} `json:"setup"`
	Spaces []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Role string `json:"role"`
	} `json:"spaces"`
}

func (s *serverMode) me() meRow {
	s.t.Helper()
	code, _, body := s.do("GET", "/api/v1/me", nil, "")
	if code != 200 {
		s.t.Fatalf("GET /me: %d %s", code, body)
	}
	var m meRow
	_ = json.Unmarshal([]byte(body), &m)
	return m
}

func TestSCN_AUTH_001_InviteLinkMakesAMemberOfOneSpace(t *testing.T) {
	s := startServerMode(t, "https://fettle.test")
	s.signInAs(ownerEmail, ownerPassword)
	acme := s.me().Spaces[0]
	// A second space of the owner, which the member must not see.
	code, _, body := s.do("POST", "/api/v1/spaces", map[string]string{"name": "other", "time_zone": "UTC"}, s.baseURL)
	if code/100 != 2 {
		t.Fatalf("create space: %d %s", code, body)
	}

	code, _, body = s.do("POST", "/api/v1/spaces/"+acme.ID+"/invites", map[string]any{"role": "member"}, s.baseURL)
	if code != 200 {
		t.Fatalf("create invite: %d %s", code, body)
	}
	var inv struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	}
	_ = json.Unmarshal([]byte(body), &inv)
	token := inv.URL[strings.LastIndex(inv.URL, "/")+1:]
	if !strings.HasPrefix(inv.URL, "https://fettle.test/invite/inv_") {
		t.Fatalf("invite url %q", inv.URL)
	}

	// The link shows its space and role without a sign-in.
	s.cookie = ""
	code, _, body = s.do("GET", "/api/v1/invites/"+token, nil, "")
	if code != 200 || !strings.Contains(body, `"role":"member"`) {
		t.Fatalf("invite info: %d %s", code, body)
	}
	// Sign-up from another origin is refused; from the server's origin it works.
	if code, _, _ = s.do("POST", "/api/v1/invites/"+token+"/signup", map[string]string{"email": "member@acme.test", "name": "Member", "password": "acme-member-pass-5521"}, "https://evil.test"); code != 403 {
		t.Fatalf("a foreign-origin sign-up got %d", code)
	}
	code, h, body := s.do("POST", "/api/v1/invites/"+token+"/signup", map[string]string{"email": "member@acme.test", "name": "Member", "password": "acme-member-pass-5521"}, s.baseURL)
	if code != 201 {
		t.Fatalf("sign-up: %d %s", code, body)
	}
	s.cookie = sessionCookie(h)
	if s.cookie == "" {
		t.Fatal("the sign-up set no session cookie")
	}
	m := s.me()
	if m.Email != "member@acme.test" || len(m.Spaces) != 1 || m.Spaces[0].ID != acme.ID || m.Spaces[0].Role != "member" {
		t.Fatalf("the member's spaces: %+v", m)
	}
	// The owner has set up no model yet; only an owner can, so the member is not sent to setup.
	if m.Setup.NeedsModel {
		t.Fatal("a member is sent to the model setup")
	}
	// The link works once.
	if code, _, _ = s.do("GET", "/api/v1/invites/"+token, nil, ""); code != 410 {
		t.Fatalf("a used invite link answers %d", code)
	}
	// A member cannot invite or change roles.
	if code, _, _ = s.do("POST", "/api/v1/spaces/"+acme.ID+"/invites", map[string]any{"role": "owner"}, s.baseURL); code != 403 {
		t.Fatalf("a member made an invite: %d", code)
	}

	// The member registers a passkey, signs out, and signs in with it.
	device := passkeystest.New("fettle.test", s.baseURL)
	var ceremony struct {
		Ceremony string          `json:"ceremony"`
		Options  json.RawMessage `json:"options"`
	}
	code, _, body = s.do("POST", "/api/auth/passkeys/register/begin", map[string]any{}, s.baseURL)
	if code != 200 {
		t.Fatalf("passkey register begin: %d %s", code, body)
	}
	_ = json.Unmarshal([]byte(body), &ceremony)
	cred, err := device.Register(ceremony.Options)
	if err != nil {
		t.Fatal(err)
	}
	if code, _, body = s.do("POST", "/api/auth/passkeys/register/finish", map[string]any{"ceremony": ceremony.Ceremony, "name": "Laptop", "credential": cred}, s.baseURL); code/100 != 2 {
		t.Fatalf("passkey register finish: %d %s", code, body)
	}
	if code, _, _ = s.do("POST", "/api/auth/sign-out", nil, s.baseURL); code/100 != 2 {
		t.Fatalf("sign-out: %d", code)
	}
	s.cookie = ""
	if code, _, _ = s.do("GET", "/api/v1/me", nil, ""); code != 401 {
		t.Fatalf("after sign-out /me answers %d", code)
	}
	code, _, body = s.do("POST", "/api/auth/passkeys/sign-in/begin", map[string]any{}, s.baseURL)
	if code != 200 {
		t.Fatalf("passkey sign-in begin: %d %s", code, body)
	}
	_ = json.Unmarshal([]byte(body), &ceremony)
	assertion, err := device.SignIn(ceremony.Options)
	if err != nil {
		t.Fatal(err)
	}
	code, h, body = s.do("POST", "/api/auth/passkeys/sign-in/finish", map[string]any{"ceremony": ceremony.Ceremony, "credential": assertion}, s.baseURL)
	if code != 200 || strings.Contains(body, `"mfaRequired":true`) {
		t.Fatalf("passkey sign-in: %d %s", code, body)
	}
	if s.cookie = sessionCookie(h); s.cookie == "" {
		t.Fatal("the passkey sign-in set no session cookie")
	}
	if m = s.me(); m.Email != "member@acme.test" || len(m.Spaces) != 1 || m.Spaces[0].ID != acme.ID || m.Spaces[0].Role != "member" {
		t.Fatalf("after the passkey sign-in: %+v", m)
	}

	// A fresh sign-in with the password works.
	s.signInAs("member@acme.test", "acme-member-pass-5521")
	if m = s.me(); len(m.Spaces) != 1 || m.Spaces[0].Role != "member" {
		t.Fatalf("after sign-in: %+v", m)
	}

	// The owner sees the member, makes them an owner, and cannot remove the last owner.
	s.signInAs(ownerEmail, ownerPassword)
	code, _, body = s.do("GET", "/api/v1/spaces/"+acme.ID+"/members", nil, "")
	var members struct {
		Members []struct {
			UserID string `json:"user_id"`
			Email  string `json:"email"`
			Role   string `json:"role"`
			You    bool   `json:"you"`
		} `json:"members"`
	}
	_ = json.Unmarshal([]byte(body), &members)
	if code != 200 || len(members.Members) != 2 {
		t.Fatalf("members: %d %s", code, body)
	}
	var ownerID, memberID string
	for _, x := range members.Members {
		if x.You {
			ownerID = x.UserID
		} else {
			memberID = x.UserID
		}
	}
	if code, _, body = s.do("PUT", "/api/v1/spaces/"+acme.ID+"/members/"+ownerID, map[string]string{"role": "member"}, s.baseURL); code != 409 {
		t.Fatalf("the last owner became a member: %d %s", code, body)
	}
	if code, _, body = s.do("DELETE", "/api/v1/spaces/"+acme.ID+"/members/"+memberID, nil, s.baseURL); code != 204 {
		t.Fatalf("remove member: %d %s", code, body)
	}
	// Member changes have audit rows (REQ-CORE-010).
	for _, action := range []string{"invite.create", "invite.accept", "member.remove"} {
		code, _, body := s.do("GET", "/api/v1/spaces/"+acme.ID+"/audit?action="+action, nil, "")
		if code != 200 || !strings.Contains(body, `"action":"`+action+`"`) {
			t.Fatalf("no audit row for %s: %d %s", action, code, body)
		}
	}
	s.signInAs("member@acme.test", "acme-member-pass-5521")
	if m = s.me(); len(m.Spaces) != 0 {
		t.Fatalf("a removed member still has spaces: %+v", m)
	}
	if code, _, _ = s.do("GET", "/api/v1/spaces/"+acme.ID+"/bots", nil, ""); code != 404 {
		t.Fatalf("a removed member reads acme bots: %d", code)
	}
}
