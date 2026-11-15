package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/agent"
	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/auth"
	"github.com/alternayte/fettle/internal/bots"
	"github.com/alternayte/fettle/internal/chats"
	"github.com/alternayte/fettle/internal/envelopes"
	"github.com/alternayte/fettle/internal/models"
	"github.com/alternayte/fettle/internal/routines"
	"github.com/alternayte/fettle/internal/runs"
	"github.com/alternayte/fettle/internal/settings"
	"github.com/alternayte/fettle/internal/spaces"
	"github.com/alternayte/fettle/internal/vault"
)

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

var errorMap = []struct {
	code int
	errs []error
}{
	{http.StatusNotFound, []error{routines.ErrNotFound, bots.ErrNotFound, chats.ErrNotFound, chats.ErrMsgNotFound, chats.ErrCardNotFound, vault.ErrNotFound, models.ErrNotFound, runs.ErrNotFound, envelopes.ErrNotFound, spaces.ErrNotMember, bots.ErrNoVersion}},
	{http.StatusConflict, []error{bots.ErrNameTaken, chats.ErrCardAnswered, bots.ErrLimit, bots.ErrPrimaryLast}},
	{http.StatusUnprocessableEntity, []error{bots.ErrBadName, bots.ErrMemoryFull, bots.ErrFactNotFound, bots.ErrFactAmbiguous, chats.ErrEmptyMessage, chats.ErrMessageLength, models.ErrNoToolsOwner, models.ErrUntestedOwner, models.ErrUnknownRole, models.ErrInvalidBaseURL, settings.ErrUnknown, agent.ErrBadAction}},
}

// ctxSpace resolves the caller's role in a space. A non-member gets 404, so no data
// of another space leaks (SI-12). ownerOnly turns a member into 403 (§16).
func (a *App) ctxSpace(ctx context.Context, spaceID uuid.UUID, ownerOnly bool) (*auth.Principal, string, error) {
	p := auth.From(ctx)
	if p == nil {
		return nil, "", huma.Error401Unauthorized("Sign in first.")
	}
	role, err := a.Spaces.Role(ctx, spaceID, p.UserID)
	if errors.Is(err, spaces.ErrNotMember) {
		return nil, "", huma.Error404NotFound("space not found")
	}
	if err != nil {
		return nil, "", err
	}
	if ownerOnly && role != spaces.RoleOwner {
		return nil, "", huma.Error403Forbidden("Only an owner of this space can do this.")
	}
	return p, role, nil
}

func actorOf(p *auth.Principal) audit.Actor { return audit.Actor{Kind: "user", ID: p.UserID.String()} }

// SpacePath is the space part of most routes.
type SpacePath struct {
	Space uuid.UUID `path:"space" doc:"Space ID"`
}

func (a *App) registerAPI(api huma.API) {
	a.registerMe(api)
	a.registerBots(api)
	a.registerChats(api)
	a.registerRuns(api)
	a.registerModels(api)
	a.registerVault(api)
	a.registerZones(api)
	a.registerPolicy(api)
	a.registerRoutines(api)
	a.registerPush(api)
	a.registerConnectors(api)
	a.registerSkills(api)
	a.registerScreens(api)
	a.registerBotLife(api)
	a.registerGroups(api)
	a.registerDesktop(api)
	a.registerPrepare(api)
	a.registerTemplates(api)
	a.registerOnboarding(api)
	a.registerEvals(api)
	a.registerComputerOps(api)
	a.registerSettings(api)
	a.registerAdmin(api)
	a.registerDiagnostics(api)
}

func op(id, method, path, summary string, tags ...string) huma.Operation {
	return huma.Operation{OperationID: id, Method: method, Path: APIPrefix + path, Summary: summary, Tags: tags}
}
