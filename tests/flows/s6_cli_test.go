//go:build e2e

package flows

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/alternayte/fettle/internal/apptest"
	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
)

// cli runs the fettle binary with env and stdin, and returns its output.
func cli(t *testing.T, env []string, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command(binary(t), args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fettle %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// chatWithCard drives `fettle chat Inbox "hello" --watch` against a server and checks the
// streamed reply and the numbered card prompt (SCN-UX-007).
func chatWithCard(t *testing.T, e *apptest.Env, env []string) {
	t.Helper()
	e.SetupModel("openai-chat", "", 128000)
	if out := cli(t, env, "", "bot", "create", "--template", "inbox"); !strings.Contains(out, "Created Inbox") {
		t.Fatalf("bot create: %s", out)
	}
	e.Fake.AddScript(fakemodel.Script{Name: "cli-hello", Match: fakemodel.Match{User: "hello"}, Steps: []fakemodel.Step{
		{ToolCalls: []fakemodel.ScriptCall{{Name: "ask_user", Args: map[string]any{"question": "Draft a reply to the newest mail?", "options": []string{"Yes", "No"}}}}},
		{Text: "Hello. I drafted the reply and saved it as a draft."},
	}})

	out := cli(t, env, "1\n", "chat", "Inbox", "hello", "--watch")
	for _, want := range []string{
		"[question] Draft a reply to the newest mail?",
		"  1) Yes\n  2) No\n",
		"Choose 1-2: ",
		"· Yes",
		"Inbox: Hello. I drafted the reply and saved it as a draft.",
		"· run done",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("chat output lacks %q:\n%s", want, out)
		}
	}
	// The card has the answer from the prompt.
	inbox := findBot(t, e, "Inbox")
	answered := false
	for _, m := range e.Messages(inbox.ChatID) {
		if c, ok := m["card"].(map[string]any); ok && c["kind"] == "question" && c["answer"] == "Yes" {
			answered = true
		}
	}
	if !answered {
		t.Fatalf("the question card has no answer Yes: %v", e.Messages(inbox.ChatID))
	}

	list := cli(t, env, "", "bot", "list")
	if !strings.Contains(list, "Inbox") || !strings.Contains(list, "(primary)") {
		t.Fatalf("bot list:\n%s", list)
	}
	show := cli(t, env, "", "bot", "show", "inbox")
	if !strings.Contains(show, "inbox-triage") || !strings.Contains(show, "Instructions (version 1):") {
		t.Fatalf("bot show:\n%s", show)
	}
	var rs struct {
		Runs []struct {
			RunID string `json:"run_id"`
			Line  struct {
				RunID string `json:"run_id"`
			} `json:"line"`
			BotName string `json:"bot_name"`
		} `json:"runs"`
	}
	e.MustDo("GET", e.SP("/runs"), nil, &rs)
	runID := ""
	for _, r := range rs.Runs {
		if r.BotName == "Inbox" {
			runID = r.Line.RunID
		}
	}
	run := cli(t, env, "", "run", "show", runID)
	for _, want := range []string{"State     done", "card_posted", "card_answered", "run_finished"} {
		if !strings.Contains(run, want) {
			t.Fatalf("run show lacks %q:\n%s", want, run)
		}
	}
	if out := cli(t, env, "", "bot", "archive", "Inbox"); !strings.Contains(out, "Archived Inbox") {
		t.Fatalf("bot archive: %s", out)
	}
}

func findBot(t *testing.T, e *apptest.Env, name string) apptest.Bot {
	t.Helper()
	var out struct {
		Bots []apptest.Bot `json:"bots"`
	}
	e.MustDo("GET", e.SP("/bots?archived=true"), nil, &out)
	for _, b := range out.Bots {
		if b.Name == name {
			return b
		}
	}
	t.Fatalf("no bot %s", name)
	return apptest.Bot{}
}

func TestSCN_UX_007_CLIChatWatchInLocalAndServerModes(t *testing.T) {
	t.Run("local", func(t *testing.T) {
		s := startServer(t, "sqlite")
		chatWithCard(t, s.Env, []string{"FETTLE_DATA=" + s.data, "FETTLE_SERVER=", "FETTLE_TOKEN="})
	})
	t.Run("server", func(t *testing.T) {
		fake, err := fakemodel.New()
		if err != nil {
			t.Fatal(err)
		}
		fs := httptest.NewServer(fake)
		t.Cleanup(fs.Close)
		s := startServerMode(t, "https://fettle.test")
		code, h, body := s.do("POST", "/api/auth/sign-in/email", map[string]string{"email": ownerEmail, "password": ownerPassword}, s.baseURL)
		if code != 200 {
			t.Fatalf("sign-in: %d %s", code, body)
		}
		for _, sc := range h.Values("Set-Cookie") {
			if strings.HasPrefix(sc, "fettle_session=") {
				s.cookie = strings.SplitN(sc, ";", 2)[0]
			}
		}
		code, _, body = s.do("POST", "/api/auth/api-keys", map[string]string{"name": "cli"}, s.baseURL)
		if code/100 != 2 {
			t.Fatalf("create token: %d %s", code, body)
		}
		var key struct {
			Plaintext string `json:"plaintext"`
		}
		if err := json.Unmarshal([]byte(body), &key); err != nil || key.Plaintext == "" {
			t.Fatalf("token answer %s", body)
		}
		e := apptest.Attach(t, s.url, key.Plaintext, fake, fs.URL)
		chatWithCard(t, e, []string{"FETTLE_SERVER=" + s.url, "FETTLE_TOKEN=" + key.Plaintext, "FETTLE_DATA=" + t.TempDir()})
	})
}
