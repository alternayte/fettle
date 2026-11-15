package oauth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"time"
)

// Person is the account that a fake identity provider signs in.
type Person struct {
	Subject string
	Email   string
	Name    string
	Groups  []string
}

// OIDC is a fake OpenID Connect issuer for tests: discovery, an authorize endpoint that
// signs in the current person at once, a token endpoint with PKCE, and an RS256 key set.
type OIDC struct {
	*httptest.Server
	ClientID, ClientSecret string

	key *rsa.PrivateKey
	mu  sync.Mutex
	who Person
	// code -> the authorize request it answers
	codes map[string]oidcGrant
}

type oidcGrant struct {
	person    Person
	nonce     string
	challenge string
	redirect  string
}

// NewOIDC starts a fake issuer on a loopback address. Close it after the test.
func NewOIDC() *OIDC {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	o := &OIDC{ClientID: "fettle-" + random()[:8], ClientSecret: random(), key: key, codes: map[string]oidcGrant{}}
	o.Server = httptest.NewServer(http.HandlerFunc(o.serve))
	return o
}

// SignInAs sets the person that the next authorize request signs in.
func (o *OIDC) SignInAs(p Person) {
	o.mu.Lock()
	o.who = p
	o.mu.Unlock()
}

func (o *OIDC) serve(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		writeJSON(w, 200, map[string]any{
			"issuer": o.URL, "authorization_endpoint": o.URL + "/authorize", "token_endpoint": o.URL + "/token",
			"jwks_uri": o.URL + "/jwks", "response_types_supported": []string{"code"},
			"subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"},
			"code_challenge_methods_supported": []string{"S256"},
		})
	case "/jwks":
		pub := o.key.PublicKey
		writeJSON(w, 200, map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "fake-1",
			"n": b64(pub.N.Bytes()), "e": b64(big.NewInt(int64(pub.E)).Bytes()),
		}}})
	case "/authorize":
		q := r.URL.Query()
		if q.Get("client_id") != o.ClientID || q.Get("response_type") != "code" {
			http.Error(w, "bad authorize request", http.StatusBadRequest)
			return
		}
		code := random()
		o.mu.Lock()
		o.codes[code] = oidcGrant{person: o.who, nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), redirect: q.Get("redirect_uri")}
		o.mu.Unlock()
		back, err := url.Parse(q.Get("redirect_uri"))
		if err != nil {
			http.Error(w, "bad redirect_uri", http.StatusBadRequest)
			return
		}
		bq := back.Query()
		bq.Set("code", code)
		bq.Set("state", q.Get("state"))
		back.RawQuery = bq.Encode()
		http.Redirect(w, r, back.String(), http.StatusFound)
	case "/token":
		_ = r.ParseForm()
		id, secret, ok := r.BasicAuth()
		if !ok {
			id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
		}
		if id != o.ClientID || secret != o.ClientSecret {
			writeJSON(w, 401, map[string]string{"error": "invalid_client"})
			return
		}
		o.mu.Lock()
		g, ok := o.codes[r.PostForm.Get("code")]
		delete(o.codes, r.PostForm.Get("code"))
		o.mu.Unlock()
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if !ok || g.redirect != r.PostForm.Get("redirect_uri") || (g.challenge != "" && b64(sum[:]) != g.challenge) {
			writeJSON(w, 400, map[string]string{"error": "invalid_grant"})
			return
		}
		now := time.Now()
		claims := map[string]any{
			"iss": o.URL, "aud": o.ClientID, "sub": g.person.Subject, "iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(),
			"nonce": g.nonce, "email": g.person.Email, "email_verified": true, "name": g.person.Name, "groups": g.person.Groups,
		}
		writeJSON(w, 200, map[string]any{"access_token": random(), "token_type": "Bearer", "expires_in": 300, "id_token": o.sign(claims)})
	default:
		http.NotFound(w, r)
	}
}

func (o *OIDC) sign(claims map[string]any) string {
	h, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "fake-1"})
	c, _ := json.Marshal(claims)
	in := b64(h) + "." + b64(c)
	sum := sha256.Sum256([]byte(in))
	sig, err := rsa.SignPKCS1v15(rand.Reader, o.key, crypto.SHA256, sum[:])
	if err != nil {
		panic(err)
	}
	return in + "." + b64(sig)
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
