package app

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/alternayte/fettle/internal/auth"
	"github.com/alternayte/fettle/ui"
)

// APIPrefix is the prefix of every API route.
const APIPrefix = "/api/v1"

// Handler returns the full HTTP handler: health, metrics, the API and the UI.
func (a *App) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer, securityHeaders)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	r.Get("/readyz", a.readyz)
	r.Handle("/metrics", promhttp.HandlerFor(a.Metrics.Registry, promhttp.HandlerOpts{}))

	// Webhooks sign their bodies instead of signing in (SI-17).
	r.Post("/hooks/{id}", a.webhook)
	// The OAuth sign-in of a connector returns here; its state is the secret (§12.1).
	r.Get(APIPrefix+"/oauth/callback", a.oauthCallback)
	// A screen stream carries its own short-lived token (§10.8).
	r.Get(APIPrefix+"/screen/stream", a.screenStream)
	if a.SignIn != nil {
		r.Handle("/api/auth/*", a.SignIn.Handler())
	}
	// Sign-up by invite link writes the session cookie, so it is outside the API group.
	r.Post(APIPrefix+"/invites/{token}/signup", a.inviteSignup)
	r.Group(func(g chi.Router) {
		if a.Cfg.Mode == ModeLocal {
			g.Use(auth.LocalAuth(a.LocalToken, func(ctx context.Context) (*auth.User, error) { return a.LocalOwner(ctx) }, publicAPIPath))
		} else {
			g.Use(a.SignIn.Middleware(publicAPIPath))
		}
		a.registerAPI(humachi.New(g, apiConfig(a.Cfg.Version)))
	})
	r.Handle("/*", spa())
	return r
}

func publicAPIPath(p string) bool {
	return p == APIPrefix+"/openapi.json" || p == APIPrefix+"/openapi.yaml" || strings.HasPrefix(p, APIPrefix+"/schemas/") || p == APIPrefix+"/auth/local" || p == APIPrefix+"/instance" ||
		(strings.HasPrefix(p, APIPrefix+"/invites/") && strings.Count(p, "/") == 4)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		}
		next.ServeHTTP(w, r)
	})
}

// readyz checks the database, the migrations and the object store (§17).
func (a *App) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	checks := map[string]string{}
	ok := true
	if err := a.DB.Engine.Ping(ctx); err != nil {
		checks["database"], ok = err.Error(), false
	} else {
		checks["database"] = "ok"
	}
	if err := a.DB.Pending(ctx); err != nil {
		checks["migrations"], ok = err.Error(), false
	} else {
		checks["migrations"] = "ok"
	}
	if err := a.Objects.Check(ctx); err != nil {
		checks["object_store"], ok = err.Error(), false
	} else {
		checks["object_store"] = "ok"
	}
	code := http.StatusOK
	if !ok {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]any{"ready": ok, "checks": checks})
}

// spa serves the embedded UI and falls back to index.html for app routes.
func spa() http.Handler {
	dist, err := fs.Sub(ui.Dist, "dist")
	if err != nil {
		panic(err)
	}
	files := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" {
			if st, err := fs.Stat(dist, p); err == nil && !st.IsDir() {
				switch {
				case strings.HasPrefix(p, "assets/"):
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				case p == "sw.js":
					// A new service worker must reach browsers at once.
					w.Header().Set("Cache-Control", "no-cache")
				case strings.HasSuffix(p, ".webmanifest"):
					w.Header().Set("Content-Type", "application/manifest+json")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		b, err := fs.ReadFile(dist, "index.html")
		if err != nil {
			http.Error(w, "The UI is not built. Run: just ui", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(b)
	})
}

// apiError maps service errors to HTTP problems.
func apiError(err error) error {
	if err == nil {
		return nil
	}
	var se huma.StatusError
	if errors.As(err, &se) {
		return err
	}
	for _, m := range errorMap {
		for _, target := range m.errs {
			if errors.Is(err, target) {
				return huma.NewError(m.code, err.Error())
			}
		}
	}
	var ve interface{ Problems() []string }
	if errors.As(err, &ve) {
		return huma.Error422UnprocessableEntity(err.Error())
	}
	return err
}

// OpenAPI returns the OpenAPI document without storage, for code generation.
func OpenAPI(version string) ([]byte, error) {
	a := &App{Cfg: &Config{Version: version}}
	r := chi.NewRouter()
	api := humachi.New(r, apiConfig(version))
	a.registerAPI(api)
	return api.OpenAPI().MarshalJSON()
}

func apiConfig(version string) huma.Config {
	cfg := huma.DefaultConfig("Fettle API", version)
	cfg.OpenAPIPath = APIPrefix + "/openapi"
	cfg.DocsPath = ""
	cfg.CreateHooks = nil
	cfg.SchemasPath = APIPrefix + "/schemas"
	cfg.Info.Description = "The HTTP API of Fettle. Every route needs the local token in local mode, or a session or access token in server mode."
	return cfg
}
