//go:build e2e

package flows

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/agentd/client"
	"github.com/alternayte/fettle/internal/apptest"
	"github.com/alternayte/fettle/internal/computers"
	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
	"github.com/alternayte/fettle/internal/fakes/web"
	"github.com/alternayte/fettle/internal/skills"
)

type teachStep struct {
	Seq      int      `json:"seq"`
	Kind     string   `json:"kind"`
	Action   string   `json:"action"`
	URL      string   `json:"url"`
	Role     string   `json:"role"`
	Name     string   `json:"name"`
	Value    string   `json:"value"`
	Secret   string   `json:"secret"`
	Command  string   `json:"command"`
	Cwd      string   `json:"cwd"`
	ExitCode int      `json:"exit_code"`
	Output   string   `json:"output"`
	Notes    []string `json:"notes"`
	Login    int      `json:"login"`
	Shot     bool     `json:"screenshot"`
}

type teachSession struct {
	ID        string      `json:"id"`
	Status    string      `json:"status"`
	Error     string      `json:"error"`
	CardID    string      `json:"card_id"`
	Steps     []teachStep `json:"steps"`
	Questions []struct {
		CardID string `json:"card_id"`
		Topic  string `json:"topic"`
	} `json:"questions"`
	Draft *struct {
		Name    string `json:"name"`
		SkillMD string `json:"skill_md"`
		Inputs  []struct {
			Name string `json:"name"`
		} `json:"inputs"`
	} `json:"draft"`
	SkillID      string `json:"skill_id"`
	SkillVersion int    `json:"skill_version"`
}

// teacher drives a teach session as the person: the bot's browser through agentd, and the
// terminal pane through the server.
type teacher struct {
	e     *apptest.Env
	bot   string
	zone  string
	sess  teachSession
	token string
	pty   *websocket.Conn
}

func desktopZone(e *apptest.Env, size string) string {
	e.T.Helper()
	z := zoneID(e, "personal")
	e.MustDo("PATCH", e.SP("/zones/%s", z), map[string]any{"name": "personal", "network": "open", "size": size, "idle_minutes": 30}, nil)
	return z
}

func startTeach(e *apptest.Env, bot, zone, title string, extra map[string]any) *teacher {
	e.T.Helper()
	body := map[string]any{"bot_id": bot, "title": title}
	for k, v := range extra {
		body[k] = v
	}
	var out struct {
		Session teachSession `json:"session"`
		Control struct {
			Token string `json:"token"`
		} `json:"control"`
	}
	e.MustDo("POST", e.SP("/teach"), body, &out)
	if out.Session.Status != "recording" || out.Control.Token == "" {
		e.T.Fatalf("start: %+v", out)
	}
	return &teacher{e: e, bot: bot, zone: zone, sess: out.Session, token: out.Control.Token}
}

// browser runs fn with the agentd client of the bot's computer.
func (tc *teacher) browser(fn func(ctx context.Context, c *client.Client, id string)) {
	tc.e.T.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	l, err := tc.e.App.Computers.Acquire(ctx, uuid.MustParse(tc.zone))
	if err != nil {
		tc.e.T.Fatal(err)
	}
	defer l.Release()
	fn(ctx, l.Client, "bot-"+tc.bot)
}

func ref(t *testing.T, snap client.Snapshot, role, name string) string {
	t.Helper()
	for _, el := range snap.Elements {
		if el.Role == role && el.Name == name {
			return el.Ref
		}
	}
	t.Fatalf("no %s %q on %s: %+v", role, name, snap.URL, snap.Elements)
	return ""
}

// act does one action on a target that a person sees on the page.
func act(t *testing.T, ctx context.Context, c *client.Client, id, role, name, action, value string) {
	t.Helper()
	snap, err := c.BrowserSnapshot(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.BrowserAct(ctx, id, client.ActRequest{Ref: ref(t, snap, role, name), Action: action, Value: value})
	if err != nil || res.Refused != "" || len(res.Uncertain) > 0 {
		t.Fatalf("%s %s %q: %v %+v", action, role, name, err, res)
	}
}

func (tc *teacher) terminal(command string) {
	tc.e.T.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if tc.pty == nil {
		u := strings.Replace(tc.e.URL, "http://", "ws://", 1) + "/api/v1/teach/pty?" + url.Values{"t": {tc.token}, "s": {tc.sess.ID}, "cols": {"100"}, "rows": {"30"}}.Encode()
		ws, resp, err := websocket.Dial(ctx, u, nil)
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		if err != nil {
			tc.e.T.Fatalf("terminal: %v", err)
		}
		ws.SetReadLimit(1 << 20)
		go func() {
			for {
				if _, _, err := ws.Read(context.Background()); err != nil {
					return
				}
			}
		}()
		tc.e.T.Cleanup(func() { _ = ws.CloseNow() })
		tc.pty = ws
		time.Sleep(time.Second)
	}
	if err := tc.pty.Write(ctx, websocket.MessageBinary, []byte(command)); err != nil {
		tc.e.T.Fatal(err)
	}
}

// waitSteps reads the session until it has n steps.
func (tc *teacher) waitSteps(n int) []teachStep {
	tc.e.T.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		tc.e.MustDo("GET", tc.e.SP("/teach/%s", tc.sess.ID), nil, &tc.sess)
		if len(tc.sess.Steps) >= n {
			return tc.sess.Steps
		}
		time.Sleep(300 * time.Millisecond)
	}
	tc.e.T.Fatalf("only %d of %d steps: %+v", len(tc.sess.Steps), n, tc.sess.Steps)
	return nil
}

func (tc *teacher) wait(ok func(teachSession) bool) teachSession {
	tc.e.T.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		tc.e.MustDo("GET", tc.e.SP("/teach/%s", tc.sess.ID), nil, &tc.sess)
		if ok(tc.sess) {
			return tc.sess
		}
		time.Sleep(200 * time.Millisecond)
	}
	tc.e.T.Fatalf("the teach session did not reach the state: %+v", tc.sess)
	return tc.sess
}

// finish presses Done and answers the bot's questions in the chat.
func (tc *teacher) finish(answers map[string]string) teachSession {
	tc.e.T.Helper()
	tc.e.MustDo("POST", tc.e.SP("/teach/%s/done", tc.sess.ID), nil, &tc.sess)
	s := tc.wait(func(s teachSession) bool { return len(s.Questions) > 0 || s.Status == "failed" })
	if len(s.Questions) == 0 || len(s.Questions) > 3 {
		tc.e.T.Fatalf("questions: %+v", s)
	}
	for _, q := range s.Questions {
		tc.e.MustDo("POST", tc.e.SP("/cards/%s/answer", q.CardID), map[string]any{"action": "answer", "value": answers[q.Topic]}, nil)
	}
	s = tc.wait(func(s teachSession) bool { return s.Status == "review" || s.Status == "failed" })
	if s.Status != "review" {
		tc.e.T.Fatalf("no review: %+v", s)
	}
	return s
}

func (tc *teacher) skillFiles(skillID string) skills.Files {
	tc.e.T.Helper()
	code, body := tc.e.Do("GET", tc.e.SP("/skills/%s/export", skillID), nil, nil)
	if code != 200 {
		tc.e.T.Fatalf("export: %d", code)
	}
	files, err := skills.FromZip([]byte(body))
	if err != nil {
		tc.e.T.Fatal(err)
	}
	// The zip holds the skill folder; the file names start with its name.
	out := skills.Files{}
	for n, b := range files {
		_, rest, ok := strings.Cut(n, "/")
		if !ok {
			rest = n
		}
		out[rest] = b
	}
	return out
}

func reviewCard(e *apptest.Env, chatID, cardID string) map[string]any {
	e.T.Helper()
	for _, m := range e.Messages(chatID) {
		if c, ok := m["card"].(map[string]any); ok && c["id"] == cardID {
			return c
		}
	}
	e.T.Fatalf("no review card %s", cardID)
	return nil
}

// A person teaches "download the acme invoice from docs.test and save its total to
// totals.csv" with one browser part and one command. The bot asks its questions, the review
// card shows both steps with role, name and command, Save makes the skill, and "Try it" with
// another month is a normal run that writes the new total (SCN-TCH-001). A bot in a zone of
// the browser size gets the change to desktop before teaching starts (SCN-TCH-006).
func TestSCN_TCH_001_TeachInvoiceTotalThenTryIt(t *testing.T) {
	hosts, _ := fixtureHosts(t)
	t.Setenv("FETTLE_COMPUTER_HOSTS", hosts)
	e := dockerApp(t)
	chief := e.Chief()
	zone := desktopZone(e, "browser")

	var ready struct {
		Ready     bool   `json:"ready"`
		Size      string `json:"size"`
		CanChange bool   `json:"can_change"`
	}
	e.MustDo("GET", e.SP("/bots/%s/teach-ready", chief.ID), nil, &ready)
	if ready.Ready || ready.Size != "browser" || !ready.CanChange {
		t.Fatalf("ready: %+v", ready)
	}
	if code, body := e.Do("POST", e.SP("/teach"), map[string]any{"bot_id": chief.ID}, nil); code != 409 || !strings.Contains(body, "desktop size") {
		t.Fatalf("teaching in a browser zone got %d: %s", code, body)
	}
	tc := startTeach(e, chief.ID, zone, "Download the acme invoice from docs.test and save its total to totals.csv", map[string]any{"change_size": true})
	e.MustDo("GET", e.SP("/bots/%s/teach-ready", chief.ID), nil, &ready)
	if !ready.Ready || ready.Size != "desktop" {
		t.Fatalf("the zone did not change to desktop: %+v", ready)
	}

	// The browser part: open the invoices and the October invoice, and download it.
	tc.browser(func(ctx context.Context, c *client.Client, id string) {
		if _, err := c.BrowserNavigate(ctx, id, "http://docs.test/invoices"); err != nil {
			t.Fatal(err)
		}
		act(t, ctx, c, id, "link", "Invoice 2026-10", "click", "")
		act(t, ctx, c, id, "link", "Download the invoice", "click", "")
	})
	tc.waitSteps(3)
	e.MustDo("POST", e.SP("/teach/%s/notes", tc.sess.ID), map[string]any{"text": "The newest month that has an invoice."}, nil)
	// The command part.
	tc.terminal("echo \"2026-10,1385.50\" >> totals.csv\r")
	steps := tc.waitSteps(4)
	nav, click, dl, cmd := steps[0], steps[1], steps[2], steps[3]
	if nav.Action != "navigate" || nav.URL != "http://docs.test/invoices" {
		t.Fatalf("navigate step %+v", nav)
	}
	if click.Action != "click" || click.Role != "link" || click.Name != "Invoice 2026-10" || !click.Shot {
		t.Fatalf("click step %+v", click)
	}
	if dl.Role != "link" || dl.Name != "Download the invoice" || len(dl.Notes) != 1 {
		t.Fatalf("download step %+v", dl)
	}
	if cmd.Kind != "command" || cmd.Command != `echo "2026-10,1385.50" >> totals.csv` || cmd.Cwd != "." || cmd.ExitCode != 0 {
		t.Fatalf("command step %+v", cmd)
	}
	// The bot waits while a person teaches on its screen.
	var sc screenOut
	e.MustDo("POST", e.SP("/bots/%s/screen", chief.ID), nil, &sc)
	if sc.Controller != "you" {
		t.Fatalf("the teach session does not hold the control lease: %q", sc.Controller)
	}

	s := tc.finish(map[string]string{"inputs": "The invoice month changes, for example 2026-11.", "success": "totals.csv has a line with the month and the total of its invoice.", "unclear": "Nothing."})
	e.MustDo("POST", e.SP("/bots/%s/screen", chief.ID), nil, &sc)
	if sc.Controller != "none" {
		t.Fatalf("Done did not return the screen: %q", sc.Controller)
	}
	card := reviewCard(e, chief.ChatID, s.CardID)
	data, _ := card["data"].(map[string]any)
	stepsJSON, _ := data["steps"].(string)
	var cardSteps []teachStep
	_ = json.Unmarshal([]byte(stepsJSON), &cardSteps)
	shown := map[string]bool{}
	for _, cs := range cardSteps {
		shown[cs.Role+" "+cs.Name+cs.Command] = true
	}
	if card["kind"] != "teach" || !shown["link Invoice 2026-10"] || !shown[` echo "2026-10,1385.50" >> totals.csv`] {
		t.Fatalf("review card %+v", card)
	}
	if s.Draft == nil || len(s.Draft.Inputs) == 0 || !strings.Contains(s.Draft.SkillMD, "Invoice 2026-10") {
		t.Fatalf("draft %+v", s.Draft)
	}

	e.MustDo("POST", e.SP("/teach/%s/save", s.ID), map[string]any{}, &s)
	if s.Status != "saved" || s.SkillVersion != 1 {
		t.Fatalf("save: %+v", s)
	}
	files := tc.skillFiles(s.SkillID)
	if !strings.Contains(string(files["steps.json"]), `"name": "Invoice 2026-10"`) || !strings.Contains(string(files["evals.json"]), "totals.csv has a line") {
		t.Fatalf("skill files: %s\n%s", files["steps.json"], files["evals.json"])
	}
	for n := range files {
		if strings.HasSuffix(n, ".jpg") || strings.HasSuffix(n, ".png") {
			t.Fatalf("a screenshot is in the skill: %s", n)
		}
	}

	// Try it: a normal run of the bot with the skill and a new month.
	marker := "teach-try"
	e.Fake.AddScript(fakemodel.Script{Name: marker, Match: fakemodel.Match{System: "[[script:" + marker + "]]"}, Steps: []fakemodel.Step{
		{ToolCalls: call("browser_navigate", map[string]any{"url": "http://docs.test/invoices/2026-11"})},
		{ToolCalls: call("exec", map[string]any{"command": `echo "2026-11,990.25" >> totals.csv`})},
		{Text: "Saved the total of 2026-11."},
	}})
	var b map[string]any
	e.MustDo("GET", e.SP("/bots/%s", chief.ID), nil, &b)
	e.MustDo("PATCH", e.SP("/bots/%s", chief.ID), map[string]any{"name": b["name"], "job": b["job"], "instructions": "[[script:" + marker + "]]"}, nil)
	var tried struct {
		ChatID string `json:"chat_id"`
		RunID  string `json:"run_id"`
	}
	e.MustDo("POST", e.SP("/teach/%s/try", s.ID), map[string]any{"inputs": map[string]string{s.Draft.Inputs[0].Name: "2026-11"}}, &tried)
	if tried.RunID == "" {
		t.Fatal("Try it started no run")
	}
	r := e.WaitRun(tried.RunID, "done")
	if res := toolResults(r, "browser_navigate"); len(res) != 1 || !strings.Contains(res[0], "990.25") {
		t.Fatalf("the run did not read the new invoice: %v", res)
	}
	loaded := false
	for _, req := range e.Fake.Requests() {
		if strings.Contains(req.System, "[[script:"+marker+"]]") {
			if strings.Contains(req.System, "Skill "+s.Draft.Name) {
				loaded = true
			}
			for _, m := range req.Messages {
				if strings.Contains(m.Text, "Skill "+s.Draft.Name) || strings.Contains(m.ToolResult, "Skill "+s.Draft.Name) {
					loaded = true
				}
			}
		}
	}
	if !loaded {
		t.Fatal("the run of Try it did not load the skill")
	}
	tc.browser(func(ctx context.Context, c *client.Client, _ string) {
		got, err := c.ReadFile(ctx, computers.BotHome(uuid.MustParse(chief.ID))+"/totals.csv")
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "2026-10,1385.50\n2026-11,990.25\n" {
			t.Fatalf("totals.csv:\n%s", got)
		}
	})
}

// Fields of type password, card and one-time code (by type, autocomplete and name, also
// after a show-password toggle) and a terminal prompt with echo off record no value, and
// the steps become login steps (SCN-TCH-002).
func TestSCN_TCH_002_SecretInputIsNeverRecorded(t *testing.T) {
	hosts, _ := fixtureHosts(t)
	t.Setenv("FETTLE_COMPUTER_HOSTS", hosts)
	e := dockerApp(t)
	chief := e.Chief()
	zone := desktopZone(e, "desktop")
	tc := startTeach(e, chief.ID, zone, "Fill the secure form", nil)
	secrets := map[string]string{"Password": "pw-acme-1111", "Code": "424242", "Verification": "515151", "Card": "4111111111111111", "Card again": "4000056655665556"}
	tc.browser(func(ctx context.Context, c *client.Client, id string) {
		if _, err := c.BrowserNavigate(ctx, id, "http://shop.test/secure"); err != nil {
			t.Fatal(err)
		}
		act(t, ctx, c, id, "textbox", "Password", "type", secrets["Password"])
		act(t, ctx, c, id, "button", "Show password", "click", "")
		act(t, ctx, c, id, "textbox", "Password", "type", "pw-acme-2222")
		for _, f := range []string{"Code", "Verification", "Card", "Card again"} {
			act(t, ctx, c, id, "textbox", f, "type", secrets[f])
		}
		act(t, ctx, c, id, "textbox", "Note", "type", "plain note")
		act(t, ctx, c, id, "button", "Send", "click", "")
	})
	tc.terminal("read -rs -p 'Password: ' pw; echo \"got ${#pw}\"\r")
	time.Sleep(time.Second)
	tc.terminal("pty-acme-secret-3333\r")
	steps := tc.waitSteps(11)
	kinds := map[string]string{}
	for _, s := range steps {
		if s.Action == "type" {
			if s.Secret != "" {
				kinds[s.Name] += s.Secret + " "
			} else {
				kinds[s.Name] += "plain:" + s.Value + " "
			}
		}
	}
	want := map[string]string{"Password": "password password ", "Code": "one_time_code ", "Verification": "one_time_code ", "Card": "card ", "Card again": "card ", "Note": "plain:plain note "}
	for k, v := range want {
		if kinds[k] != v {
			t.Errorf("field %s: recorded %q, want %q", k, kinds[k], v)
		}
	}
	var term *teachStep
	for i := range steps {
		if steps[i].Kind == "command" {
			term = &steps[i]
		}
	}
	if term == nil || term.Secret != "echo_off" || term.Login == 0 || !strings.Contains(term.Output, "got 20") {
		t.Fatalf("terminal step %+v", term)
	}
	for _, s := range steps {
		if s.Secret != "" && s.Login == 0 {
			t.Errorf("secret step %d is not a login step", s.Seq)
		}
	}
	_, body := e.Do("GET", e.SP("/teach/%s", tc.sess.ID), nil, nil)
	for _, v := range append([]string{"pw-acme-2222", "pty-acme-secret-3333"}, secrets["Password"], secrets["Code"], secrets["Verification"], secrets["Card"], secrets["Card again"]) {
		if strings.Contains(body, v) {
			t.Errorf("the session holds the secret %q", v)
		}
	}
}

// A login during teaching: no password in steps.json, the events or the card; the review
// asks for a vault item, and the saved skill calls fill_login with it (SCN-TCH-003).
func TestSCN_TCH_003_LoginBecomesAVaultLogin(t *testing.T) {
	hosts, _ := fixtureHosts(t)
	t.Setenv("FETTLE_COMPUTER_HOSTS", hosts)
	e := dockerApp(t)
	chief := e.Chief()
	zone := desktopZone(e, "desktop")
	tc := startTeach(e, chief.ID, zone, "Sign in to the acme shop", nil)
	tc.browser(func(ctx context.Context, c *client.Client, id string) {
		if _, err := c.BrowserNavigate(ctx, id, "http://shop.test/login"); err != nil {
			t.Fatal(err)
		}
		act(t, ctx, c, id, "textbox", "User name", "type", web.ShopUser)
		act(t, ctx, c, id, "textbox", "Password", "type", web.ShopPassword)
		act(t, ctx, c, id, "button", "Sign in", "click", "")
	})
	steps := tc.waitSteps(4)
	login := 0
	for _, s := range steps {
		if s.Name == "Password" {
			login = s.Login
			if s.Secret != "password" || s.Value != "" || login == 0 {
				t.Fatalf("password step %+v", s)
			}
		}
	}
	s := tc.finish(map[string]string{"inputs": "Nothing changes.", "success": "The account page shows the user.", "unclear": "Nothing."})
	if !strings.Contains(s.Draft.SkillMD, "fill_login") {
		t.Fatalf("the draft does not sign in with fill_login:\n%s", s.Draft.SkillMD)
	}
	if code, body := e.Do("POST", e.SP("/teach/%s/save", s.ID), map[string]any{}, nil); code != 422 || !strings.Contains(body, "vault item") {
		t.Fatalf("save without a vault item got %d: %s", code, body)
	}
	e.MustDo("POST", e.SP("/vault"), map[string]any{"name": "acme-shop", "kind": "login", "site": "https://shop.test", "username": web.ShopUser, "value": web.ShopPassword}, nil)
	seq := 0
	for _, st := range s.Steps {
		if st.Login == login && seq == 0 {
			seq = st.Seq
		}
	}
	e.MustDo("POST", e.SP("/teach/%s/save", s.ID), map[string]any{"steps": []map[string]any{{"seq": seq, "vault_item": "acme-shop"}}}, &s)
	files := tc.skillFiles(s.SkillID)
	md, sj := string(files["SKILL.md"]), string(files["steps.json"])
	if !strings.Contains(md, "fill_login") || !strings.Contains(md, "acme-shop") || strings.Contains(md, "{{login_") {
		t.Fatalf("SKILL.md does not call fill_login with the item:\n%s", md)
	}
	var parsed struct {
		Steps []struct {
			Kind      string `json:"kind"`
			Tool      string `json:"tool"`
			VaultItem string `json:"vault_item"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(files["steps.json"], &parsed); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, st := range parsed.Steps {
		if st.Kind == "login" && st.Tool == "fill_login" && st.VaultItem == "acme-shop" {
			found = true
		}
	}
	if !found {
		t.Fatalf("steps.json has no login step:\n%s", sj)
	}
	_, audit := e.Do("GET", e.SP("/audit?limit=500"), nil, nil)
	_, session := e.Do("GET", e.SP("/teach/%s", s.ID), nil, nil)
	_, chat := e.Do("GET", e.SP("/chats/%s/messages?limit=200", chief.ChatID), nil, nil)
	for where, text := range map[string]string{"steps.json": sj, "SKILL.md": md, "the audit events": audit, "the session": session, "the chat and its card": chat} {
		if strings.Contains(text, web.ShopPassword) {
			t.Errorf("the password is in %s", where)
		}
	}
}
