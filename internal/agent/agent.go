package agent

import (
	"context"
	"errors"
	"sync"

	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/drel"

	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/bots"
	"github.com/alternayte/fettle/internal/chats"
	"github.com/alternayte/fettle/internal/envelopes"
	"github.com/alternayte/fettle/internal/learning"
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
	// Learning records steering signals and run scores (§14). Nil turns them off.
	Learning *learning.Service
	// ApplyPatch makes a new skill version from an applied patch card (§14.3).
	ApplyPatch func(ctx context.Context, user audit.Actor, spaceID uuid.UUID, card chats.Card) error
	// bg counts background work that uses the database after a request returns.
	bg     *sync.WaitGroup
	Vault  *vault.Vault
	Tools  *Registry
	Policy *policy.Service
	// Checker opens the zone computer of a bot for file, command and citations checks.
	Checker func(ctx context.Context, spaceID, botID uuid.UUID) (Checker, error)
	// Hire creates the bots of an allowed proposal card: bots, paused routines, skills and
	// grant cards (§9.8).
	Hire func(ctx context.Context, user audit.Actor, spaceID uuid.UUID, list []Proposal) error
	// Forker makes and ends the forks of forked sub-agents (§9.4).
	Forker Forker
	// DeliverAttachments copies the attachments of a user message into a bot's home at
	// attachments/<id>/<name>.
	DeliverAttachments func(ctx context.Context, spaceID, botID uuid.UUID, atts []chats.Attachment) error
	// CopyShared copies one file from a zone's shared folder to another zone (handoff, §9.7).
	CopyShared func(ctx context.Context, fromZone, toZone uuid.UUID, from, to string) error
	// SubagentEnded frees what a sub-agent run used on the computer (its browser context).
	SubagentEnded func(ctx context.Context, spaceID, botID, runID uuid.UUID)
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
	ag.bg = &sync.WaitGroup{}
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
	ag.registerSubagentTools()
	ag.registerHandoff()
	ag.registerPropose()
	ag.registerSuggest()
	ag.registerAskUser()
	ag.registerTeamTools()
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

// Wait waits for the background work of requests, such as the correction check.
func (a *Agent) Wait() { a.bg.Wait() }
