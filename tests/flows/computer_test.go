//go:build e2e

package flows

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alternayte/fettle/internal/apptest"
	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
)

// computerImage is the image that `just image` builds.
func computerImage() string {
	if v := os.Getenv("FETTLE_COMPUTER_IMAGE"); v != "" {
		return v
	}
	return "fettle-computer:dev"
}

func docker(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// fixtureHosts runs the fixture web server (§24.4) in a container on the default bridge and
// returns the FETTLE_COMPUTER_HOSTS value that resolves the fixture sites inside computers.
func fixtureHosts(t *testing.T) (string, map[string]string) {
	t.Helper()
	bin, err := filepath.Abs("../../bin/linux/fettle-fakes")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("the Linux fakes binary is missing at %s; run just e2e", bin)
	}
	// One container per site, so each site has its own address and network allowlists,
	// which work on addresses, can tell the sites apart.
	var hosts []string
	ids := map[string]string{}
	for _, h := range []string{"docs.test", "shop.test", "evil.test", "slow.test"} {
		id := docker(t, "run", "-d", "--rm", "--user", "0", "--entrypoint", "/fakes/fettle-fakes",
			"-v", filepath.Dir(bin)+":/fakes:ro", computerImage(), "fake", "serve", "--web-addr", ":80")
		t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", id).Run() })
		ip := docker(t, "inspect", "-f", "{{.NetworkSettings.IPAddress}}", id)
		if ip == "" {
			t.Fatal("the fixture container has no address on the default bridge")
		}
		hosts = append(hosts, h+"="+ip)
		ids[h] = id
	}
	return strings.Join(hosts, ","), ids
}

// startComputers runs fettle with the docker provider and the fixture sites. It removes the
// computers that the server made when the test ends.
func startComputers(t *testing.T, dialect string, env ...string) *server {
	t.Helper()
	before := map[string]bool{}
	for _, id := range strings.Fields(docker(t, "ps", "-aq", "--filter", "label=fettle.computer=1")) {
		before[id] = true
	}
	t.Cleanup(func() {
		out, _ := exec.Command("docker", "ps", "-aq", "--filter", "label=fettle.computer=1").Output()
		for _, id := range strings.Fields(string(out)) {
			if !before[id] {
				_ = exec.Command("docker", "rm", "-f", id).Run()
			}
		}
	})
	hosts, ids := fixtureHosts(t)
	env = append([]string{"FETTLE_DEFAULT_PROVIDER=docker", "FETTLE_COMPUTER_IMAGE=" + computerImage(), "FETTLE_COMPUTER_HOSTS=" + hosts}, env...)
	s := startServer(t, dialect, env...)
	s.env, s.fixtures = env, ids
	s.SetupModel("openai-chat", "", 128000)
	return s
}

// fixtureLog returns the request lines of one fixture site.
func (s *server) fixtureLog(host string) string {
	s.T.Helper()
	out, _ := exec.Command("docker", "logs", s.fixtures[host]).CombinedOutput()
	return string(out)
}

// kill ends the server process at once, as a crash does.
func (s *server) kill() {
	s.T.Helper()
	_ = s.cmd.Process.Kill()
	_ = s.cmd.Wait()
}

// restart starts a new server process on the same data folder and database.
func (s *server) restart() *server {
	s.T.Helper()
	n := startOn(s.T.(*testing.T), s.data, "sqlite", s.Fake, s.FakeURL, s.env...)
	n.env, n.fixtures = s.env, s.fixtures
	return n
}

// script adds a fake model script that a bot runs when its instructions hold the marker.
func (s *server) script(name string, steps ...fakemodel.Step) {
	s.Fake.AddScript(fakemodel.Script{Name: name, Match: fakemodel.Match{System: "[[script:" + name + "]]"}, Steps: steps})
}

func call(name string, args map[string]any) []fakemodel.ScriptCall {
	return []fakemodel.ScriptCall{{Name: name, Args: args}}
}

// instruct sets a bot's instructions to a script marker.
func (s *server) instruct(botID, marker string) {
	s.T.Helper()
	var b map[string]any
	s.MustDo("GET", s.SP("/bots/%s", botID), nil, &b)
	s.MustDo("PATCH", s.SP("/bots/%s", botID), map[string]any{"name": b["name"], "job": b["job"], "instructions": "[[script:" + marker + "]]"}, nil)
}

// toolResults returns the result or error text of each call of a tool in a run, in order.
func toolResults(r apptest.Run, tool string) []string {
	var out []string
	for _, ev := range r.Events {
		if ev.Type != "tool_call" && ev.Type != "tool_result" {
			continue
		}
		var d struct {
			Tool   string `json:"tool"`
			Result string `json:"result"`
			Error  string `json:"error"`
		}
		_ = json.Unmarshal(ev.Data, &d)
		if d.Tool == tool {
			out = append(out, d.Result+d.Error)
		}
	}
	return out
}

// runTool asks Chief to run one scripted tool call and returns the tool's output.
func (s *server) runTool(name string, args map[string]any) string {
	s.T.Helper()
	marker := fmt.Sprintf("tool-%s-%d", name, time.Now().UnixNano())
	s.script(marker, fakemodel.Step{ToolCalls: call(name, args)}, fakemodel.Step{Text: "Done."})
	chief := s.Chief()
	s.instruct(chief.ID, marker)
	run, _ := s.Post(chief.ChatID, "Please do the step.")
	r := s.WaitRun(run, "done", "failed", "escalated", "waiting")
	res := toolResults(r, name)
	if len(res) == 0 {
		s.T.Fatalf("no %s result: %s", name, r.EventTypes())
	}
	return res[0]
}
