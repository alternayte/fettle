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
	// CanFork reports whether the zone of a bot can fork: native fork support or a
	// workspace small enough to copy (rule R3).
	CanFork func(ctx context.Context, spaceID, botID uuid.UUID) bool
	// ScreenUse takes the bot's screen for one action and returns the release function.
	// ErrScreenControlled means a person controls the screen, so the run waits.
	ScreenUse func(ctx context.Context, spaceID, botID uuid.UUID) (func(), error)
	// Computer brings up the computer of a bot's zone; ErrNoComputer means wait.
	Computer func(ctx context.Context, spaceID, botID uuid.UUID) error
	HTTP     *http.Client
	// RoutineFirstSend reports that a routine has had no allowed external send yet; that
	// send is a hard ask (§11.2). RoutineSendApproved records the allowed card.
	RoutineFirstSend    func(ctx context.Context, routineID uuid.UUID) bool
	RoutineSendApproved func(ctx context.Context, routineID uuid.UUID) error
	// Dynamic returns the tools that a bot has beyond the built-in ones: the tools of the
	// connector accounts that its zone holds (§12).
	Dynamic func(ctx context.Context, spaceID, botID uuid.UUID) ([]Tool, error)
	// SkillIndex returns the name and description of each skill of a bot (§13.1).
	SkillIndex func(ctx context.Context, spaceID, botID uuid.UUID) []Skill
	// LoadSkill returns the full text of a bot's skill for its run, and copies the skill's
	// files into the bot home when it has files.
	LoadSkill func(ctx context.Context, env *ToolEnv, name string) (string, error)
	// Notify sends a push notification to the members of a space (§15.2). url is the
	// path of the card or the run in the app.
	Notify func(ctx context.Context, spaceID uuid.UUID, kind, title, body, url string)
	// RoutineNotify returns the notification setting of a routine: always, on_failure or never.
	RoutineNotify func(ctx context.Context, routineID uuid.UUID) string
	// RescueAfter is the River rescue timeout; run jobs use it as their timeout.
	RescueAfter time.Duration
	Log         *slog.Logger
	Metrics     Metrics
	now         func() time.Time
	changes     map[string]Change
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
	ag.registerTakeover()
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
