package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/alternayte/fettle/internal/agent"
	"github.com/alternayte/fettle/internal/computers"
	"github.com/alternayte/fettle/internal/jobs"
	"github.com/alternayte/fettle/internal/providers/docker"
	"github.com/alternayte/fettle/internal/providers/host"
	"github.com/alternayte/fettle/internal/settings"
	"github.com/alternayte/fettle/internal/tools"
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

func (a *App) setupComputers(ctx context.Context) {
	provs := map[string]computers.Provider{}
	image := ComputerImage(a.Cfg.Version)
	if d, err := docker.New(image); err == nil {
		provs["docker"] = d
	}
	provs["host"] = host.New(agentdBinary(), filepath.Join(a.Cfg.Data, "runtime", "host"))
	def := os.Getenv("FETTLE_DEFAULT_PROVIDER")
	if def == "" {
		def = "docker"
	}
	a.Computers = computers.NewManager(&computers.Manager{
		E: a.DB.Engine, Zones: a.Zones, Vault: a.Vault, Objects: a.Objects, Log: a.Log,
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
		Redact:  func(name, v string) { a.Vault.Redactor.RegisterGlobal(name, v) },
		OnSleep: a.wakeComputerWaiters,
	})
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
		if err != nil {
			return err
		}
		l.Release()
		return nil
	}
	(&tools.Computer{Bots: a.Bots, Computers: a.Computers}).Register(a.Agent.Tools)
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
	if _, err := w.a.Computers.SleepIdle(ctx); err != nil {
		w.a.Log.Warn("idle sleep failed", "error", err)
	}
	return nil
}

func maintenanceJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{river.NewPeriodicJob(river.PeriodicInterval(time.Minute), func() (river.JobArgs, *river.InsertOpts) {
		return MaintenanceArgs{}, &river.InsertOpts{Queue: jobs.Maintenance}
	}, &river.PeriodicJobOpts{RunOnStart: true})}
}
