// Package oauth is the fake OAuth 2.1 authorization server of the tests (§24.4): metadata,
// dynamic client registration (RFC 7591), authorization code with PKCE S256 and refresh.
// It approves every authorization at once, as a person who clicks Allow.
package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Server is the fake authorization server.
type Server struct {
	URL string // the issuer; set after the HTTP server starts

	mu       sync.Mutex
	clients  map[string][]string // client ID -> redirect URIs
	codes    map[string]grant
	tokens   map[string]tokenInfo
	refresh  map[string]tokenInfo
	PKCEUsed int // authorization codes redeemed with a valid S256 verifier
	// User names the account that the next authorization signs in as.
	User string
}

type grant struct {
	client, redirect, challenge, user string
}

type tokenInfo struct {
	client, user string
	expires      time.Time
}

// New returns the fake server.
func New() *Server {
	return &Server{clients: map[string][]string{}, codes: map[string]grant{}, tokens: map[string]tokenInfo{}, refresh: map[string]tokenInfo{}, User: "owner@acme.test"}
}

func random() string {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Registered returns the number of registered clients.
func (s *Server) Registered() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.clients)
}

// UserOf returns the account of a valid access token, or "".
func (s *Server) UserOf(token string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tokens[token]
	if !ok || time.Now().After(t.expires) {
		return ""
	}
	return t.user
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/.well-known/oauth-authorization-server", "/.well-known/openid-configuration":
		writeJSON(w, 200, map[string]any{
			"issuer": s.URL, "authorization_endpoint": s.URL + "/authorize", "token_endpoint": s.URL + "/token",
			"registration_endpoint": s.URL + "/register", "code_challenge_methods_supported": []string{"S256"},
			"grant_types_supported": []string{"authorization_code", "refresh_token"}, "response_types_supported": []string{"code"},
			"token_endpoint_auth_methods_supported": []string{"none", "client_secret_post"},
		})
	case "/register":
		var in struct {
			RedirectURIs []string `json:"redirect_uris"`
			ClientName   string   `json:"client_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || len(in.RedirectURIs) == 0 {
			writeJSON(w, 400, map[string]string{"error": "invalid_redirect_uri"})
			return
		}
		id := "client-" + random()
		s.mu.Lock()
		s.clients[id] = in.RedirectURIs
		s.mu.Unlock()
		writeJSON(w, 201, map[string]any{"client_id": id, "redirect_uris": in.RedirectURIs, "client_name": in.ClientName, "token_endpoint_auth_method": "none"})
	case "/authorize":
		q := r.URL.Query()
		s.mu.Lock()
		uris, ok := s.clients[q.Get("client_id")]
		user := s.User
		s.mu.Unlock()
		valid := false
		for _, u := range uris {
			valid = valid || u == q.Get("redirect_uri")
		}
		if !ok || !valid || q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
			http.Error(w, "invalid authorization request", http.StatusBadRequest)
			return
		}
		code := random()
		s.mu.Lock()
		s.codes[code] = grant{client: q.Get("client_id"), redirect: q.Get("redirect_uri"), challenge: q.Get("code_challenge"), user: user}
		s.mu.Unlock()
		u, _ := url.Parse(q.Get("redirect_uri"))
		v := u.Query()
		v.Set("code", code)
		v.Set("state", q.Get("state"))
		u.RawQuery = v.Encode()
		http.Redirect(w, r, u.String(), http.StatusFound)
	case "/token":
		_ = r.ParseForm()
		f := r.PostForm
		s.mu.Lock()
		defer s.mu.Unlock()
		var t tokenInfo
		switch f.Get("grant_type") {
		case "authorization_code":
			g, ok := s.codes[f.Get("code")]
			delete(s.codes, f.Get("code"))
			sum := sha256.Sum256([]byte(f.Get("code_verifier")))
			if !ok || g.client != f.Get("client_id") || g.redirect != f.Get("redirect_uri") || base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
				writeJSON(w, 400, map[string]string{"error": "invalid_grant"})
				return
			}
			s.PKCEUsed++
			t = tokenInfo{client: g.client, user: g.user}
		case "refresh_token":
			old, ok := s.refresh[f.Get("refresh_token")]
			delete(s.refresh, f.Get("refresh_token"))
			if !ok {
				writeJSON(w, 400, map[string]string{"error": "invalid_grant"})
				return
			}
			t = old
		default:
			writeJSON(w, 400, map[string]string{"error": "unsupported_grant_type"})
			return
		}
		t.expires = time.Now().Add(time.Hour)
		access, ref := "at-"+random(), "rt-"+random()
		s.tokens[access] = t
		s.refresh[ref] = t
		writeJSON(w, 200, map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": 3600, "refresh_token": ref, "scope": "mail"})
	default:
		http.NotFound(w, r)
	}
}

// BearerOf returns the bearer token of a request.
func BearerOf(r *http.Request) string {
	t, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return t
}

// AccessTokens returns the access tokens that the server issued.
func (s *Server) AccessTokens() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.tokens))
	for t := range s.tokens {
		out = append(out, t)
	}
	return out
}
