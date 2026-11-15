package computers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/alternayte/drel"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/agentd"
	"github.com/alternayte/fettle/agentd/client"
	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/objects"
	"github.com/alternayte/fettle/internal/vault"
	"github.com/alternayte/fettle/internal/zones"
)

// Computer states (§10.2).
const (
	StateNone        = "none"
	StateStarting    = "starting"
	StateRunning     = "running"
	StateSleeping    = "sleeping"
	StateStopping    = "stopping"
	StateError       = "error"
	StateQuarantined = "quarantined"
)

// ErrNoCapacity means the admission limit is reached; the run waits (§10.2).
var ErrNoCapacity = errors.New("waiting for a free computer")

// Manager owns the computers of all zones.
type Manager struct {
	E       *drel.Engine
	Zones   *zones.Service
	Vault   *vault.Vault
	Objects objects.Store
	Log     *slog.Logger
	Audit   *audit.Log
	// Providers by kind; Default names the provider of zones without one.
	Providers map[string]Provider
	Default   string
	Image     string
	// MaxRunning is the admission limit; 0 means no limit.
	MaxRunning func(ctx context.Context) int
	// ExtraHosts are test DNS entries inside computers.
	ExtraHosts map[string]string
	// OnSleep is called after a computer sleeps, so runs that wait for a computer can start.
	OnSleep func(ctx context.Context)
	// HomeLimit is the size limit of a bot home in bytes (0: no limit).
	HomeLimit func(ctx context.Context, spaceID uuid.UUID) int64
	// OnOversize is called when a bot home is over its limit; the bot posts a warning card.
	OnOversize func(ctx context.Context, spaceID, botID uuid.UUID, limit int64)
	// Redact registers a computer token for redaction (SI-01).
	Redact func(name, value string)
	now    func() time.Time

	mu       sync.Mutex
	live     map[uuid.UUID]*live // by computer ID; tokens live only in memory (SI-02)
	creating map[uuid.UUID]chan struct{}
	forks    map[uuid.UUID]*Fork // by sub-agent run ID
	preps    *preparer
}

type live struct {
	token    string
	client   *client.Client
	machine  Machine
	leases   int
	viewers  int
	imported map[string]bool // workspace paths imported into this machine
}

// NewManager prepares a manager that the caller filled.
func NewManager(mm *Manager) *Manager {
	mm.live = map[uuid.UUID]*live{}
	mm.creating = map[uuid.UUID]chan struct{}{}
	if mm.now == nil {
		mm.now = func() time.Time { return time.Now().UTC() }
	}
	if mm.Log == nil {
		mm.Log = slog.Default()
	}
	return mm
}

// SetClock replaces the clock (tests).
func (m *Manager) SetClock(now func() time.Time) { m.now = now }

// Lease is the use of a zone's computer by one tool call or run step.
type Lease struct {
	m        *Manager
	Computer uuid.UUID
	ZoneID   uuid.UUID
	SpaceID  uuid.UUID
	// Screens reports that the zone has the desktop size, so bots have screens (§10.8).
	Screens  bool
	Client   *client.Client
	released bool
}

// Release ends the lease.
func (l *Lease) Release() {
	if l.released || l.m == nil {
		return
	}
	l.released = true
	l.m.mu.Lock()
	if lv := l.m.live[l.Computer]; lv != nil && lv.leases > 0 {
		lv.leases--
	}
	l.m.mu.Unlock()
	_ = l.m.touch(context.Background(), l.Computer)
}

func (m *Manager) touch(ctx context.Context, id uuid.UUID) error {
	_, err := m.E.Exec(ctx, "UPDATE computers SET last_active_at = $1 WHERE id = $2", m.now(), id)
	return err
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

func newToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// current returns the computer row of a zone, if any.
func (m *Manager) current(ctx context.Context, zoneID uuid.UUID) (*Computer, error) {
	return drel.NewRepository(m.E, ComputerMeta).Where(Computers.ZoneID.Eq(zoneID)).OrderBy(Computers.CreatedAt.Desc(), Computers.ID.Desc()).FirstOrNil(ctx)
}

// Current returns the computer of a zone, or nil.
func (m *Manager) Current(ctx context.Context, zoneID uuid.UUID) (*Computer, error) {
	return m.current(ctx, zoneID)
}

func (m *Manager) providerFor(ctx context.Context, z *zones.Zone) (Provider, *ProviderRow, error) {
	if z.ProviderID != nil {
		row, err := drel.NewRepository(m.E, ProviderRowMeta).Where(ProviderRows.ID.Eq(*z.ProviderID)).FirstOrNil(ctx)
		if err != nil {
			return nil, nil, err
		}
		if row == nil {
			return nil, nil, fmt.Errorf("the provider of zone %s does not exist", z.Name)
		}
		p, ok := m.Providers[row.Kind]
		if !ok {
			return nil, nil, fmt.Errorf("the %s provider is not available on this server", row.Kind)
		}
		if !row.Enabled {
			return nil, row, fmt.Errorf("the %s provider is off; turn it on in Settings, Computers", row.Kind)
		}
		return p, row, nil
	}
	p, ok := m.Providers[m.Default]
	if !ok {
		return nil, nil, errors.New("no computer provider is set up; set one up in Settings, Computers")
	}
	row, err := m.ensureProviderRow(ctx, m.Default)
	if err == nil && !row.Enabled {
		return nil, row, fmt.Errorf("the %s provider is off; turn it on in Settings, Computers", row.Kind)
	}
	return p, row, err
}

func (m *Manager) ensureProviderRow(ctx context.Context, kind string) (*ProviderRow, error) {
	row, err := drel.NewRepository(m.E, ProviderRowMeta).Where(ProviderRows.Kind.Eq(kind)).Where(ProviderRows.SpaceID.IsNull()).FirstOrNil(ctx)
	if err != nil || row != nil {
		return row, err
	}
	p := m.Providers[kind]
	// host runs bots without isolation, so it starts off; a hard-ask card turns it on.
	row = &ProviderRow{Kind: kind, Name: kind, Config: map[string]string{}, Caps: p.Caps(), Enabled: kind != "host"}
	err = m.E.WithTx(ctx, func(ctx context.Context) error {
		drel.Repo(drel.MustFromContext(ctx), ProviderRowMeta).Add(row)
		return nil
	})
	return row, err
}

func (m *Manager) running(ctx context.Context) (int, error) {
	var n int
	err := m.E.QueryRow(ctx, "SELECT COUNT(*) FROM computers WHERE state IN ('running','starting')").Scan(&n)
	return n, err
}

// Acquire returns a lease on the computer of a zone. It starts, resumes or replaces the
// machine when needed. ErrNoCapacity means the run must wait.
func (m *Manager) Acquire(ctx context.Context, zoneID uuid.UUID) (*Lease, error) {
	for {
		m.mu.Lock()
		wait, busy := m.creating[zoneID]
		if !busy {
			m.creating[zoneID] = make(chan struct{})
		}
		m.mu.Unlock()
		if !busy {
			break
		}
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	defer func() {
		m.mu.Lock()
		close(m.creating[zoneID])
		delete(m.creating, zoneID)
		m.mu.Unlock()
	}()
	z, err := m.Zones.Find(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	c, err := m.current(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	if c != nil && c.State == StateRunning {
		m.mu.Lock()
		lv := m.live[c.ID()]
		m.mu.Unlock()
		if lv != nil {
			if _, err := lv.client.Health(ctx); err == nil {
				return m.lease(c, z, lv), nil
			}
		}
	}
	if c == nil || c.State != StateRunning {
		if max := m.maxRunning(ctx); max > 0 {
			n, err := m.running(ctx)
			if err != nil {
				return nil, err
			}
			if n >= max {
				return nil, ErrNoCapacity
			}
		}
	}
	lv, c, err := m.bringUp(ctx, z, c)
	if err != nil {
		return nil, err
	}
	return m.lease(c, z, lv), nil
}

func (m *Manager) maxRunning(ctx context.Context) int {
	if m.MaxRunning == nil {
		return 0
	}
	return m.MaxRunning(ctx)
}

func (m *Manager) lease(c *Computer, z *zones.Zone, lv *live) *Lease {
	m.mu.Lock()
	lv.leases++
	m.mu.Unlock()
	_ = m.touch(context.Background(), c.ID())
	return &Lease{m: m, Computer: c.ID(), ZoneID: z.ID(), SpaceID: z.SpaceID, Screens: z.Size == "desktop", Client: lv.client}
}

// bringUp resumes the known machine when this process holds its token, else creates a new
// machine with a new token and certificate (SI-02 rotation).
func (m *Manager) bringUp(ctx context.Context, z *zones.Zone, c *Computer) (*live, *Computer, error) {
	prov, row, err := m.providerFor(ctx, z)
	if err != nil {
		return nil, nil, err
	}
	if c != nil {
		m.mu.Lock()
		lv := m.live[c.ID()]
		m.mu.Unlock()
		if lv != nil && c.ProviderRef != "" {
			if err := m.setState(ctx, c.ID(), StateStarting, ""); err != nil {
				return nil, nil, err
			}
			if err := lv.machine.Resume(ctx); err == nil {
				if err := m.waitHealthy(ctx, lv, c, lv.machine); err == nil {
					return lv, c, m.setState(ctx, c.ID(), StateRunning, "")
				}
			}
		}
		// The machine is gone or its token is unknown: replace it.
		if c.ProviderRef != "" {
			if mach, err := prov.Attach(ctx, c.ProviderRef); err == nil {
				_ = mach.Destroy(ctx)
			}
		}
		m.mu.Lock()
		delete(m.live, c.ID())
		m.mu.Unlock()
		if err := m.setState(ctx, c.ID(), StateNone, "replaced"); err != nil {
			return nil, nil, err
		}
	}
	if err := m.ensurePrepared(row.Kind); err != nil {
		return nil, nil, err
	}
	token := newToken()
	certPEM, keyPEM, err := agentd.GenerateCert([]string{"fettle-computer"})
	if err != nil {
		return nil, nil, err
	}
	netCfg, _ := json.Marshal(map[string]any{"mode": z.Network, "allow_hosts": z.AllowHosts})
	nc := &Computer{ZoneID: z.ID(), ProviderID: row.ID(), State: StateStarting, TokenHash: hashToken(token), CertPEM: string(certPEM), ImageDigest: m.Image}
	if c != nil {
		nc.WorkspaceRev = c.WorkspaceRev
	}
	if err := m.E.WithTx(ctx, func(ctx context.Context) error {
		drel.Repo(drel.MustFromContext(ctx), ComputerMeta).Add(nc)
		return nil
	}); err != nil {
		return nil, nil, err
	}
	if m.Redact != nil {
		m.Redact("computer token", token)
	}
	spec := Spec{ZoneID: z.ID().String(), Image: m.Image, Size: Size(z.Size), DiskGiB: Sizes[Size(z.Size)].DiskGiB, ExtraHosts: m.ExtraHosts, Env: map[string]string{
		"FETTLE_AGENTD_TOKEN":    token,
		"FETTLE_AGENTD_TLS_CERT": string(certPEM),
		"FETTLE_AGENTD_TLS_KEY":  string(keyPEM),
		"FETTLE_AGENTD_NETWORK":  string(netCfg),
	}}
	mach, err := prov.Create(ctx, spec)
	if err != nil {
		_ = m.setState(context.WithoutCancel(ctx), nc.ID(), StateError, err.Error())
		return nil, nil, fmt.Errorf("start the computer of zone %s: %w", z.Name, err)
	}
	if _, err := m.E.Exec(ctx, "UPDATE computers SET provider_ref = $1 WHERE id = $2", mach.Ref(), nc.ID()); err != nil {
		return nil, nil, err
	}
	nc.ProviderRef = mach.Ref()
	lv := &live{token: token, machine: mach, imported: map[string]bool{}}
	if err := m.waitHealthy(ctx, lv, nc, mach); err != nil {
		_ = m.setState(context.WithoutCancel(ctx), nc.ID(), StateError, err.Error())
		return nil, nil, err
	}
	m.mu.Lock()
	m.live[nc.ID()] = lv
	m.mu.Unlock()
	if err := m.importShared(ctx, z, lv); err != nil {
		return nil, nil, err
	}
	return lv, nc, m.setState(ctx, nc.ID(), StateRunning, "")
}

func (m *Manager) setState(ctx context.Context, id uuid.UUID, state, errText string) error {
	_, err := m.E.Exec(ctx, "UPDATE computers SET state = $1, error = $2, updated_at = $3 WHERE id = $4", state, errText, m.now(), id)
	return err
}

// waitHealthy connects to agentd with the pinned certificate and waits for its health check.
func (m *Manager) waitHealthy(ctx context.Context, lv *live, c *Computer, mach Machine) error {
	deadline := time.Now().Add(90 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		url, err := mach.AgentdURL(ctx)
		if err == nil {
			cl, err := client.New(url, lv.token, []byte(c.CertPEM))
			if err != nil {
				return err
			}
			hctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			_, err = cl.Health(hctx)
			cancel()
			if err == nil {
				lv.client = cl
				_, _ = m.E.Exec(ctx, "UPDATE computers SET agentd_url = $1 WHERE id = $2", url, c.ID())
				return nil
			}
			last = err
		} else {
			last = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return fmt.Errorf("agentd did not answer within 90 s: %w", last)
}

// VerifyToken checks a token against the stored hash of a computer (SI-02).
func (m *Manager) VerifyToken(ctx context.Context, computerID uuid.UUID, token string) bool {
	var h string
	if err := m.E.QueryRow(ctx, "SELECT token_hash FROM computers WHERE id = $1", computerID).Scan(&h); err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(h), []byte(hashToken(token))) == 1
}

// BotHome is the folder of a bot inside the computer.
func BotHome(botID uuid.UUID) string { return "bots/" + botID.String() }

// SharedPath is the zone's shared folder inside the computer.
const SharedPath = "shared"

// EnsureBotHome imports the latest workspace of a bot into the leased machine once.
func (m *Manager) EnsureBotHome(ctx context.Context, l *Lease, botID uuid.UUID) error {
	path := BotHome(botID)
	m.mu.Lock()
	lv := m.live[l.Computer]
	done := lv != nil && lv.imported[path]
	m.mu.Unlock()
	if lv == nil || done {
		return nil
	}
	ws, err := m.workspace(ctx, botID.String(), "bot_home")
	if err != nil {
		return err
	}
	if ws != nil && ws.ObjectKey != "" {
		if err := m.importArchive(ctx, l.SpaceID, lv, ws, path); err != nil {
			return err
		}
	} else if _, err := lv.client.Stat(ctx, path); errors.Is(err, client.ErrNotFound) {
		if err := lv.client.WriteFile(ctx, path+"/.fettle/README", []byte("This folder is the home of one bot.\n"), 0o644); err != nil {
			return err
		}
	}
	m.mu.Lock()
	lv.imported[path] = true
	m.mu.Unlock()
	return nil
}

func (m *Manager) importShared(ctx context.Context, z *zones.Zone, lv *live) error {
	ws, err := m.workspace(ctx, z.ID().String(), "zone_shared")
	if err != nil {
		return err
	}
	if ws != nil && ws.ObjectKey != "" {
		if err := m.importArchive(ctx, z.SpaceID, lv, ws, SharedPath); err != nil {
			return err
		}
	} else if _, err := lv.client.Stat(ctx, SharedPath); errors.Is(err, client.ErrNotFound) {
		if err := lv.client.WriteFile(ctx, SharedPath+"/README", []byte("Files in this folder are shared by the bots of one zone.\n"), 0o644); err != nil {
			return err
		}
	}
	m.mu.Lock()
	lv.imported[SharedPath] = true
	m.mu.Unlock()
	return nil
}

func (m *Manager) workspace(ctx context.Context, owner, kind string) (*Workspace, error) {
	return drel.NewRepository(m.E, WorkspaceMeta).Where(Workspaces.OwnerKey.Eq(owner)).Where(Workspaces.Kind.Eq(kind)).FirstOrNil(ctx)
}

func workspaceAAD(ws *Workspace) string {
	return fmt.Sprintf("fettle:workspace:%s:%d", ws.ID(), ws.Rev)
}

func (m *Manager) importArchive(ctx context.Context, spaceID uuid.UUID, lv *live, ws *Workspace, path string) error {
	rc, err := m.Objects.Get(ctx, ws.ObjectKey)
	if err != nil {
		return fmt.Errorf("read workspace %s: %w", ws.ObjectKey, err)
	}
	defer func() { _ = rc.Close() }()
	plain, err := m.Vault.OpenStream(ctx, spaceID, rc, workspaceAAD(ws))
	if err != nil {
		return err
	}
	return lv.client.Import(ctx, path, plain)
}

// Checkpoint exports the bot homes and the shared folder of a zone's computer into the
// object store as new encrypted revisions (§10.3).
func (m *Manager) Checkpoint(ctx context.Context, computerID uuid.UUID) error {
	m.mu.Lock()
	lv := m.live[computerID]
	var paths []string
	if lv != nil {
		for p := range lv.imported {
			paths = append(paths, p)
		}
	}
	m.mu.Unlock()
	if lv == nil {
		return nil
	}
	c, err := drel.NewRepository(m.E, ComputerMeta).Where(Computers.ID.Eq(computerID)).First(ctx)
	if err != nil {
		return err
	}
	z, err := m.Zones.Find(ctx, c.ZoneID)
	if err != nil {
		return err
	}
	var oversize error
	for _, p := range paths {
		owner, kind, botID := z.ID().String(), "zone_shared", (*uuid.UUID)(nil)
		zoneID := z.ID()
		if p != SharedPath {
			id, err := uuid.Parse(p[len("bots/"):])
			if err != nil {
				continue
			}
			owner, kind, botID = id.String(), "bot_home", &id
		}
		if err := m.checkpointPath(ctx, z, lv, p, owner, kind, botID, &zoneID); err != nil {
			if errors.Is(err, ErrOversize) {
				oversize = err // the other homes still get their checkpoint
				continue
			}
			return err
		}
	}
	if _, err = m.E.Exec(ctx, "UPDATE computers SET workspace_rev = workspace_rev + 1 WHERE id = $1", computerID); err != nil {
		return err
	}
	return oversize
}

// ErrOversize means a bot home is over its size limit: its checkpoints pause and the
// computer keeps running (§10.3).
var ErrOversize = errors.New("a bot home is over its size limit")

func (m *Manager) checkpointPath(ctx context.Context, z *zones.Zone, lv *live, path, owner, kind string, botID, zoneID *uuid.UUID) error {
	ws, err := m.workspace(ctx, owner, kind)
	if err != nil {
		return err
	}
	if ws == nil {
		ws = &Workspace{OwnerKey: owner, Kind: kind, BotID: botID, SpaceID: z.SpaceID}
		if kind == "zone_shared" {
			ws.ZoneID = zoneID
		}
		if err := m.E.WithTx(ctx, func(ctx context.Context) error {
			drel.Repo(drel.MustFromContext(ctx), WorkspaceMeta).Add(ws)
			return nil
		}); err != nil {
			return err
		}
	}
	var limit int64
	if kind == "bot_home" && m.HomeLimit != nil {
		limit = m.HomeLimit(ctx, z.SpaceID)
	}
	rc, err := lv.client.ExportLimited(ctx, path, nil, limit)
	if errors.Is(err, client.ErrTooLarge) {
		if m.OnOversize != nil && botID != nil {
			m.OnOversize(ctx, z.SpaceID, *botID, limit)
		}
		return fmt.Errorf("%w: %s", ErrOversize, path)
	}
	if err != nil {
		return fmt.Errorf("export %s: %w", path, err)
	}
	defer func() { _ = rc.Close() }()
	next := &Workspace{OwnerKey: owner, Kind: kind, Rev: ws.Rev + 1}
	next.SetID(ws.ID())
	key := fmt.Sprintf("workspaces/%s/%d", ws.ID(), next.Rev)
	pr, pw := io.Pipe()
	hasher := sha256.New()
	counter := &countWriter{}
	go func() {
		sw, err := m.Vault.SealStream(ctx, z.SpaceID, pw, workspaceAAD(next))
		if err != nil {
			pw.CloseWithError(err)
			return
		}
		_, err = io.Copy(io.MultiWriter(sw, hasher, counter), rc)
		if err == nil {
			err = sw.Close()
		}
		pw.CloseWithError(err)
	}()
	if err := m.Objects.Put(ctx, key, pr, -1); err != nil {
		return fmt.Errorf("store workspace: %w", err)
	}
	old := ws.ObjectKey
	if _, err := m.E.Exec(ctx, "UPDATE workspaces SET rev = $1, object_key = $2, size = $3, sha256 = $4, updated_at = $5 WHERE id = $6",
		next.Rev, key, counter.n, hex.EncodeToString(hasher.Sum(nil)), m.now(), ws.ID()); err != nil {
		return err
	}
	if old != "" && old != key {
		_ = m.Objects.Delete(ctx, old)
	}
	return nil
}

type countWriter struct{ n int64 }

func (c *countWriter) Write(p []byte) (int, error) { c.n += int64(len(p)); return len(p), nil }

// SleepIdle checkpoints and sleeps running computers with no lease and no viewer after
// their zone's idle minutes (§10.2). It returns the number of computers it put to sleep.
func (m *Manager) SleepIdle(ctx context.Context) (int, error) {
	rows, err := drel.NewRepository(m.E, ComputerMeta).Where(Computers.State.Eq(StateRunning)).All(ctx)
	if err != nil {
		return 0, err
	}
	slept := 0
	for _, c := range rows {
		m.mu.Lock()
		lv := m.live[c.ID()]
		busy := lv != nil && (lv.leases > 0 || lv.viewers > 0)
		m.mu.Unlock()
		if busy {
			continue
		}
		z, err := m.Zones.Find(ctx, c.ZoneID)
		if err != nil {
			continue
		}
		last := c.UpdatedAt()
		if c.LastActiveAt != nil {
			last = *c.LastActiveAt
		}
		if m.now().Sub(last) < time.Duration(z.IdleMinutes)*time.Minute {
			continue
		}
		if err := m.Sleep(ctx, c.ID()); err != nil {
			m.Log.Warn("idle sleep failed", "computer", c.ID(), "error", err)
			continue
		}
		slept++
	}
	if slept > 0 && m.OnSleep != nil {
		m.OnSleep(ctx)
	}
	return slept, nil
}

// Sleep checkpoints a computer and sleeps its machine.
func (m *Manager) Sleep(ctx context.Context, computerID uuid.UUID) error {
	if err := m.Checkpoint(ctx, computerID); err != nil {
		return err
	}
	m.mu.Lock()
	lv := m.live[computerID]
	m.mu.Unlock()
	if lv != nil {
		if err := lv.machine.Sleep(ctx); err != nil && !errors.Is(err, ErrGone) {
			return err
		}
	}
	return m.setState(ctx, computerID, StateSleeping, "")
}

// CheckpointAll checkpoints every live computer (shutdown, REQ-CORE-008).
func (m *Manager) CheckpointAll(ctx context.Context) error {
	m.mu.Lock()
	ids := make([]uuid.UUID, 0, len(m.live))
	for id := range m.live {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	var errs []error
	for _, id := range ids {
		if err := m.Checkpoint(ctx, id); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Destroy removes the machines of a zone (zone deletion, tests).
func (m *Manager) Destroy(ctx context.Context, computerID uuid.UUID) error {
	m.mu.Lock()
	lv := m.live[computerID]
	delete(m.live, computerID)
	m.mu.Unlock()
	if lv != nil {
		_ = lv.machine.Destroy(ctx)
	}
	return m.setState(ctx, computerID, StateNone, "")
}

// Fork is a temporary machine with a copy of a zone's workspaces (§10.4, forked sub-agents).
type Fork struct {
	m       *Manager
	machine Machine
	Client  *client.Client
	ZoneID  uuid.UUID
}

// Destroy removes the fork machine.
func (f *Fork) Destroy(ctx context.Context) error { return f.machine.Destroy(ctx) }

// Fork checkpoints the zone's computer and starts a fork: native when the provider supports
// it, else a new machine that imports the current workspace revisions ("copy" fork).
func (m *Manager) Fork(ctx context.Context, zoneID uuid.UUID, botIDs []uuid.UUID) (*Fork, error) {
	l, err := m.Acquire(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	defer l.Release()
	for _, b := range botIDs {
		if err := m.EnsureBotHome(ctx, l, b); err != nil {
			return nil, err
		}
	}
	if err := m.Checkpoint(ctx, l.Computer); err != nil {
		return nil, err
	}
	z, err := m.Zones.Find(ctx, zoneID)
	if err != nil {
		return nil, err
	}
	prov, _, err := m.providerFor(ctx, z)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	parent := m.live[l.Computer]
	m.mu.Unlock()
	if prov.Caps().NativeFork && parent != nil {
		if mach, err := parent.machine.Fork(ctx); err == nil {
			c, _ := drel.NewRepository(m.E, ComputerMeta).Where(Computers.ID.Eq(l.Computer)).First(ctx)
			lv := &live{token: parent.token, machine: mach, imported: map[string]bool{}}
			if err := m.waitHealthy(ctx, lv, c, mach); err != nil {
				_ = mach.Destroy(ctx)
				return nil, err
			}
			return &Fork{m: m, machine: mach, Client: lv.client, ZoneID: zoneID}, nil
		}
	}
	token := newToken()
	certPEM, keyPEM, err := agentd.GenerateCert([]string{"fettle-computer"})
	if err != nil {
		return nil, err
	}
	if m.Redact != nil {
		m.Redact("computer token", token)
	}
	netCfg, _ := json.Marshal(map[string]any{"mode": z.Network, "allow_hosts": z.AllowHosts})
	mach, err := prov.Create(ctx, Spec{ZoneID: zoneID.String(), Image: m.Image, Size: Size(z.Size), ExtraHosts: m.ExtraHosts, Env: map[string]string{
		"FETTLE_AGENTD_TOKEN": token, "FETTLE_AGENTD_TLS_CERT": string(certPEM), "FETTLE_AGENTD_TLS_KEY": string(keyPEM), "FETTLE_AGENTD_NETWORK": string(netCfg),
	}})
	if err != nil {
		return nil, err
	}
	lv := &live{token: token, machine: mach, imported: map[string]bool{}}
	tmp := &Computer{CertPEM: string(certPEM)}
	tmp.SetID(uuid.New())
	if err := m.waitHealthy(ctx, lv, tmp, mach); err != nil {
		_ = mach.Destroy(ctx)
		return nil, err
	}
	paths := map[string]string{SharedPath: zoneID.String()}
	for _, b := range botIDs {
		paths[BotHome(b)] = b.String()
	}
	for p, owner := range paths {
		kind := "bot_home"
		if p == SharedPath {
			kind = "zone_shared"
		}
		ws, err := m.workspace(ctx, owner, kind)
		if err != nil {
			_ = mach.Destroy(ctx)
			return nil, err
		}
		if ws != nil && ws.ObjectKey != "" {
			if err := m.importArchive(ctx, z.SpaceID, lv, ws, p); err != nil {
				_ = mach.Destroy(ctx)
				return nil, err
			}
		}
	}
	return &Fork{m: m, machine: mach, Client: lv.client, ZoneID: zoneID}, nil
}

// ProviderOf returns the provider row that a zone uses.
func (m *Manager) ProviderOf(ctx context.Context, z *zones.Zone) (*ProviderRow, error) {
	_, row, err := m.providerFor(ctx, z)
	return row, err
}

// ProviderRows returns the instance providers, with a row for each provider of this server.
func (m *Manager) ProviderRows(ctx context.Context) ([]*ProviderRow, error) {
	kinds := make([]string, 0, len(m.Providers))
	for k := range m.Providers {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		if _, err := m.ensureProviderRow(ctx, k); err != nil {
			return nil, err
		}
	}
	return drel.NewRepository(m.E, ProviderRowMeta).Where(ProviderRows.SpaceID.IsNull()).OrderBy(ProviderRows.Kind.Asc()).All(ctx)
}

// SetEnabled turns an instance provider on or off. Turning on host is a hard ask; the caller
// runs it after the card (§11.2).
func (m *Manager) SetEnabled(ctx context.Context, actor audit.Actor, spaceID *uuid.UUID, kind string, on bool) error {
	if _, ok := m.Providers[kind]; !ok {
		return fmt.Errorf("the %s provider is not available on this server", kind)
	}
	if _, err := m.ensureProviderRow(ctx, kind); err != nil {
		return err
	}
	return m.E.WithTx(ctx, func(ctx context.Context) error {
		if _, err := drel.MustFromContext(ctx).Exec(ctx, "UPDATE providers SET enabled = $1, updated_at = $2 WHERE kind = $3 AND space_id IS NULL", on, m.now(), kind); err != nil {
			return err
		}
		return m.Audit.Record(ctx, actor, spaceID, "provider.enable", "provider:"+kind, map[string]string{"enabled": fmt.Sprint(on)})
	})
}

// CheckProvider tells whether a zone may use a provider: it must be on, and it must enforce
// allowlists when the zone has one (REQ-CMP-012). A nil providerID means the default.
func (m *Manager) CheckProvider(ctx context.Context, providerID *uuid.UUID, allowlist bool) error {
	var row *ProviderRow
	var err error
	if providerID == nil {
		if _, ok := m.Providers[m.Default]; !ok {
			return nil // no default provider here; a lease explains it when a run starts
		}
		row, err = m.ensureProviderRow(ctx, m.Default)
	} else {
		row, err = drel.NewRepository(m.E, ProviderRowMeta).Where(ProviderRows.ID.Eq(*providerID)).FirstOrNil(ctx)
	}
	if err != nil {
		return err
	}
	if row == nil {
		return errors.New("this provider does not exist")
	}
	if !row.Enabled {
		return zones.ErrProviderOff
	}
	if allowlist && !row.Caps.NetworkAllowlist {
		return zones.ErrNoAllowlist
	}
	return nil
}

// Replace stops the machine of a zone after a checkpoint, so the next lease creates a new
// machine with the zone's current provider and network rules.
func (m *Manager) Replace(ctx context.Context, zoneID uuid.UUID) error {
	c, err := m.current(ctx, zoneID)
	if err != nil || c == nil || c.State == StateNone {
		return err
	}
	m.mu.Lock()
	lv := m.live[c.ID()]
	m.mu.Unlock()
	if lv != nil {
		if c.State == StateRunning {
			if err := m.Checkpoint(ctx, c.ID()); err != nil {
				return err
			}
		}
		m.mu.Lock()
		delete(m.live, c.ID())
		m.mu.Unlock()
		_ = lv.machine.Destroy(ctx)
	} else if c.ProviderRef != "" {
		if p, ok := m.Providers[m.kindOf(ctx, c.ProviderID)]; ok {
			if mach, err := p.Attach(ctx, c.ProviderRef); err == nil {
				_ = mach.Destroy(ctx)
			}
		}
	}
	return m.setState(ctx, c.ID(), StateNone, "replaced")
}

func (m *Manager) kindOf(ctx context.Context, providerID uuid.UUID) string {
	var kind string
	_ = m.E.QueryRow(ctx, "SELECT kind FROM providers WHERE id = $1", providerID).Scan(&kind)
	return kind
}

// CanFork reports whether a zone can fork its computer: the provider forks natively, or
// the stored workspaces of the zone are 1 GiB or less, so a copy fork is cheap (rule R3).
func (m *Manager) CanFork(ctx context.Context, zoneID uuid.UUID) bool {
	z, err := m.Zones.Find(ctx, zoneID)
	if err != nil {
		return false
	}
	prov, _, err := m.providerFor(ctx, z)
	if err != nil {
		return false
	}
	if prov.Caps().NativeFork {
		return true
	}
	var size int64
	if err := m.E.QueryRow(ctx, "SELECT COALESCE(SUM(w.size), 0) FROM workspaces w LEFT JOIN bots b ON b.id = w.bot_id WHERE w.zone_id = $1 OR b.zone_id = $2", zoneID, zoneID).Scan(&size); err != nil {
		return false
	}
	return size <= 1<<30
}

// DeleteBotHome removes a bot's home workspace: its stored revisions and, when the zone's
// computer runs, the folder on the computer (bot delete, §6.1).
func (m *Manager) DeleteBotHome(ctx context.Context, botID uuid.UUID, zoneID *uuid.UUID) error {
	rows, err := m.E.Query(ctx, "SELECT id FROM workspaces WHERE bot_id = $1", botID)
	if err != nil {
		return err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		keys, err := m.Objects.List(ctx, fmt.Sprintf("workspaces/%s/", id))
		if err != nil {
			return err
		}
		for _, k := range keys {
			if err := m.Objects.Delete(ctx, k); err != nil {
				return err
			}
		}
	}
	if _, err := m.E.Exec(ctx, "DELETE FROM workspaces WHERE bot_id = $1", botID); err != nil {
		return err
	}
	if zoneID == nil {
		return nil
	}
	c, err := m.Current(ctx, *zoneID)
	if err != nil || c == nil {
		return nil
	}
	m.mu.Lock()
	lv := m.live[c.ID()]
	m.mu.Unlock()
	if lv == nil {
		return nil // the computer sleeps; it imports only stored workspaces when it wakes
	}
	l, err := m.Acquire(ctx, *zoneID)
	if err != nil {
		return err
	}
	defer l.Release()
	_, err = l.Client.Exec(ctx, client.ExecRequest{Argv: []string{"rm", "-rf", BotHome(botID)}, TimeoutMS: 60000}, func(string, string) {})
	return err
}

// AddFork keeps a fork for a sub-agent run until DropFork.
func (m *Manager) AddFork(runID uuid.UUID, f *Fork) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.forks == nil {
		m.forks = map[uuid.UUID]*Fork{}
	}
	m.forks[runID] = f
}

// ForkOf returns the fork of a sub-agent run.
func (m *Manager) ForkOf(runID uuid.UUID) (*Fork, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.forks[runID]
	return f, ok
}

// ForkLease is a lease on the fork of a sub-agent run, for its tools. Releasing it does
// nothing; DropFork ends the fork.
func (m *Manager) ForkLease(runID uuid.UUID) (*Lease, bool) {
	f, ok := m.ForkOf(runID)
	if !ok {
		return nil, false
	}
	return &Lease{ZoneID: f.ZoneID, Client: f.Client}, true
}

// DropFork destroys the fork of a sub-agent run.
func (m *Manager) DropFork(ctx context.Context, runID uuid.UUID) error {
	m.mu.Lock()
	f, ok := m.forks[runID]
	delete(m.forks, runID)
	m.mu.Unlock()
	if !ok {
		return nil
	}
	return f.Destroy(ctx)
}
