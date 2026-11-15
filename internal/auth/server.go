package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	authall "github.com/alternayte/auth-all"
	"github.com/alternayte/auth-all/apierr"
	"github.com/alternayte/auth-all/plugins/apikeys"
	"github.com/alternayte/auth-all/plugins/roles"
	"github.com/alternayte/auth-all/ratelimit"
	"github.com/alternayte/auth-all/ratelimit/storelimit"
	"github.com/alternayte/auth-all/store"
	"github.com/alternayte/auth-all/store/postgres"
	"github.com/alternayte/auth-all/store/sqlite"
	"github.com/alternayte/drel"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SessionCookie is the session cookie of server mode.
const SessionCookie = "fettle_session"

// TokenPrefix starts every personal access token.
const TokenPrefix = "fet_"

// Server is the sign-in of server mode (§16): accounts with email and password,
// database sessions, sign-in rate limits and personal access tokens, through auth-all.
type Server struct {
	Auth    *authall.Auth
	users   *UserStore
	baseURL string
}

// ServerConfig opens the server-mode sign-in on the Fettle database.
type ServerConfig struct {
	SQL     *sql.DB       // SQLite
	PGPool  *pgxpool.Pool // PostgreSQL
	BaseURL string        // FETTLE_BASE_URL
	Clock   func() time.Time
}

// NewServer builds the sign-in. It changes no schema; Migrate does.
func NewServer(e *drel.Engine, c ServerConfig) (*Server, error) {
	var st store.Store
	if c.PGPool != nil {
		s, err := postgres.NewPool(c.PGPool, postgres.AllowStatementCache())
		if err != nil {
			return nil, err
		}
		st = s
	} else {
		st = sqlite.New(c.SQL)
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, errors.New("FETTLE_BASE_URL must be the public URL of this server, for example https://fettle.example.com")
	}
	limiter, err := storelimit.New(st, []ratelimit.Rule{
		// The eleventh sign-in from one address in one minute gets 429 (SCN-AUTH-004).
		{Operation: ratelimit.OpSignIn, Scope: ratelimit.ScopeIP, Limit: 10, Window: time.Minute},
		{Operation: ratelimit.OpSignIn, Scope: ratelimit.ScopeEmail, Limit: 10, Window: 15 * time.Minute},
	})
	if err != nil {
		return nil, err
	}
	secure := u.Scheme == "https"
	opts := []authall.Option{
		authall.WithStore(st),
		authall.WithBaseURL(strings.TrimRight(c.BaseURL, "/")),
		authall.WithBasePath("/api/auth"),
		authall.WithEmailPassword(),
		authall.WithCookie(authall.CookieOptions{Name: SessionCookie, SameSite: http.SameSiteLaxMode, Secure: &secure}),
		// 12 hours idle, 30 days at most (§16).
		authall.WithSession(authall.SessionOptions{TTL: 30 * 24 * time.Hour, IdleTimeout: 12 * time.Hour, TouchInterval: 5 * time.Minute}),
		authall.WithStrictOriginCheck(),
		authall.WithRateLimiter(limiter),
		authall.WithPlugins(
			roles.New(roles.Hierarchy("user", "admin")),
			apikeys.New(apikeys.Prefix(TokenPrefix), apikeys.MaxTTL(366*24*time.Hour), apikeys.AllowNoExpiry(), apikeys.AdminRole("admin")),
		),
	}
	if c.Clock != nil {
		opts = append(opts, authall.WithClock(c.Clock))
	}
	a, err := authall.New(opts...)
	if err != nil {
		return nil, err
	}
	return &Server{Auth: a, users: NewUserStore(e), baseURL: strings.TrimRight(c.BaseURL, "/")}, nil
}

// Migrate applies the auth-all tables. The caller holds the migration lock.
func (s *Server) Migrate(ctx context.Context) error {
	_, err := s.Auth.Migrate(ctx)
	return err
}

// CreateUser adds an account with a password and its Fettle user row.
func (s *Server) CreateUser(ctx context.Context, email, name, password string, admin bool) (*User, error) {
	au, err := s.Auth.CreateUser(ctx, authall.CreateUserInput{Email: email, Password: password, DisplayName: name, EmailVerified: true})
	if err != nil {
		return nil, err
	}
	return s.users.Ensure(ctx, au.Email, name, admin)
}

// Handler serves the auth-all routes under /api/auth. Self sign-up is closed: an owner
// creates accounts (fettle admin create-user) until invite links exist.
func (s *Server) Handler() http.Handler {
	h := s.Auth.Handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/auth/sign-up") {
			writeErr(w, http.StatusForbidden, "Sign-up is closed. Ask the owner of this server for an account.")
			return
		}
		h.ServeHTTP(w, r)
	})
}

// Middleware resolves a session cookie or a personal access token to a Fettle user.
// Cookie requests that change state must come from this server's origin (SI-13).
func (s *Server) Middleware(public func(path string) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ap := authall.PrincipalFrom(r.Context())
			if ap == nil {
				if public(r.URL.Path) {
					next.ServeHTTP(w, r)
					return
				}
				writeErr(w, http.StatusUnauthorized, "Sign in first.")
				return
			}
			if ap.ViaCookie && unsafeMethod(r.Method) && !SameOrigin(r, s.baseURL) {
				writeErr(w, http.StatusForbidden, "This request comes from another origin.")
				return
			}
			u, err := s.users.Ensure(r.Context(), ap.User.Email, ap.User.DisplayName, false)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, "Fettle could not load your account.")
				return
			}
			if u.Status != "active" || ap.User.DisabledAt != nil {
				writeErr(w, http.StatusForbidden, "This account is disabled.")
				return
			}
			via := "session"
			if ap.Method == authall.MethodAPIKey {
				via = "access_token"
			}
			p := &Principal{UserID: u.ID(), Email: u.Email, IsAdmin: u.IsAdmin, Via: via}
			next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
		})
		return s.Auth.LoadSession(inner)
	}
}

// SignUp creates an account with a password and signs it in on this response: an invited
// person's first sign-in (§16). Self sign-up without an invite stays closed.
func (s *Server) SignUp(ctx context.Context, w http.ResponseWriter, email, name, password, clientIP string) (*User, error) {
	au, err := s.Auth.CreateUser(ctx, authall.CreateUserInput{Email: email, Password: password, DisplayName: name, EmailVerified: true})
	if err != nil {
		return nil, err
	}
	u, err := s.users.Ensure(ctx, au.Email, name, false)
	if err != nil {
		return nil, err
	}
	res, err := s.Auth.SignIn(ctx, authall.SignInInput{Email: email, Password: password, ClientIP: clientIP})
	if err != nil {
		return nil, err
	}
	if res.Session != nil {
		s.Auth.SetSessionCookie(w, res.Token, res.Session.ExpiresAt)
	}
	return u, nil
}

// PublicError returns the status and the message of an auth-all error, when err is one.
func PublicError(err error) (int, string, bool) {
	var e *apierr.Error
	if errors.As(err, &e) {
		return e.Status, e.Message, true
	}
	return 0, "", false
}
