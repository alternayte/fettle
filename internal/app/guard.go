package app

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/auth"
	"github.com/alternayte/fettle/internal/spaces"
)

// OwnerOnlyRoute is the rule of §16 and Appendix G for a space route: members chat, read
// and steer runs; only owners change bots, skills, routines, settings, zones, grants,
// connectors, the vault, approvals, budgets, members and invites, and see learning.
// rest is the path after /spaces/{space}.
func OwnerOnlyRoute(method, rest string) bool {
	seg := strings.Split(strings.TrimPrefix(rest, "/"), "/")
	read := method == http.MethodGet
	switch seg[0] {
	case "":
		return !read
	case "connectors", "zones", "computer-providers", "vault", "policy", "approval-mode", "settings", "usage", "audit",
		"model-providers", "model-roles", "learning", "invites", "templates", "onboarding", "export":
		return true
	case "memory", "skills", "routines":
		return !read
	case "bots":
		// Members take over a screen and share a sign-in, as they sign in by takeover.
		if len(seg) >= 3 && (seg[2] == "screen" || seg[2] == "sign-ins") {
			return false
		}
		return !read
	case "runs":
		return len(seg) >= 3 && seg[2] == "draft-skill"
	case "teach":
		// Members teach; only owners save a taught skill (§13.3).
		return len(seg) >= 3 && seg[2] == "save"
	case "members":
		// A member may leave (DELETE of themself); the handler checks the rest.
		return method == http.MethodPut || method == http.MethodPost
	}
	return false
}

// spaceGuard checks the space of a /spaces/{space} route before the request is read or
// validated (SI-12): a person who is not a member gets 404, and a member gets 403 on a
// route for owners. The handlers check again.
func (a *App) spaceGuard(next http.Handler) http.Handler {
	prefix := APIPrefix + "/spaces/"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, prefix) {
			next.ServeHTTP(w, r)
			return
		}
		id, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, prefix), "/")
		sid, err := uuid.Parse(id)
		p := auth.From(r.Context())
		if err != nil || p == nil {
			next.ServeHTTP(w, r)
			return
		}
		role, err := a.Spaces.Role(r.Context(), sid, p.UserID)
		if errors.Is(err, spaces.ErrNotMember) {
			writeJSON(w, http.StatusNotFound, map[string]any{"status": 404, "title": "Not Found", "detail": "space not found"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"status": 500, "title": "Internal Server Error", "detail": "Fettle could not check the space."})
			return
		}
		if role != spaces.RoleOwner && OwnerOnlyRoute(r.Method, "/"+rest) {
			writeJSON(w, http.StatusForbidden, map[string]any{"status": 403, "title": "Forbidden", "detail": "Only an owner of this space can do this."})
			return
		}
		next.ServeHTTP(w, r)
	})
}

type maintenanceKey struct{}

// InMaintenance reports whether ctx belongs to the maintenance tick, which works for
// every space by design (idle sleep, routines, digests, patch jobs).
func InMaintenance(ctx context.Context) bool { return ctx.Value(maintenanceKey{}) != nil }
