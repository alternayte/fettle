package agent

import (
	"context"
	"errors"

	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/drel"

	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/bots"
	"github.com/alternayte/fettle/internal/chats"
	"github.com/alternayte/fettle/internal/envelopes"
	"github.com/alternayte/fettle/internal/models"
	"github.com/alternayte/fettle/internal/policy"
	"github.com/alternayte/fettle/internal/runs"
	"github.com/alternayte/fettle/internal/settings"
	"github.com/alternayte/fettle/internal/spaces"
	"github.com/alternayte/fettle/internal/usage"
	"github.com/alternayte/fettle/internal/vault"
)

// Agent holds the services that the agent loop uses.
type Agent struct {
	E         *drel.Engine
	Audit     *audit.Log
	Bots      *bots.Service
	Chats     *chats.Service
	Envelopes *envelopes.Service
	Runs      *runs.Service
	Journal   *runs.Journal
	Models    *models.Service
	Usage     *usage.Service
	Settings  *settings.Store
	Spaces    *spaces.Service
	Vault     *vault.Vault
	Tools     *Registry
	Policy    *policy.Service
	// Computer brings up the computer of a bot's zone; ErrNoComputer means wait.
	Computer func(ctx context.Context, spaceID, botID uuid.UUID) error
	HTTP     *http.Client
	Log      *slog.Logger
	Metrics  Metrics
	now      func() time.Time
	changes  map[string]Change
}

// Metrics receives counters from the loop.
type Metrics interface {
	RunFinished(status, step string)
	ModelCall(role, model string, tokensIn, tokensOut int64, cost float64)
	Card(kind string)
}

// New returns an agent with the built-in tools registered.
func New(a Agent) *Agent {
	ag := &a
	if ag.Tools == nil {
		ag.Tools = NewRegistry()
	}
	if ag.Log == nil {
		ag.Log = slog.Default()
	}
	if ag.now == nil {
		ag.now = func() time.Time { return time.Now().UTC() }
	}
	ag.registerMemoryTools()
	return ag
}

// SetClock replaces the clock (tests).
func (a *Agent) SetClock(now func() time.Time) {
	a.now = now
	a.Journal.SetClock(now)
	a.Runs.SetClock(now)
}

// Now returns the agent's clock time.
func (a *Agent) Now() time.Time { return a.now() }

// ErrNoComputer is returned by Agent.Computer when no computer is free; the run waits.
var ErrNoComputer = errors.New("waiting for a free computer")
