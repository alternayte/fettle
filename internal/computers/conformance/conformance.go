// Package conformance is the provider conformance suite (§10.4): every provider passes it on
// its target host. Provider tests call Run and Manager with a real provider.
package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/agentd"
	"github.com/alternayte/fettle/agentd/client"
	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/computers"
	"github.com/alternayte/fettle/internal/objects"
	"github.com/alternayte/fettle/internal/spaces"
	"github.com/alternayte/fettle/internal/testenv"
	"github.com/alternayte/fettle/internal/vault"
	"github.com/alternayte/fettle/internal/zones"

	"github.com/alternayte/drel"
)

// Report writes one result line for a provider scenario (§24.1).
func Report(t testing.TB, scn, provider string, pass bool, extra map[string]any) {
	line := map[string]any{"scn": scn, "pass": pass, "provider": provider, "host": runtime.GOOS + "/" + runtime.GOARCH, "at": time.Now().UTC().Format(time.RFC3339)}
	for k, v := range extra {
		line[k] = v
	}
	b, _ := json.Marshal(line)
	t.Log(string(b))
	dir := os.Getenv("FETTLE_REPORT_DIR")
	if dir == "" {
		return
	}
	_ = os.MkdirAll(dir, 0o755)
	f, err := os.OpenFile(filepath.Join(dir, "providers.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = f.Write(append(b, '\n'))
}

func connect(ctx context.Context, t *testing.T, m computers.Machine, token string, cert []byte) *client.Client {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		url, err := m.AgentdURL(ctx)
		if err == nil {
			cl, err := client.New(url, token, cert)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = cl.Health(ctx); err == nil {
				return cl
			}
			last = err
		} else {
			last = err
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("agentd did not answer: %v", last)
	return nil
}

// Run checks the machine lifecycle of a provider with agentd: create, exec, files, export and
// import, sleep and resume, endpoint, destroy and failure on a lost machine.
func Run(t *testing.T, p computers.Provider, image string) {
	ctx := context.Background()
	if err := p.Prepare(ctx, nil); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	token := "tok-" + uuid.NewString()
	cert, key, err := agentd.GenerateCert([]string{"fettle-computer"})
	if err != nil {
		t.Fatal(err)
	}
	m, err := p.Create(ctx, computers.Spec{ZoneID: uuid.NewString(), Image: image, Size: computers.SizeSmall, Env: map[string]string{
		"FETTLE_AGENTD_TOKEN": token, "FETTLE_AGENTD_TLS_CERT": string(cert), "FETTLE_AGENTD_TLS_KEY": string(key), "FETTLE_AGENTD_NETWORK": `{"mode":"open"}`,
	}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	destroyed := false
	t.Cleanup(func() {
		if !destroyed {
			_ = m.Destroy(context.Background())
		}
	})
	cl := connect(ctx, t, m, token, cert)

	t.Run("endpoint", func(t *testing.T) {
		url, err := m.AgentdURL(ctx)
		if err != nil || !strings.HasPrefix(url, "https://") {
			t.Fatalf("agentd URL %q %v", url, err)
		}
		other, _, _ := agentd.GenerateCert([]string{"x"})
		bad, _ := client.New(url, token, other)
		if _, err := bad.Health(ctx); err == nil {
			t.Fatal("a client pinned to another certificate connected")
		}
		wrong, _ := client.New(url, "wrong", cert)
		if _, err := wrong.Health(ctx); !errors.Is(err, client.ErrUnauthorized) {
			t.Fatalf("wrong token: %v", err)
		}
	})
	t.Run("exec", func(t *testing.T) {
		var out strings.Builder
		res, err := cl.Exec(ctx, client.ExecRequest{Shell: "echo hello-$((40+2)); exit 3"}, func(_, d string) { out.WriteString(d) })
		if err != nil || res.ExitCode != 3 || !strings.Contains(out.String(), "hello-42") {
			t.Fatalf("exec: %+v %q %v", res, out.String(), err)
		}
	})
	t.Run("files", func(t *testing.T) {
		if err := cl.WriteFile(ctx, "bots/b1/notes/a.txt", []byte("alpha"), 0o644); err != nil {
			t.Fatal(err)
		}
		b, err := cl.ReadFile(ctx, "bots/b1/notes/a.txt")
		if err != nil || string(b) != "alpha" {
			t.Fatalf("read %q %v", b, err)
		}
		ents, err := cl.List(ctx, "bots/b1", true)
		if err != nil || len(ents) == 0 {
			t.Fatalf("list %v %v", ents, err)
		}
		if _, err := cl.ReadFile(ctx, "../etc/passwd"); err == nil {
			t.Fatal("a path outside home was readable")
		}
	})
	t.Run("export_import", func(t *testing.T) {
		_ = cl.WriteFile(ctx, "bots/b1/node_modules/x/index.js", []byte("skip"), 0o644)
		rc, err := cl.Export(ctx, "bots/b1", nil)
		if err != nil {
			t.Fatal(err)
		}
		arch, _ := io.ReadAll(rc)
		_ = rc.Close()
		if err := cl.Import(ctx, "bots/b2", bytes.NewReader(arch)); err != nil {
			t.Fatal(err)
		}
		if b, err := cl.ReadFile(ctx, "bots/b2/notes/a.txt"); err != nil || string(b) != "alpha" {
			t.Fatalf("imported file %q %v", b, err)
		}
		if _, err := cl.Stat(ctx, "bots/b2/node_modules/x/index.js"); !errors.Is(err, client.ErrNotFound) {
			t.Fatalf("node_modules was exported: %v", err)
		}
	})
	t.Run("sleep_resume", func(t *testing.T) {
		if err := m.Sleep(ctx); err != nil {
			t.Fatal(err)
		}
		if err := m.Resume(ctx); err != nil {
			t.Fatal(err)
		}
		cl = connect(ctx, t, m, token, cert)
		if b, err := cl.ReadFile(ctx, "bots/b1/notes/a.txt"); err != nil || string(b) != "alpha" {
			t.Fatalf("after resume %q %v", b, err)
		}
	})
	t.Run("destroy_and_lost_machine", func(t *testing.T) {
		ref := m.Ref()
		if _, err := p.Attach(ctx, ref); err != nil {
			t.Fatalf("attach a live machine: %v", err)
		}
		if err := m.Destroy(ctx); err != nil {
			t.Fatal(err)
		}
		destroyed = true
		if _, err := p.Attach(ctx, ref); !errors.Is(err, computers.ErrGone) {
			t.Fatalf("attach after destroy: %v", err)
		}
		if _, err := m.AgentdURL(ctx); err == nil {
			t.Fatal("a destroyed machine has an agentd URL")
		}
	})
}

// Manager checks the computer manager with a provider: acquire, bot home, checkpoint,
// sleep and resume, copy fork, and replacement after a restart restore the files.
func Manager(t *testing.T, kind string, p computers.Provider, image string) {
	ctx := context.Background()
	db, cfg := testenv.Open(t, "sqlite")
	master, _ := vault.ParseMasterKey(testenv.TestMasterKey)
	al := audit.New(db.Engine)
	v := vault.New(db.Engine, al, master)
	store, err := objects.NewDisk(filepath.Join(cfg.Data, "objects"))
	if err != nil {
		t.Fatal(err)
	}
	zs := zones.New(db.Engine, al)
	sp := &spaces.Space{Name: "acme", TimeZone: "UTC"}
	if err := db.Engine.WithTx(ctx, func(ctx context.Context) error {
		drel.Repo(drel.MustFromContext(ctx), spaces.SpaceMeta).Add(sp)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	personal, err := zs.CreateDefaults(ctx, sp.ID())
	if err != nil {
		t.Fatal(err)
	}
	newManager := func() *computers.Manager {
		return computers.NewManager(&computers.Manager{E: db.Engine, Zones: zs, Vault: v, Objects: store, Providers: map[string]computers.Provider{kind: p}, Default: kind, Image: image})
	}
	mgr := newManager()
	bot := uuid.New()
	l, err := mgr.Acquire(ctx, personal.ID())
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.EnsureBotHome(ctx, l, bot); err != nil {
		t.Fatal(err)
	}
	home := computers.BotHome(bot)
	if err := l.Client.WriteFile(ctx, home+"/report.md", []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	l.Release()
	c, _ := mgr.Current(ctx, personal.ID())
	if err := mgr.Sleep(ctx, c.ID()); err != nil {
		t.Fatalf("sleep: %v", err)
	}
	l, err = mgr.Acquire(ctx, personal.ID())
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if b, err := l.Client.ReadFile(ctx, home+"/report.md"); err != nil || string(b) != "v1" {
		t.Fatalf("after resume %q %v", b, err)
	}
	l.Release()

	f, err := mgr.Fork(ctx, personal.ID(), []uuid.UUID{bot})
	if err != nil {
		t.Fatalf("fork: %v", err)
	}
	if b, err := f.Client.ReadFile(ctx, home+"/report.md"); err != nil || string(b) != "v1" {
		t.Fatalf("fork file %q %v", b, err)
	}
	_ = f.Client.WriteFile(ctx, home+"/report.md", []byte("fork"), 0o644)
	_ = f.Destroy(ctx)

	// A new manager (as after a restart) does not know the token: it replaces the machine and
	// restores the workspace from the object store.
	c, _ = mgr.Current(ctx, personal.ID())
	if err := mgr.Checkpoint(ctx, c.ID()); err != nil {
		t.Fatal(err)
	}
	mgr2 := newManager()
	l, err = mgr2.Acquire(ctx, personal.ID())
	if err != nil {
		t.Fatalf("replace: %v", err)
	}
	if err := mgr2.EnsureBotHome(ctx, l, bot); err != nil {
		t.Fatal(err)
	}
	if b, err := l.Client.ReadFile(ctx, home+"/report.md"); err != nil || string(b) != "v1" {
		t.Fatalf("restored file %q %v", b, err)
	}
	l.Release()
	c2, _ := mgr2.Current(ctx, personal.ID())
	if c2.ID() == c.ID() {
		t.Fatal("the replacement kept the old computer row and token")
	}
	t.Cleanup(func() { _ = mgr2.Destroy(context.Background(), c2.ID()); _ = mgr.Destroy(context.Background(), c.ID()) })
}
