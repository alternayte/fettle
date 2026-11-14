//go:build e2e

package flows

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alternayte/fettle/internal/apptest"
	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
	"github.com/alternayte/fettle/internal/fakes/web"
)

// pendingCard returns the card of a change reply from the chat.
func (s *server) changeCard(chatID, cardID string) map[string]any {
	s.T.Helper()
	for _, m := range s.Messages(chatID) {
		if c, ok := m["card"].(map[string]any); ok && c["id"] == cardID {
			return c
		}
	}
	s.T.Fatalf("card %s is not in chat %s", cardID, chatID)
	return nil
}

type cardReply struct {
	CardID string `json:"card_id"`
	ChatID string `json:"chat_id"`
}

func TestSCN_POL_003_HardAsksAlwaysShowCards(t *testing.T) {
	s := startServer(t, "sqlite")
	// Never mode (through its own card) and a rule that allows every tool.
	var mode cardReply
	s.MustDo("PUT", s.SP("/approval-mode"), map[string]any{"mode": "never"}, &mode)
	s.MustDo("POST", s.SP("/cards/%s/answer", mode.CardID), map[string]any{"action": "allow"}, nil)
	var pol struct {
		Mode string `json:"mode"`
	}
	s.MustDo("GET", s.SP("/policy"), nil, &pol)
	if pol.Mode != "never" {
		t.Fatalf("mode %q after the card", pol.Mode)
	}
	s.MustDo("POST", s.SP("/policy/rules"), map[string]any{"tool": "*", "effect": "allow"}, nil)
	if code, _ := s.Do("PUT", s.SP("/settings/approval.mode"), map[string]any{"value": "ask"}, nil); code != 422 {
		t.Fatalf("the setting route changed the approval mode without a card: %d", code)
	}

	var item struct {
		ID string `json:"id"`
	}
	s.MustDo("POST", s.SP("/vault"), map[string]any{"name": "shop", "kind": "login", "site": "https://shop.test", "username": web.ShopUser, "value": web.ShopPassword}, &item)
	var zs struct {
		Zones []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"zones"`
	}
	s.MustDo("GET", s.SP("/zones"), nil, &zs)
	personal := ""
	for _, z := range zs.Zones {
		if z.Name == "personal" {
			personal = z.ID
		}
	}

	asks := map[string]func() cardReply{
		"create a zone": func() cardReply {
			var r cardReply
			s.MustDo("POST", s.SP("/zones"), map[string]any{"name": "work", "network": "open", "size": "browser"}, &r)
			return r
		},
		"add a grant": func() cardReply {
			var r cardReply
			s.MustDo("POST", s.SP("/zones/%s/grants", personal), map[string]any{"kind": "vault_item", "target_id": item.ID}, &r)
			return r
		},
		"enable host": func() cardReply {
			var r cardReply
			s.MustDo("POST", s.SP("/computer-providers/host/enable"), nil, &r)
			return r
		},
	}
	for name, ask := range asks {
		r := ask()
		c := s.changeCard(r.ChatID, r.CardID)
		if c["status"] != "pending" || c["hard_ask"] != true {
			t.Fatalf("%s: card %v is not a pending hard ask", name, c)
		}
		if code, _ := s.Do("POST", s.SP("/cards/%s/answer", r.CardID), map[string]any{"action": "always"}, nil); code/100 == 2 {
			t.Fatalf("%s: the card accepted Always allow", name)
		}
	}
	// Nothing changed before a person allowed the cards.
	s.MustDo("GET", s.SP("/zones"), nil, &zs)
	if len(zs.Zones) != 2 {
		t.Fatalf("zones %d before the card was allowed", len(zs.Zones))
	}
}

func TestSCN_CMP_016_EnableHostNeedsHardAskCard(t *testing.T) {
	s := startServer(t, "sqlite")
	provs := func() map[string]bool {
		var out struct {
			Providers []struct {
				Kind    string `json:"kind"`
				Enabled bool   `json:"enabled"`
			} `json:"providers"`
		}
		s.MustDo("GET", s.SP("/computer-providers"), nil, &out)
		m := map[string]bool{}
		for _, p := range out.Providers {
			m[p.Kind] = p.Enabled
		}
		return m
	}
	if on, ok := provs()["host"]; !ok || on {
		t.Fatalf("host provider: listed %t, on %t; want listed and off", ok, on)
	}
	var r cardReply
	s.MustDo("POST", s.SP("/computer-providers/host/enable"), nil, &r)
	if provs()["host"] {
		t.Fatal("host is on before the card was allowed")
	}
	s.MustDo("POST", s.SP("/cards/%s/answer", r.CardID), map[string]any{"action": "allow"}, nil)
	if !provs()["host"] {
		t.Fatal("host is off after the card was allowed")
	}
}

func TestSCN_CMP_011_ToolsRunAsFettleWithoutRoot(t *testing.T) {
	s := startComputers(t, "sqlite")
	out := s.runTool("exec", map[string]any{"command": "id -un; command -v sudo || echo no-sudo; touch /etc/fettle-probe 2>&1 || echo etc-refused; nft flush ruleset 2>&1; echo nft-exit=$?"})
	for _, want := range []string{"fettle\n", "no-sudo", "etc-refused"} {
		if !strings.Contains(out, want) {
			t.Fatalf("exec output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "nft-exit=0") {
		t.Fatalf("nftables rules changed from exec:\n%s", out)
	}
}

func TestSCN_AGT_002_LargeOutputGoesToHome(t *testing.T) {
	s := startComputers(t, "sqlite")
	out := s.runTool("exec", map[string]any{"command": "python3 -c \"print('A'*1024 + 'B'*28672 + 'Z'*1024, end='')\""})
	if !strings.Contains(out, "The output has 30720 bytes") || !strings.Contains(out, ".fettle/outputs/") {
		t.Fatalf("no offload note:\n%.400s", out)
	}
	if strings.Count(out, "B") > 2100 || !strings.Contains(out, strings.Repeat("A", 1024)) || !strings.Contains(out, strings.Repeat("Z", 1024)) {
		t.Fatalf("context does not hold only 1 KB head and tail (%d bytes)", len(out))
	}
	_, path, _ := strings.Cut(out, ".fettle/outputs/")
	path, _, _ = strings.Cut(path, ".txt")
	path = ".fettle/outputs/" + path + ".txt"
	size := s.runTool("exec", map[string]any{"command": "wc -c < " + path})
	if !strings.Contains(size, "30720") {
		t.Fatalf("the output file %s does not hold the full output: %s", path, size)
	}
}

func TestSCN_CMP_013_AllowlistLimitsExecAndBrowser(t *testing.T) {
	s := startComputers(t, "sqlite")
	var zs struct {
		Zones []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"zones"`
	}
	s.MustDo("GET", s.SP("/zones"), nil, &zs)
	personal := ""
	for _, z := range zs.Zones {
		if z.Name == "personal" {
			personal = z.ID
		}
	}
	s.MustDo("PATCH", s.SP("/zones/%s", personal), map[string]any{"name": "personal", "network": "allowlist", "allow_hosts": []string{"docs.test"}, "size": "browser"}, nil)

	out := s.runTool("exec", map[string]any{"command": "curl -s -m 5 http://docs.test/alpha; echo; curl -s -m 5 -o /dev/null -w 'shop=%{http_code}' http://shop.test/login || echo shop-refused"})
	if !strings.Contains(out, "bamboo top") || !strings.Contains(out, "shop-refused") {
		t.Fatalf("exec allowlist:\n%s", out)
	}
	page := s.runTool("browser_navigate", map[string]any{"url": "http://docs.test/beta"})
	if !strings.Contains(page, "steel frame") {
		t.Fatalf("browser did not reach docs.test:\n%s", page)
	}
	shop := s.runTool("browser_navigate", map[string]any{"url": "http://shop.test/login"})
	if strings.Contains(shop, "Sign in to the shop") {
		t.Fatalf("browser reached shop.test through the allowlist:\n%s", shop)
	}

	// The zone form refuses providers without allowlists for this zone.
	var provs struct {
		Providers []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"providers"`
	}
	s.MustDo("GET", s.SP("/computer-providers"), nil, &provs)
	for _, p := range provs.Providers {
		if p.Kind != "host" {
			continue
		}
		code, body := s.Do("PATCH", s.SP("/zones/%s", personal), map[string]any{"name": "personal", "provider_id": p.ID, "network": "allowlist", "allow_hosts": []string{"docs.test"}, "size": "browser"}, nil)
		if code != 422 {
			t.Fatalf("zone with allowlist accepted %s: %d %s", p.Kind, code, body)
		}
	}
}

func TestSCN_MOD_006_ComputerModeForOwnerWithoutVision(t *testing.T) {
	s := startComputers(t, "sqlite")
	// Owner without vision; computer_use with vision.
	var p struct {
		ID string `json:"id"`
	}
	s.MustDo("POST", s.SP("/model-providers"), map[string]any{"name": "blind", "api_type": "openai-chat", "base_url": s.FakeURL + "/mode/novision/v1", "auth_style": "bearer", "key": "sk-fake-blind-1234"}, &p)
	s.MustDo("POST", s.SP("/model-providers/%s/test", p.ID), map[string]any{"model": "fake/large"}, nil)
	s.MustDo("PUT", s.SP("/model-roles/owner"), map[string]any{"targets": []map[string]any{{"provider_id": p.ID, "model": "fake/large", "price_in": 1.0, "price_out": 1.0, "context_window": 128000}}}, nil)
	var seeing struct {
		ID string `json:"id"`
	}
	s.MustDo("POST", s.SP("/model-providers"), map[string]any{"name": "seeing", "api_type": "anthropic-messages", "base_url": s.FakeURL + "/v1", "auth_style": "x_api_key", "key": "sk-fake-see-5678"}, &seeing)
	s.MustDo("POST", s.SP("/model-providers/%s/test", seeing.ID), map[string]any{"model": "fake/vision"}, nil)
	s.MustDo("PUT", s.SP("/model-roles/computer_use"), map[string]any{"targets": []map[string]any{{"provider_id": seeing.ID, "model": "fake/vision", "price_in": 1.0, "price_out": 1.0, "context_window": 128000}}}, nil)

	s.Fake.AddScript(fakemodel.Script{Name: "screen-answer", Match: fakemodel.Match{System: "You look at a screenshot"}, Steps: []fakemodel.Step{{Text: "The page shows the Gamma Desk heading and its price."}}})
	s.runTool("browser_navigate", map[string]any{"url": "http://docs.test/gamma"})
	shot := s.runTool("browser_screenshot", map[string]any{})
	if !strings.Contains(shot, "cannot see images") {
		t.Fatalf("browser_screenshot sent an image to a model without vision: %s", shot)
	}
	before := time.Now()
	answer := s.runTool("computer_mode", map[string]any{"question": "What is the heading?"})
	if !strings.Contains(answer, "Gamma Desk heading") {
		t.Fatalf("computer_mode answer: %s", answer)
	}
	sawImage := false
	for _, r := range s.Fake.Requests() {
		if r.At.After(before) && strings.Contains(r.System, "You look at a screenshot") {
			sawImage = r.Images == 1 && r.API == "anthropic-messages"
		}
		if r.At.After(before) && r.Images > 0 && !strings.Contains(r.System, "You look at a screenshot") {
			t.Fatalf("an owner request got an image: %+v", r.API)
		}
	}
	if !sawImage {
		t.Fatal("the computer_use model got no screenshot")
	}
}

// neverMode turns on Never mode through its card.
func (s *server) neverMode() {
	s.T.Helper()
	var r cardReply
	s.MustDo("PUT", s.SP("/approval-mode"), map[string]any{"mode": "never"}, &r)
	s.MustDo("POST", s.SP("/cards/%s/answer", r.CardID), map[string]any{"action": "allow"}, nil)
}

// runSteps runs one scripted run of Chief and returns the run.
func (s *server) runSteps(steps ...fakemodel.Step) apptest.Run {
	s.T.Helper()
	marker := fmt.Sprintf("steps-%d", time.Now().UnixNano())
	s.script(marker, append(steps, fakemodel.Step{Text: "Done."})...)
	chief := s.Chief()
	s.instruct(chief.ID, marker)
	run, _ := s.Post(chief.ChatID, "Please do the steps.")
	return s.WaitRun(run, "done", "failed", "escalated", "waiting")
}

func TestSCN_CMP_014_BrowserMasksRevalidatesAndNeverAcceptsDialogs(t *testing.T) {
	s := startComputers(t, "sqlite")
	s.neverMode()

	r := s.runSteps(
		fakemodel.Step{ToolCalls: call("browser_navigate", map[string]any{"url": "http://shop.test/login"})},
		fakemodel.Step{ToolCalls: call("browser_act", map[string]any{"ref": "e2", "action": "type", "value": web.ShopPassword})},
		fakemodel.Step{ToolCalls: call("browser_snapshot", map[string]any{})},
	)
	snaps := toolResults(r, "browser_snapshot")
	if len(snaps) != 1 || !strings.Contains(snaps[0], `e2 textbox "Password" [masked]`) || strings.Contains(snaps[0], web.ShopPassword) {
		t.Fatalf("the password field is not masked:\n%v", snaps)
	}

	r = s.runSteps(
		fakemodel.Step{ToolCalls: call("browser_navigate", map[string]any{"url": "http://shop.test/changing?after=800"})},
		fakemodel.Step{DelayMS: 1500, ToolCalls: call("browser_act", map[string]any{"ref": "e1", "action": "click"})},
	)
	acts := toolResults(r, "browser_act")
	if len(acts) != 1 || !strings.Contains(acts[0], "Refused: The element e1 changed") {
		t.Fatalf("an act on a changed element was not refused:\n%v", acts)
	}

	r = s.runSteps(
		fakemodel.Step{ToolCalls: call("browser_navigate", map[string]any{"url": "http://shop.test/actions"})},
		fakemodel.Step{ToolCalls: call("browser_act", map[string]any{"ref": "e3", "action": "click"})},
	)
	nav := toolResults(r, "browser_navigate")
	if len(nav) != 1 || !strings.Contains(nav[0], `"Card number" [masked]`) || strings.Contains(nav[0], "4111") {
		t.Fatalf("the card field is not masked:\n%v", nav)
	}
	acts = toolResults(r, "browser_act")
	if len(acts) != 1 || !strings.Contains(acts[0], "Fettle did not accept it") || !strings.Contains(acts[0], "kept") {
		t.Fatalf("the confirm dialog was accepted or not reported:\n%v", acts)
	}

	r = s.runSteps(fakemodel.Step{ToolCalls: call("browser_act", map[string]any{"ref": "e6", "action": "click"})})
	acts = toolResults(r, "browser_act")
	if len(acts) != 1 || !strings.Contains(acts[0], "Confirmed: Did click on e6") || !strings.Contains(acts[0], "Uncertain: The page did not finish loading") {
		t.Fatalf("a click whose page does not load does not report confirmed and uncertain parts:\n%v", acts)
	}
}
