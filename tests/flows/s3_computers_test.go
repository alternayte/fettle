//go:build e2e

package flows

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/apptest"
	"github.com/alternayte/fettle/internal/audit"
	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
)

// dockerApp starts an in-process app whose computers run in Docker, for tests that move
// the computer clock.
func dockerApp(t *testing.T) *apptest.Env {
	t.Helper()
	t.Setenv("FETTLE_DEFAULT_PROVIDER", "docker")
	t.Setenv("FETTLE_COMPUTER_IMAGE", computerImage())
	before := map[string]bool{}
	for _, id := range strings.Fields(docker(t, "ps", "-aq", "--filter", "label=fettle.computer=1")) {
		before[id] = true
	}
	t.Cleanup(func() {
		for _, id := range strings.Fields(docker(t, "ps", "-aq", "--filter", "label=fettle.computer=1")) {
			if !before[id] {
				docker(t, "rm", "-f", id)
			}
		}
	})
	e := apptest.Start(t, "sqlite")
	e.SetupModel("openai-chat", "", 128000)
	return e
}

// runExec makes a bot run one command and returns its run ID.
func runExec(e *apptest.Env, chatID, botID, command string) string {
	e.T.Helper()
	marker := fmt.Sprintf("x%d", time.Now().UnixNano())
	e.Fake.AddScript(fakemodel.Script{Name: marker, Match: fakemodel.Match{System: "[[script:" + marker + "]]"}, Steps: []fakemodel.Step{{ToolCalls: call("exec", map[string]any{"command": command})}, {Text: "Done."}}})
	var b map[string]any
	e.MustDo("GET", e.SP("/bots/%s", botID), nil, &b)
	e.MustDo("PATCH", e.SP("/bots/%s", botID), map[string]any{"name": b["name"], "job": b["job"], "instructions": "[[script:" + marker + "]]"}, nil)
	run, _ := e.Post(chatID, "Run the command.")
	return run
}

func zoneID(e *apptest.Env, name string) string {
	var zs struct {
		Zones []struct{ ID, Name string } `json:"zones"`
	}
	e.MustDo("GET", e.SP("/zones"), nil, &zs)
	for _, z := range zs.Zones {
		if z.Name == name {
			return z.ID
		}
	}
	e.T.Fatalf("no zone %s", name)
	return ""
}

func TestSCN_CMP_003_IdleSleepAndAdmission(t *testing.T) {
	e := dockerApp(t)
	ctx := context.Background()
	if _, err := e.App.Settings.Set(ctx, audit.System, nil, "computers.max_running", 1); err != nil {
		t.Fatal(err)
	}
	personal := zoneID(e, "personal")
	e.MustDo("PATCH", e.SP("/zones/%s", personal), map[string]any{"name": "personal", "network": "open", "size": "small", "idle_minutes": 1}, nil)
	var research apptest.Bot
	e.MustDo("POST", e.SP("/bots"), map[string]any{"name": "Research", "job": "Finds sources", "zone_id": zoneID(e, "browsing")}, &research)

	chief := e.Chief()
	r1 := runExec(e, chief.ChatID, chief.ID, "echo first")
	e.WaitRun(r1, "done")
	clock := time.Now().UTC()
	e.App.Computers.SetClock(func() time.Time { return clock })
	r2 := runExec(e, research.ChatID, research.ID, "echo second")
	r := e.WaitRun(r2, "waiting")
	if !r.HasEvent("computer_wait") {
		t.Fatalf("the second zone's run does not wait for a computer: %s", r.EventTypes())
	}
	var wait string
	for _, ev := range r.Events {
		if ev.Type == "computer_wait" {
			wait = string(ev.Data)
		}
	}
	if !strings.Contains(wait, "Waiting for a free computer") {
		t.Fatalf("wait reason %s", wait)
	}
	// Under a minute without leases: the computer stays.
	clock = clock.Add(30 * time.Second)
	if n, _ := e.App.Computers.SleepIdle(ctx); n != 0 {
		t.Fatal("the computer slept before its idle minute")
	}
	// The tick meters the running time so far (§8.4).
	e.App.Computers.Meter(ctx)
	if secs, n := computerTime(t, e, personal); n != 1 || secs < 30 {
		t.Fatalf("after one meter: %d rows, %d s", n, secs)
	}
	c, _ := e.App.Computers.Current(ctx, uuid.MustParse(personal))
	rev := c.WorkspaceRev
	clock = clock.Add(2 * time.Minute)
	if n, err := e.App.Computers.SleepIdle(ctx); err != nil || n != 1 {
		t.Fatalf("idle sleep: %d %v", n, err)
	}
	c, _ = e.App.Computers.Current(ctx, uuid.MustParse(personal))
	if c.State != "sleeping" || c.WorkspaceRev <= rev {
		t.Fatalf("personal computer is %s at revision %d (was %d)", c.State, c.WorkspaceRev, rev)
	}
	// The sleep meters the rest, so the zone has its whole running time once.
	if secs, n := computerTime(t, e, personal); n != 2 || secs < 150 || secs > 240 {
		t.Fatalf("after the sleep: %d rows, %d s", n, secs)
	}
	// The sleep frees the slot, and the waiting run starts.
	e.WaitRun(r2, "done")
}

func TestSCN_CMP_004_WorkspaceArchiveLimitAndRestore(t *testing.T) {
	e := dockerApp(t)
	ctx := context.Background()
	chief := e.Chief()
	run := runExec(e, chief.ChatID, chief.ID, "echo 'the kiln log' > notes.md && mkdir -p node_modules/pkg && echo 'module' > node_modules/pkg/index.js && truncate -s 6G big.bin")
	e.WaitRun(run, "done")
	zone := uuid.MustParse(zoneID(e, "personal"))
	c, _ := e.App.Computers.Current(ctx, zone)
	if err := e.App.Computers.Checkpoint(ctx, c.ID()); err == nil {
		t.Fatal("a 6 GiB home was checkpointed")
	}
	var warned bool
	for _, m := range e.Messages(chief.ChatID) {
		if card, ok := m["card"].(map[string]any); ok && card["kind"] == "warning" && card["status"] == "pending" {
			warned = true
		}
	}
	if !warned {
		t.Fatal("no oversize warning card")
	}
	if c, _ = e.App.Computers.Current(ctx, zone); c.State != "running" {
		t.Fatalf("the computer is %s; it keeps running over the limit", c.State)
	}
	run = runExec(e, chief.ChatID, chief.ID, "rm big.bin")
	e.WaitRun(run, "done")
	if err := e.App.Computers.Checkpoint(ctx, c.ID()); err != nil {
		t.Fatalf("checkpoint below the limit: %v", err)
	}
	// The archive is not readable without the space data key.
	var key string
	if err := e.App.DB.Engine.QueryRow(ctx, "SELECT object_key FROM workspaces WHERE kind = 'bot_home'").Scan(&key); err != nil {
		t.Fatal(err)
	}
	rc, err := e.App.Objects.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(rc)
	_ = rc.Close()
	if strings.Contains(string(raw), "kiln log") || len(raw) < 4 || string(raw[:4]) == "\x28\xb5\x2f\xfd" {
		t.Fatal("the archive is plain or plain zstd")
	}
	// A new machine restores the files, without node_modules.
	if err := e.App.Computers.Replace(ctx, zone); err != nil {
		t.Fatal(err)
	}
	run = runExec(e, chief.ChatID, chief.ID, "cat notes.md; ls node_modules 2>&1 | head -1")
	r := e.WaitRun(run, "done")
	out := strings.Join(toolResults(r, "exec"), "\n")
	if !strings.Contains(out, "the kiln log") || !strings.Contains(out, "No such file") {
		t.Fatalf("restored home: %s", out)
	}
}

// computerTime returns the metered seconds and the number of computer usage rows of a zone.
func computerTime(t *testing.T, e *apptest.Env, zone string) (secs int64, rows int) {
	t.Helper()
	err := e.App.DB.Engine.QueryRow(context.Background(), "SELECT COALESCE(SUM(quantity),0), COUNT(*) FROM usage_events WHERE kind = 'computer' AND unit = 'seconds' AND size = 'small' AND zone_id = $1", uuid.MustParse(zone)).Scan(&secs, &rows)
	if err != nil {
		t.Fatal(err)
	}
	return secs, rows
}
