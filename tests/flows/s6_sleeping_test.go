//go:build e2e && providers

package flows

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/alternayte/fettle/internal/apptest"
	"github.com/alternayte/fettle/internal/computers/conformance"
)

// replyLatency posts one message that makes the bot run a command in its computer and
// returns the time from the post to the end of the run, on the server's clock.
func replyLatency(t *testing.T, e *apptest.Env, b apptest.Bot) time.Duration {
	t.Helper()
	posted := time.Now()
	run := runExec(e, b.ChatID, b.ID, "echo ready")
	e.WaitRun(run, "done")
	var r struct {
		FinishedAt *time.Time `json:"finished_at"`
	}
	e.MustDo("GET", e.SP("/runs/%s", run), nil, &r)
	if r.FinishedAt == nil {
		t.Fatalf("run %s has no end time", run)
	}
	return r.FinishedAt.Sub(posted)
}

func median(t *testing.T, e *apptest.Env, b apptest.Bot, n int) time.Duration {
	t.Helper()
	var xs []time.Duration
	for i := 0; i < n; i++ {
		xs = append(xs, replyLatency(t, e, b))
	}
	sort.Slice(xs, func(i, j int) bool { return xs[i] < xs[j] })
	return xs[n/2]
}

func dockerMemGiB(t *testing.T) float64 {
	out, err := exec.Command("docker", "info", "--format", "{{.MemTotal}}").Output()
	if err != nil {
		t.Fatal(err)
	}
	var b float64
	_, _ = fmt.Sscanf(strings.TrimSpace(string(out)), "%f", &b)
	return b / (1 << 30)
}

// SCN-CMP-017 (NFR-010): 20 sleeping bots in 4 zones do not slow a running bot's reply
// by more than 10 % on a host with 8 GB RAM (the Docker host of this test).
func TestSCN_CMP_017_SleepingBotsDoNotSlowARunningBot(t *testing.T) {
	mem := dockerMemGiB(t)
	if mem > 8.5 {
		// The scenario is defined for an 8 GB host; a larger host reports it as skipped,
		// which is not a pass.
		conformance.Report(t, "SCN-CMP-017", "docker", false, map[string]any{"skipped": fmt.Sprintf("the Docker host has %.1f GiB, not 8 GB", mem)})
		return
	}
	e := dockerApp(t)
	e.MustDo("PUT", e.SP("/settings/agent.max_turns"), map[string]any{"value": 50}, nil)
	runner := newBot(e, "Runner", "Runs commands", "runner")
	// Warm the runner's computer, then measure the baseline.
	replyLatency(t, e, runner)
	base := median(t, e, runner, 15)

	// Four zones with five bots each; each zone's computer starts once, then sleeps.
	for z := 1; z <= 4; z++ {
		name := fmt.Sprintf("team-%d", z)
		var card struct {
			CardID string `json:"card_id"`
		}
		e.MustDo("POST", e.SP("/zones"), map[string]any{"name": name, "network": "open", "size": "browser", "idle_minutes": 10}, &card)
		e.MustDo("POST", e.SP("/cards/%s/answer", card.CardID), map[string]any{"action": "allow"}, nil)
		zone := struct{ ID string }{ID: zoneID(e, name)}
		var first apptest.Bot
		for i := 1; i <= 5; i++ {
			var b apptest.Bot
			e.MustDo("POST", e.SP("/bots"), map[string]any{"name": fmt.Sprintf("Sleeper %d-%d", z, i), "job": "Sleeps", "zone_id": zone.ID}, &b)
			if i == 1 {
				first = b
			}
		}
		e.WaitRun(runExec(e, first.ChatID, first.ID, "echo awake"), "done")
		var after struct {
			ComputerState string `json:"computer_state"`
		}
		e.MustDo("POST", e.SP("/zones/%s/computer/stop", zone.ID), nil, &after)
		if after.ComputerState != "sleeping" {
			t.Fatalf("zone team-%d computer is %q after stop", z, after.ComputerState)
		}
	}
	loaded := median(t, e, runner, 15)
	ratio := float64(loaded) / float64(base)
	pass := ratio <= 1.10
	conformance.Report(t, "SCN-CMP-017", "docker", pass, map[string]any{"baseline_ms": base.Milliseconds(), "with_sleepers_ms": loaded.Milliseconds(), "ratio": fmt.Sprintf("%.3f", ratio), "docker_mem_gib": fmt.Sprintf("%.2f", mem), "sleeping_bots": 20, "zones": 4})
	if !pass {
		t.Fatalf("the reply latency is %s with 20 sleeping bots against %s without (%.0f %%); NFR-010 allows 10 %%", loaded, base, (ratio-1)*100)
	}
}
