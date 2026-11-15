//go:build e2e

package flows

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/alternayte/fettle/internal/apptest"
	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
)

// rfbGreeting opens a screen stream and returns the first bytes of the VNC server.
func rfbGreeting(e *apptest.Env, token string) (string, int) {
	e.T.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	u := strings.Replace(e.URL, "http://", "ws://", 1) + "/api/v1/screen/stream?t=" + url.QueryEscape(token)
	ws, resp, err := websocket.Dial(ctx, u, &websocket.DialOptions{Subprotocols: []string{"binary"}})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		if resp != nil {
			return "", resp.StatusCode
		}
		e.T.Fatal(err)
	}
	defer func() { _ = ws.CloseNow() }()
	conn := websocket.NetConn(ctx, ws, websocket.MessageBinary)
	buf := make([]byte, 12)
	if _, err := io.ReadFull(conn, buf); err != nil {
		e.T.Fatalf("read the VNC greeting: %v", err)
	}
	return string(buf), http.StatusSwitchingProtocols
}

type screenOut struct {
	ViewToken  string `json:"view_token"`
	Controller string `json:"controller"`
}

func takeoverCard(e *apptest.Env, chatID string) (string, map[string]any) {
	e.T.Helper()
	msgs := e.Messages(chatID)
	for i := len(msgs) - 1; i >= 0; i-- {
		if c, ok := msgs[i]["card"].(map[string]any); ok && c["kind"] == "takeover" {
			return c["id"].(string), c
		}
	}
	e.T.Fatal("no takeover card")
	return "", nil
}

func TestSCN_CMP_015_TakeoverViewControlAndDone(t *testing.T) {
	hosts, _ := fixtureHosts(t)
	t.Setenv("FETTLE_COMPUTER_HOSTS", hosts)
	e := dockerApp(t)
	personal := zoneID(e, "personal")
	e.MustDo("PATCH", e.SP("/zones/%s", personal), map[string]any{"name": "personal", "network": "open", "size": "desktop", "idle_minutes": 30}, nil)
	chief := e.Chief()
	page := "http://shop.test/login"
	marker := "takeover"
	e.Fake.AddScript(fakemodel.Script{Name: marker, Match: fakemodel.Match{System: "[[script:" + marker + "]]"}, Steps: []fakemodel.Step{
		{ToolCalls: call("browser_navigate", map[string]any{"url": page})},
		{ToolCalls: call("request_takeover", map[string]any{"reason": "Solve the check on the Acme sign-in page."})},
		{ToolCalls: call("browser_snapshot", map[string]any{})},
		{Text: "Signed in."},
	}})
	var b map[string]any
	e.MustDo("GET", e.SP("/bots/%s", chief.ID), nil, &b)
	e.MustDo("PATCH", e.SP("/bots/%s", chief.ID), map[string]any{"name": b["name"], "job": b["job"], "instructions": "[[script:" + marker + "]]"}, nil)

	run, _ := e.Post(chief.ChatID, "Sign in to Acme.")
	r := e.WaitRun(run, "waiting")
	if !r.HasEvent("takeover_requested") {
		t.Fatalf("no takeover_requested: %s", r.EventTypes())
	}
	cardID, card := takeoverCard(e, chief.ChatID)
	data, _ := card["data"].(map[string]any)
	if data["bot_id"] != chief.ID || !strings.Contains(card["detail"].(string), "Solve the check") {
		t.Fatalf("card %+v", card)
	}

	// The card opens the screen: a view stream, then control.
	var sc screenOut
	e.MustDo("POST", e.SP("/bots/%s/screen", chief.ID), nil, &sc)
	if g, code := rfbGreeting(e, sc.ViewToken); code != http.StatusSwitchingProtocols || !strings.HasPrefix(g, "RFB 003.") {
		t.Fatalf("view stream: %d %q", code, g)
	}
	var ctl struct {
		Token string `json:"token"`
	}
	e.MustDo("POST", e.SP("/bots/%s/screen/control", chief.ID), nil, &ctl)
	if g, code := rfbGreeting(e, ctl.Token); code != http.StatusSwitchingProtocols || !strings.HasPrefix(g, "RFB 003.") {
		t.Fatalf("control stream: %d %q", code, g)
	}
	e.MustDo("POST", e.SP("/bots/%s/screen", chief.ID), nil, &sc)
	if sc.Controller != "you" {
		t.Fatalf("controller %q", sc.Controller)
	}
	if r := e.GetRun(run); r.Line.State != "waiting" {
		t.Fatalf("the run is %s during control", r.Line.State)
	}

	// Done returns control, answers the card and resumes the run.
	e.MustDo("POST", e.SP("/bots/%s/screen/done", chief.ID), map[string]any{"card_id": cardID, "note": "I solved it."}, nil)
	r = e.WaitRun(run, "done")
	if !r.HasEvent("takeover_done") {
		t.Fatalf("no takeover_done: %s", r.EventTypes())
	}
	if res := toolResults(r, "request_takeover"); len(res) != 1 || !strings.Contains(res[0], "I solved it.") {
		t.Fatalf("request_takeover result %v", res)
	}
	if code, _ := e.Do("POST", e.SP("/bots/%s/screen/done", chief.ID), map[string]any{"card_id": cardID}, nil); code/100 != 2 {
		t.Fatalf("a second Done got %d", code)
	}
	if g, code := rfbGreeting(e, ctl.Token); code != http.StatusUnauthorized {
		t.Fatalf("the control token works after Done: %d %q", code, g)
	}

	// Control without a request: the bot waits until control returns.
	e.MustDo("POST", e.SP("/bots/%s/screen/control", chief.ID), nil, &ctl)
	marker2 := "blocked"
	e.Fake.AddScript(fakemodel.Script{Name: marker2, Match: fakemodel.Match{System: "[[script:" + marker2 + "]]"}, Steps: []fakemodel.Step{
		{ToolCalls: call("browser_snapshot", map[string]any{})},
		{Text: "Read the page."},
	}})
	e.MustDo("PATCH", e.SP("/bots/%s", chief.ID), map[string]any{"name": b["name"], "job": b["job"], "instructions": "[[script:" + marker2 + "]]"}, nil)
	run2, _ := e.Post(chief.ChatID, "Read the page.")
	r = e.WaitRun(run2, "waiting")
	var wait string
	for _, ev := range r.Events {
		if ev.Type == "computer_wait" {
			wait = string(ev.Data)
		}
	}
	if !strings.Contains(wait, "a person controls the screen") || len(toolResults(r, "browser_snapshot")) != 0 {
		t.Fatalf("the bot acted during control: %s %s", r.EventTypes(), wait)
	}
	e.MustDo("POST", e.SP("/bots/%s/screen/done", chief.ID), map[string]any{}, nil)
	r = e.WaitRun(run2, "done")
	if res := toolResults(r, "browser_snapshot"); len(res) != 1 || !strings.Contains(res[0], "Sign in to the shop") {
		t.Fatalf("snapshot after control %v", res)
	}

	// A view token expires after 10 minutes on the test clock.
	e.MustDo("POST", e.SP("/bots/%s/screen", chief.ID), nil, &sc)
	e.App.Screens.SetClock(func() time.Time { return time.Now().Add(10 * time.Minute) })
	if _, code := rfbGreeting(e, sc.ViewToken); code != http.StatusUnauthorized {
		t.Fatalf("a 10-minute-old view token got %d", code)
	}
}
