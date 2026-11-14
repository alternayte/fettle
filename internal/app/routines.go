package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/agent"
	"github.com/alternayte/fettle/internal/envelopes"
	"github.com/alternayte/fettle/internal/routines"
	"github.com/alternayte/fettle/internal/settings"
)

func init() {
	settings.Register(settings.Def{Key: "routines.max_per_bot", Scope: "space", Default: 50, Doc: "Routines one bot can have."})
}

// setupRoutines connects routine firings to the inbox (§13.2).
func (a *App) setupRoutines() {
	a.Routines = routines.New(a.DB.Engine, a.Audit, a.Vault)
	a.Routines.MaxPerBot = func(ctx context.Context, sp uuid.UUID) int { return a.Settings.Int(ctx, &sp, "routines.max_per_bot") }
	a.Routines.Fire = a.fireRoutine
	a.Agent.RoutineFirstSend = func(ctx context.Context, id uuid.UUID) bool { return a.Routines.FirstSend(ctx, id) }
	a.Agent.RoutineSendApproved = a.Routines.MarkSendApproved
}

// fireRoutine creates the envelope of one firing: origin routine, reduced intent, the
// idempotency key routine:due-time, and results in the bot's direct chat.
func (a *App) fireRoutine(ctx context.Context, f routines.Firing) (uuid.UUID, error) {
	r := f.Routine
	chat, err := a.Chats.DirectFor(ctx, r.SpaceID, r.BotID)
	if err != nil {
		return uuid.Nil, err
	}
	goal := r.Prompt
	if f.Payload != "" {
		goal += "\n\nThe webhook sent this body:\n" + f.Payload
	}
	if len(goal) > 8000 {
		goal = goal[:8000]
	}
	kind := "routine"
	if f.Source == "webhook" {
		kind = "webhook"
	}
	due := f.DueAt.UTC()
	bot, chatID := r.BotID, chat.ID()
	env := &envelopes.Envelope{SpaceID: r.SpaceID, Kind: kind, Origin: "routine", Requester: envelopes.Requester{Type: "routine", ID: r.ID().String()},
		OwnerBotID: &bot, ChatID: &chatID, Goal: goal, Capabilities: agent.UserCapabilities, Deadline: &due,
		IdempotencyKey: fmt.Sprintf("%s:%s", r.ID(), due.Format(time.RFC3339Nano))}
	if _, err := a.Agent.Submit(ctx, env); err != nil {
		return uuid.Nil, err
	}
	return env.ID(), nil
}

// webhook fires a webhook routine. It verifies the signature before it reads the body as
// anything but bytes (SI-17).
func (a *App) webhook(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": routines.ErrSignature.Error()})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "the body is larger than 1 MiB"})
		return
	}
	rt, err := a.Routines.Verify(r.Context(), id, r.Header.Get("X-Fettle-Signature"), r.Header.Get("X-Fettle-Secret"), body)
	if errors.Is(err, routines.ErrSignature) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "the webhook could not be checked"})
		return
	}
	payload := string(body)
	var v any
	if json.Unmarshal(body, &v) == nil {
		if b, err := json.MarshalIndent(v, "", "  "); err == nil {
			payload = string(b)
		}
	}
	envID, err := a.Routines.FireWebhook(r.Context(), rt, strings.TrimSpace(payload))
	if errors.Is(err, routines.ErrDisabled) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "the routine could not start"})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"envelope_id": envID.String()})
}
