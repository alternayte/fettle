package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

// Principal is the caller of a request.
type Principal struct {
	UserID  uuid.UUID
	Email   string
	IsAdmin bool
	// Via is local_token, session or access_token.
	Via string
}

type ctxKey struct{}

// WithPrincipal stores the caller in ctx.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// From returns the caller of ctx, or nil.
func From(ctx context.Context) *Principal {
	p, _ := ctx.Value(ctxKey{}).(*Principal)
	return p
}

// LocalCookie is the cookie that carries proof of the local token for the browser UI.
const LocalCookie = "fettle_local"

// CookieValue derives the cookie value from the local token. The raw token never goes into the cookie.
func CookieValue(token string) string {
	h := sha256.Sum256([]byte("fettle-ui-cookie:" + token))
	return hex.EncodeToString(h[:])
}

func equal(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

// SameOrigin reports whether a state-changing request comes from this server's origin (SI-13).
// Requests without Origin and Referer (CLI, curl) pass; they cannot carry a browser cookie by accident.
func SameOrigin(r *http.Request, allowed ...string) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		ref := r.Header.Get("Referer")
		if ref == "" {
			return true
		}
		u, err := url.Parse(ref)
		if err != nil {
			return false
		}
		o = u.Scheme + "://" + u.Host
	}
	u, err := url.Parse(o)
	if err != nil {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	for _, a := range allowed {
		if strings.EqualFold(strings.TrimRight(a, "/"), strings.TrimRight(o, "/")) {
			return true
		}
	}
	return false
}

func unsafeMethod(m string) bool {
	return m != http.MethodGet && m != http.MethodHead && m != http.MethodOptions
}

// LocalAuth accepts only requests that carry the local token, as a bearer token or as
// the UI cookie (SI-11). owner resolves the local owner. public lists paths that need no token.
func LocalAuth(token string, owner func(context.Context) (*User, error), public func(path string) bool) func(http.Handler) http.Handler {
	cookie := CookieValue(token)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if public(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			ok, viaCookie := false, false
			if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") && equal(strings.TrimPrefix(h, "Bearer "), token) {
				ok = true
			} else if c, err := r.Cookie(LocalCookie); err == nil && equal(c.Value, cookie) {
				ok, viaCookie = true, true
			}
			if !ok {
				writeErr(w, http.StatusUnauthorized, "The local token is missing or wrong. Open Fettle from the app or from the link that fettle up prints.")
				return
			}
			if viaCookie && unsafeMethod(r.Method) && !SameOrigin(r) {
				writeErr(w, http.StatusForbidden, "This request comes from another origin.")
				return
			}
			u, err := owner(r.Context())
			if err != nil || u == nil {
				writeErr(w, http.StatusInternalServerError, "The local owner is missing.")
				return
			}
			p := &Principal{UserID: u.ID(), Email: u.Email, IsAdmin: true, Via: "local_token"}
			next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
		})
	}
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"status": code, "title": http.StatusText(code), "detail": msg})
}
