package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/alternayte/fettle/internal/agent"
	"github.com/alternayte/fettle/internal/chats"
	"github.com/alternayte/fettle/internal/computers"
	"github.com/alternayte/fettle/internal/jobs"
	"github.com/alternayte/fettle/internal/providers/applecontainer"
	"github.com/alternayte/fettle/internal/providers/docker"
	"github.com/alternayte/fettle/internal/providers/host"
	"github.com/alternayte/fettle/internal/providers/kiln"
	"github.com/alternayte/fettle/internal/settings"
	"github.com/alternayte/fettle/internal/tools"
	"github.com/alternayte/fettle/internal/usage"
)

func init() {
	settings.Register(settings.Def{Key: "computers.max_running", Scope: "instance", Default: 0, Doc: "Computers that run at the same time. Zero uses a value from the memory of this host."})
}

// ComputerImage returns the computer image reference: FETTLE_COMPUTER_IMAGE, else the
// published image of this version.
func ComputerImage(version string) string {
	if v := os.Getenv("FETTLE_COMPUTER_IMAGE"); v != "" {
		return v
	}
	if version == "" || version == "dev" || strings.Contains(version, "-") {
		return "fettle-computer:dev"
	}
	return "ghcr.io/alternayte/fettle-computer:" + strings.TrimPrefix(version, "v")
}

// agentdBinary finds fettle-agentd for the host provider: FETTLE_AGENTD_BIN, else next to fettle.
func agentdBinary() string {
	if v := os.Getenv("FETTLE_AGENTD_BIN"); v != "" {
		return v
	}
	exe, err := os.Executable()
	if err != nil {
		return "fettle-agentd"
	}
	name := "fettle-agentd"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(filepath.Dir(exe), name)
}

// extraHosts reads FETTLE_COMPUTER_HOSTS ("docs.test=192.0.2.1,shop.test=192.0.2.1"), the
// test DNS entries inside computers (§24.4).
func extraHosts() map[string]string {
	out := map[string]string{}
	for _, kv := range strings.Split(os.Getenv("FETTLE_COMPUTER_HOSTS"), ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(kv), "=")
		if ok && k != "" && v != "" {
			out[k] = v
		}
	}
	return out
}

// defaultMaxRunning is max(1, floor((RAM - 4 GiB) / 2 GiB)) for the browser size (§10.2).
func defaultMaxRunning() int {
	ram := hostRAMBytes()
	if ram <= 0 {
		return 2
	}
	n := int((ram - 4<<30) / (2 << 30))
	return max(1, n)
}

// DefaultProvider picks the default computer provider (D-15): applecontainer on macOS 26
// on Apple silicon, local kiln on Linux with /dev/kvm, docker elsewhere. host is never a
// default.
func DefaultProvider(provs map[string]computers.Provider) string {
	for _, k := range []string{"applecontainer", "kiln", "docker"} {
		if _, ok := provs[k]; ok {
			return k
		}
	}
	return "docker"
}

func (a *App) setupComputers(ctx context.Context) {
	provs := map[string]computers.Provider{}
	image := ComputerImage(a.Cfg.Version)
	if d, err := docker.New(image); err == nil {
		provs["docker"] = d
	}
	provs["host"] = host.New(agentdBinary(), filepath.Join(a.Cfg.Data, "runtime", "host"))
	if applecontainer.Supported() == nil {
		provs["applecontainer"] = applecontainer.New(filepath.Join(a.Cfg.Data, "runtime", "applecontainer"), image)
	}
	def := os.Getenv("FETTLE_DEFAULT_PROVIDER")
	if def == "" {
		def = DefaultProvider(provs)
	}
	// A remote Kiln host (D-15: remote, never a default unless FETTLE_DEFAULT_PROVIDER names it).
	if u, tok := os.Getenv("FETTLE_KILN_URL"), os.Getenv("FETTLE_KILN_TOKEN"); u != "" && tok != "" {
		k := kiln.New(u, tok, image, a.Cfg.Version)
		k.HTTP = a.HTTP
		provs["kiln"] = k
		a.Vault.Redactor.RegisterGlobal("Kiln token", tok)
	}
	a.Computers = computers.NewManager(&computers.Manager{
		E: a.DB.Engine, Zones: a.Zones, Vault: a.Vault, Objects: a.Objects, Log: a.Log, Audit: a.Audit,
		Providers: provs, Default: def, Image: image, ExtraHosts: extraHosts(),
		MaxRunning: func(ctx context.Context) int {
			if n := a.Settings.Int(ctx, nil, "computers.max_running"); n > 0 {
				return n
			}
			if a.Cfg.Mode == ModeServer {
				return 0
			}
			return defaultMaxRunning()
		},
		Redact:     func(name, v string) { a.Vault.Redactor.RegisterGlobal(name, v) },
		HomeLimit:  a.homeLimit,
		OnOversize: a.warnOversize,
		OnSleep:    a.wakeComputerWaiters,
		OnActive:   a.recordComputerTime,
	})
	a.Zones.CheckProvider = a.Computers.CheckProvider
	a.Agent.Computer = func(ctx context.Context, spaceID, botID uuid.UUID) error {
		b, err := a.Bots.Get(ctx, spaceID, botID)
		if err != nil {
			return err
		}
		if b.ZoneID == nil {
			return tools.ErrNoZone
		}
		l, err := a.Computers.Acquire(ctx, *b.ZoneID)
		if errors.Is(err, computers.ErrNoCapacity) {
			return agent.ErrNoComputer
		}
		var pe *computers.PreparingError
		if errors.As(err, &pe) {
			return &agent.ComputerWait{Reason: "Preparing the computer (" + pe.Detail + ")"}
		}
		if err != nil {
			return err
		}
		l.Release()
		return nil
	}
	a.Agent.Checker = a.checker
	a.Agent.Forker = forker{a: a}
	a.Agent.DeliverAttachments = func(ctx context.Context, spaceID, botID uuid.UUID, atts []chats.Attachment) error {
		b, err := a.Bots.Get(ctx, spaceID, botID)
		if err != nil || b.ZoneID == nil {
			return err
		}
		l, err := a.Computers.Acquire(ctx, *b.ZoneID)
		if err != nil {
			return err
		}
		defer l.Release()
		if err := a.Computers.EnsureBotHome(ctx, l, botID); err != nil {
			return err
		}
		for _, at := range atts {
			data, err := a.AttachmentData(ctx, spaceID, at)
			if err != nil {
				return err
			}
			if err := l.Client.WriteFile(ctx, path.Join(computers.BotHome(botID), "attachments", at.ID, at.Name), data, 0o644); err != nil {
				return err
			}
		}
		return nil
	}
	a.Agent.Hire = a.hire
	a.Agent.CopyShared = func(ctx context.Context, fromZone, toZone uuid.UUID, from, to string) error {
		src, err := a.Computers.Acquire(ctx, fromZone)
		if err != nil {
			return err
		}
		data, err := src.Client.ReadFile(ctx, from)
		src.Release()
		if err != nil {
			return err
		}
		dst, err := a.Computers.Acquire(ctx, toZone)
		if err != nil {
			return err
		}
		defer dst.Release()
		return dst.Client.WriteFile(ctx, to, data, 0o644)
	}
	a.Agent.CanFork = func(ctx context.Context, spaceID, botID uuid.UUID) bool {
		b, err := a.Bots.Get(ctx, spaceID, botID)
		return err == nil && b.ZoneID != nil && a.Computers.CanFork(ctx, *b.ZoneID)
	}
	a.Agent.SubagentEnded = func(ctx context.Context, spaceID, botID, runID uuid.UUID) {
		b, err := a.Bots.Get(ctx, spaceID, botID)
		if err != nil || b.ZoneID == nil {
			return
		}
		l, err := a.Computers.Acquire(ctx, *b.ZoneID)
		if err != nil {
			return
		}
		defer l.Release()
		_ = l.Client.BrowserClose(ctx, "sub-"+runID.String())
	}
	c := &tools.Computer{Bots: a.Bots, Computers: a.Computers}
	c.Register(a.Agent.Tools)
	br := &tools.Browser{Computer: c, Agent: a.Agent, Models: a.Models}
	br.RegisterBrowser(a.Agent.Tools)
	(&tools.Broker{Browser: br, Vault: a.Vault, Zones: a.Zones, Now: a.Agent.Now}).RegisterBroker(a.Agent.Tools)
}

// wakeComputerWaiters wakes the oldest run that waits for a computer.
func (a *App) wakeComputerWaiters(ctx context.Context) {
	var id uuid.UUID
	if err := a.DB.Engine.QueryRow(ctx, "SELECT id FROM runs WHERE status = 'waiting' AND wait_reason = 'computer' ORDER BY updated_at LIMIT 1").Scan(&id); err != nil {
		return
	}
	_ = a.Runs.Wake(ctx, id, jobs.Interactive)
}

// MaintenanceArgs is the job that sleeps idle computers every minute.
type MaintenanceArgs struct{}

// Kind implements river.JobArgs.
func (MaintenanceArgs) Kind() string { return "maintenance_tick" }

type maintenanceWorker struct {
	river.WorkerDefaults[MaintenanceArgs]
	a *App
}

func (w *maintenanceWorker) Work(ctx context.Context, _ *river.Job[MaintenanceArgs]) error {
	ctx = context.WithValue(ctx, maintenanceKey{}, true)
	w.a.Computers.Meter(ctx)
	if _, err := w.a.Computers.SleepIdle(ctx); err != nil {
		w.a.Log.Warn("idle sleep failed", "error", err)
	}
	if _, err := w.a.Routines.Tick(ctx); err != nil {
		w.a.Log.Warn("routine tick failed", "error", err)
	}
	if _, err := w.a.Agent.ExpireCards(ctx); err != nil {
		w.a.Log.Warn("card expiry failed", "error", err)
	}
	w.a.SyncSkael(ctx)
	w.a.SendDigests(ctx, time.Now())
	w.a.RunPatchJobs(ctx, time.Now())
	w.a.extensionTick(ctx)
	w.a.cleanupSignIn(ctx)
	if err := w.a.WakeOpenWork(ctx); err != nil {
		w.a.Log.Warn("open work wake failed", "error", err)
	}
	return nil
}

// recordComputerTime writes the running seconds of a computer to usage, per zone and size
// (§8.4). Computer time has no cost of its own: the provider bills it outside Fettle.
func (a *App) recordComputerTime(ctx context.Context, c *computers.Computer, seconds int64) {
	z, err := a.Zones.Find(ctx, c.ZoneID)
	if err != nil {
		return
	}
	zoneID := z.ID()
	if err := a.Usage.Record(ctx, &usage.UsageEvent{SpaceID: z.SpaceID, Kind: "computer", ZoneID: &zoneID, Quantity: seconds, Unit: "seconds", Size: z.Size}); err != nil {
		a.Log.Warn("computer usage", "error", err)
	}
}

// cleanupSignIn removes expired sessions, tokens and passkey ceremonies of server mode;
// auth-all runs no background work.
func (a *App) cleanupSignIn(ctx context.Context) {
	if a.SignIn == nil {
		return
	}
	if err := a.SignIn.Auth.Cleanup(ctx); err != nil {
		a.Log.Warn("sign-in cleanup", "error", err)
	}
	if a.SignIn.Passkeys == nil {
		return
	}
	if _, err := a.SignIn.Passkeys.Cleanup(ctx); err != nil {
		a.Log.Warn("passkey cleanup", "error", err)
	}
}

func maintenanceJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{river.NewPeriodicJob(river.PeriodicInterval(time.Minute), func() (river.JobArgs, *river.InsertOpts) {
		return MaintenanceArgs{}, &river.InsertOpts{Queue: jobs.Maintenance}
	}, &river.PeriodicJobOpts{RunOnStart: true})}
}

func init() {
	settings.Register(settings.Def{Key: "workspaces.home_limit_gib", Scope: "space", Default: 5, Doc: "Size limit of one bot home in GiB. Over it, checkpoints pause."})
}

// homeLimit returns the bot home limit in bytes (§10.3).
func (a *App) homeLimit(ctx context.Context, spaceID uuid.UUID) int64 {
	gib := a.Settings.Float(ctx, &spaceID, "workspaces.home_limit_gib")
	if gib <= 0 {
		return 0
	}
	return int64(gib * float64(1<<30))
}

// warnOversize posts one warning card in the bot's direct chat while its home is over the
// limit; the computer keeps running and its checkpoints pause (§10.3).
func (a *App) warnOversize(ctx context.Context, spaceID, botID uuid.UUID, limit int64) {
	chat, err := a.Chats.DirectFor(ctx, spaceID, botID)
	if err != nil {
		return
	}
	var n int
	_ = a.DB.Engine.QueryRow(ctx, `SELECT COUNT(*) FROM messages WHERE chat_id = $1 AND card LIKE $2 AND card LIKE $3`,
		chat.ID(), `%"kind":"warning"%`, `%"status":"pending"%`).Scan(&n)
	if n > 0 {
		return
	}
	card := chats.Card{ID: uuid.NewString(), Kind: "warning", Status: "pending", Title: "My files are too large to save",
		Detail: fmt.Sprintf("My home is over %.0f GiB, so Fettle stopped saving it. My computer keeps running. Delete large files, or raise the limit in Settings.", float64(limit)/float64(1<<30))}
	msg := &chats.Message{SpaceID: spaceID, ChatID: chat.ID(), AuthorKind: "bot", AuthorID: botID.String(), Card: &card}
	if err := a.Chats.Insert(ctx, msg, nil); err != nil {
		a.Log.Warn("oversize card", "error", err)
		return
	}
	a.Chats.Publish(chat.ID(), "message", a.Agent.MessageView(ctx, msg))
	a.Agent.Notify(ctx, spaceID, "question", card.Title, card.Detail, fmt.Sprintf("/chat/%s?card=%s", chat.ID(), card.ID))
}
