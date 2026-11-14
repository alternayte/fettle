package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alternayte/fettle/internal/agent"
	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/auth"
	"github.com/alternayte/fettle/internal/bots"
	"github.com/alternayte/fettle/internal/chats"
	"github.com/alternayte/fettle/internal/envelopes"
	"github.com/alternayte/fettle/internal/jobs"
	"github.com/alternayte/fettle/internal/models"
	"github.com/alternayte/fettle/internal/objects"
	"github.com/alternayte/fettle/internal/runs"
	"github.com/alternayte/fettle/internal/settings"
	"github.com/alternayte/fettle/internal/spaces"
	"github.com/alternayte/fettle/internal/usage"
	"github.com/alternayte/fettle/internal/vault"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

// App is one running Fettle instance: storage, services, jobs and the HTTP server.
type App struct {
	Cfg      *Config
	DB       *Database
	Objects  objects.Store
	HTTP     *http.Client
	Log      *slog.Logger
	Metrics  *Metrics
	Hub      *chats.Hub
	Audit    *audit.Log
	Vault    *vault.Vault
	Settings *settings.Store
	Users    *auth.UserStore
	Spaces   *spaces.Service
	Bots     *bots.Service
	Chats    *chats.Service
	Envs     *envelopes.Service
	Runs     *runs.Service
	Journal  *runs.Journal
	Models   *models.Service
	Usage    *usage.Service
	Agent    *agent.Agent
	Queue    jobs.Queue

	MasterKeySource vault.MasterKeySource
	LocalToken      string

	server   *http.Server
	listener net.Listener
	mu       sync.Mutex
	stopped  bool
	// Telemetry reports the counts it would send (REQ-CORE-012).
	Telemetry *Telemetry
}

// New opens storage, applies migrations and builds every service. It does not start jobs or HTTP.
func New(ctx context.Context, cfg *Config) (*App, error) {
	if err := cfg.Normalize(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.Data, 0o700); err != nil {
		return nil, fmt.Errorf("create data folder: %w", err)
	}
	a := &App{Cfg: cfg}
	db, err := OpenDatabase(ctx, cfg)
	if err != nil {
		return nil, err
	}
	a.DB = db
	if _, err := db.Migrate(ctx, cfg); err != nil {
		db.Close()
		return nil, err
	}
	master, src, err := vault.LoadMasterKey(cfg.MasterKey, cfg.Data, cfg.Mode == ModeServer)
	if err != nil {
		db.Close()
		return nil, err
	}
	a.MasterKeySource = src
	if a.Objects, err = OpenObjects(ctx, cfg); err != nil {
		db.Close()
		return nil, err
	}
	if a.HTTP, err = NewHTTPClient(cfg.ExtraCAFile); err != nil {
		db.Close()
		return nil, err
	}
	e := db.Engine
	a.Metrics = NewMetrics(e)
	a.Hub = chats.NewHub()
	a.Audit = audit.New(e)
	a.Vault = vault.New(e, a.Audit, master)
	a.Log = NewLogger(cfg.LogLevel, os.Stderr, a.Vault.Redactor)
	slog.SetDefault(a.Log)
	a.Settings = settings.New(e, a.Audit)
	a.Users = auth.NewUserStore(e)
	a.Spaces = spaces.New(e, a.Audit)
	a.Bots = bots.New(e, a.Audit)
	a.Bots.MaxPerSpace = func(ctx context.Context, sp uuid.UUID) int { return a.Settings.Int(ctx, &sp, "bots.max_per_space") }
	a.Chats = chats.New(e, a.Hub)
	a.Envs = envelopes.New(e)
	a.Runs = runs.NewService(e)
	a.Journal = runs.NewJournal(e, a.Objects, a.Vault)
	a.Journal.Redact = a.Vault.Redactor.String
	a.Models = models.NewService(e, a.Vault, a.Audit, a.HTTP)
	a.Usage = usage.New(e)
	a.Agent = agent.New(agent.Agent{E: e, Audit: a.Audit, Bots: a.Bots, Chats: a.Chats, Envelopes: a.Envs, Runs: a.Runs, Journal: a.Journal, Models: a.Models, Usage: a.Usage, Settings: a.Settings, Spaces: a.Spaces, Vault: a.Vault, HTTP: a.HTTP, Log: a.Log, Metrics: a.Metrics})
	a.Telemetry = NewTelemetry(a)

	workers := river.NewWorkers()
	river.AddWorker(workers, &agent.Worker{A: a.Agent})
	interactive, background := 4, 2
	if cfg.Mode == ModeServer {
		interactive, background = 16, 8
	}
	if cfg.Workers > 0 {
		interactive = cfg.Workers
	}
	jc := jobs.Config{Interactive: interactive, Background: background, Maintenance: 1, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), FetchPoll: 100 * time.Millisecond}
	if cfg.RescueAfterSeconds > 0 {
		jc.RescueAfter = time.Duration(cfg.RescueAfterSeconds) * time.Second
	}
	if db.Postgres {
		a.Queue, err = jobs.NewPostgres(db.PGPool, jc, workers)
	} else {
		a.Queue, err = jobs.NewSQLite(db.RiverSQL, jc, workers)
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	a.Runs.Queue = a.Queue

	if cfg.Mode == ModeLocal {
		if err := a.bootstrapLocal(ctx); err != nil {
			db.Close()
			return nil, err
		}
		if a.LocalToken, err = ensureLocalToken(cfg.Data); err != nil {
			db.Close()
			return nil, err
		}
	}
	return a, nil
}

// OpenObjects opens the object store of cfg.
func OpenObjects(ctx context.Context, cfg *Config) (objects.Store, error) {
	if cfg.ObjectStore == "s3" {
		return objects.NewS3(ctx, objects.S3Config{Endpoint: cfg.S3.Endpoint, Region: cfg.S3.Region, Bucket: cfg.S3.Bucket, AccessKey: cfg.S3.AccessKey, SecretKey: cfg.S3.SecretKey, PathStyle: cfg.S3.PathStyle})
	}
	return objects.NewDisk(filepath.Join(cfg.Data, "objects"))
}

// ensureLocalToken reads DATA/local.token or creates it with mode 0600 (§4.2).
func ensureLocalToken(dir string) (string, error) {
	p := filepath.Join(dir, "local.token")
	if b, err := os.ReadFile(p); err == nil && len(strings.TrimSpace(string(b))) >= 32 {
		_ = os.Chmod(p, 0o600)
		return strings.TrimSpace(string(b)), nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(buf)
	if err := os.WriteFile(p, []byte(tok+"\n"), 0o600); err != nil {
		return "", err
	}
	return tok, os.Chmod(p, 0o600)
}

// LocalOwner returns the owner user of local mode.
func (a *App) LocalOwner(ctx context.Context) (*auth.User, error) {
	u, _, err := a.Users.EnsureLocalOwner(ctx, "")
	return u, err
}

// bootstrapLocal creates the owner, the first space and its primary bot on first start.
func (a *App) bootstrapLocal(ctx context.Context) error {
	u, _, err := a.Users.EnsureLocalOwner(ctx, "")
	if err != nil {
		return err
	}
	ms, err := a.Spaces.ForUser(ctx, u.ID())
	if err != nil {
		return err
	}
	if len(ms) > 0 {
		return nil
	}
	_, err = a.CreateSpace(ctx, audit.System, "My space", "", u.ID())
	return err
}

// CreateSpace adds a space with its primary bot and that bot's direct chat.
func (a *App) CreateSpace(ctx context.Context, actor audit.Actor, name, tz string, owner uuid.UUID) (*spaces.Space, error) {
	var sp *spaces.Space
	err := a.DB.Engine.WithTx(ctx, func(ctx context.Context) error {
		var err error
		sp, err = a.Spaces.Create(ctx, actor, name, tz, owner)
		if err != nil {
			return err
		}
		chief, err := a.Bots.Create(ctx, actor, sp.ID(), bots.Input{Name: "Chief", Job: "Chief of Staff: routes work, answers questions and reports back", Instructions: ChiefInstructions, IsPrimary: true})
		if err != nil {
			return err
		}
		_, err = a.Chats.CreateDirect(ctx, sp.ID(), chief.ID(), chief.Name)
		return err
	})
	return sp, err
}

// ChiefInstructions are the default instructions of the primary bot.
const ChiefInstructions = `You are the Chief of Staff of this space.
- Answer questions directly when you can.
- Ask one short question when the request is not clear.
- Save stable facts about the user in memory.
- Report results in a few lines. Put details in lists or tables.`

// Start starts the job workers and the HTTP server. It returns when the listener is ready.
func (a *App) Start(ctx context.Context, handler http.Handler) error {
	if err := a.Queue.Start(context.WithoutCancel(ctx)); err != nil {
		return fmt.Errorf("start jobs: %w", err)
	}
	ln, err := net.Listen("tcp", a.Cfg.Addr)
	if err != nil {
		return err
	}
	if a.Cfg.Mode == ModeLocal {
		if tcp, ok := ln.Addr().(*net.TCPAddr); !ok || !tcp.IP.IsLoopback() {
			_ = ln.Close()
			return errors.New("local mode must listen on a loopback address")
		}
	}
	a.listener = ln
	a.server = &http.Server{Handler: handler, ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 120 * time.Second}
	go func() {
		if err := a.server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			a.Log.Error("http server stopped", "error", err)
		}
	}()
	a.Telemetry.Start(ctx)
	return nil
}

// Addr returns the listen address after Start.
func (a *App) Addr() string {
	if a.listener == nil {
		return a.Cfg.Addr
	}
	return a.listener.Addr().String()
}

// URL returns the base URL of the running server.
func (a *App) URL() string {
	if a.Cfg.BaseURL != "" {
		return strings.TrimRight(a.Cfg.BaseURL, "/")
	}
	return "http://" + a.Addr()
}

// Shutdown stops job fetching, lets running steps reach a safe point and exits within the
// context deadline (REQ-CORE-008).
func (a *App) Shutdown(ctx context.Context) error {
	a.mu.Lock()
	if a.stopped {
		a.mu.Unlock()
		return nil
	}
	a.stopped = true
	a.mu.Unlock()
	var errs []error
	if a.server != nil {
		sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if err := a.server.Shutdown(sctx); err != nil {
			errs = append(errs, err)
		}
		cancel()
	}
	if a.Queue != nil {
		if err := a.Queue.Stop(ctx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
			errs = append(errs, err)
		}
	}
	a.Telemetry.Stop()
	a.DB.Close()
	return errors.Join(errs...)
}

// StoreMasterKey saves a new master key after a rekey.
func (a *App) StoreMasterKey(k []byte) error {
	return vault.StoreMasterKey(a.MasterKeySource, a.Cfg.Data, k)
}
