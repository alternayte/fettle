package tools_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/alternayte/fettle/internal/agent"
	"github.com/alternayte/fettle/internal/envelopes"
	"github.com/alternayte/fettle/internal/policy"
	"github.com/alternayte/fettle/internal/tools"
)

// appendixE is the class of each built-in tool (Appendix E).
var appendixE = map[string]string{
	"exec": envelopes.ClassWriteLocal, "write_file": envelopes.ClassWriteLocal, "edit_file": envelopes.ClassWriteLocal, "delete_file": envelopes.ClassWriteLocal,
	"read_file": envelopes.ClassRead, "list_files": envelopes.ClassRead, "search_files": envelopes.ClassRead,
	"browser_navigate": envelopes.ClassRead, "browser_snapshot": envelopes.ClassRead, "browser_screenshot": envelopes.ClassRead, "browser_downloads": envelopes.ClassRead,
	"fill_login": envelopes.ClassWriteLocal, "totp_code": envelopes.ClassWriteLocal, "use_secret": envelopes.ClassWriteLocal,
	"computer_mode": envelopes.ClassRead, "request_takeover": envelopes.ClassRead, "ask_user": envelopes.ClassRead,
	"remember": envelopes.ClassWriteLocal, "forget": envelopes.ClassWriteLocal,
	"search_history": envelopes.ClassRead, "load_skill": envelopes.ClassRead, "list_bots": envelopes.ClassRead,
	"attach_file": envelopes.ClassRead, "reply": envelopes.ClassRead, "handoff": envelopes.ClassWriteLocal,
	"propose_bot": envelopes.ClassAdmin, "propose_team": envelopes.ClassAdmin, "create_routine": envelopes.ClassAdmin, "update_routine": envelopes.ClassAdmin, "update_self": envelopes.ClassAdmin,
	"sleep_until": envelopes.ClassWriteLocal, "add_open_work": envelopes.ClassWriteLocal, "complete_open_work": envelopes.ClassWriteLocal,
	// report_result is the structured end of a helper run (§9.4, D-151); only helpers get it.
	"report_result": envelopes.ClassRead,
}

// registry holds every built-in tool that this build has.
func registry() *agent.Registry {
	a := agent.New(agent.Agent{})
	c := &tools.Computer{}
	c.Register(a.Tools)
	br := &tools.Browser{Computer: c}
	br.RegisterBrowser(a.Tools)
	(&tools.Broker{Browser: br}).RegisterBroker(a.Tools)
	return a.Tools
}

func TestSCN_POL_001_ToolAndBrowserActionClasses(t *testing.T) {
	r := registry()
	for _, n := range r.Names() {
		tool, _ := r.Get(n)
		if n == "browser_act" {
			continue // its class comes from the action (§10.7), checked below
		}
		want, ok := appendixE[n]
		if !ok {
			t.Errorf("tool %s is not in Appendix E", n)
			continue
		}
		if tool.Class != want {
			t.Errorf("tool %s has class %s, want %s", n, tool.Class, want)
		}
	}
	for _, c := range []struct {
		action, role, name, form string
		submits                  bool
		want                     string
	}{
		{"click", "button", "Send", "Message", false, envelopes.ClassSend},
		{"click", "button", "Delete", "", false, envelopes.ClassWriteExternal},
		{"click", "button", "Next", "", false, envelopes.ClassRead},
		{"click", "link", "Account page", "", false, envelopes.ClassRead},
		{"click", "button", "Save", "Profile", true, envelopes.ClassWriteExternal},
		{"click", "button", "Pay now", "", false, envelopes.ClassSpend},
		{"type", "textbox", "Message text", "Message", false, envelopes.ClassRead},
	} {
		if got := policy.ClassifyBrowserAct(c.action, c.role, c.name, c.form, c.submits); got != c.want {
			t.Errorf("%s on %s %q: class %s, want %s", c.action, c.role, c.name, got, c.want)
		}
	}
}

func TestSCN_AGT_004_ToolSetFollowsCapabilities(t *testing.T) {
	r := registry()
	r.Add(agent.Tool{Name: "post_note", Class: envelopes.ClassSend, Schema: json.RawMessage(`{}`)})
	noSend := []string{envelopes.ClassRead, envelopes.ClassWriteLocal, envelopes.ClassWriteExternal}
	for _, tool := range r.For(noSend, false) {
		if tool.Class == envelopes.ClassSend || tool.Class == envelopes.ClassAdmin || tool.Class == envelopes.ClassSpend {
			t.Errorf("an envelope without send gets %s (%s)", tool.Name, tool.Class)
		}
	}
	all := append(noSend, envelopes.ClassSend, envelopes.ClassAdmin, envelopes.ClassSpend)
	part := r.For(all, true)
	if len(part) == 0 {
		t.Fatal("a fanout part has no read tools")
	}
	for _, tool := range part {
		if tool.Class != envelopes.ClassRead {
			t.Errorf("a fanout part gets %s (%s)", tool.Name, tool.Class)
		}
	}
	// A call whose class comes from the action is checked again (a Send click without send).
	if agent.Allowed(noSend, false, envelopes.ClassSend) || agent.Allowed(all, true, envelopes.ClassWriteExternal) || agent.Allowed(all, false, envelopes.ClassSpend) {
		t.Fatal("a call of an excluded class is allowed")
	}
}

func TestTOTPMatchesRFC6238(t *testing.T) {
	// RFC 6238 test vector: the ASCII seed "12345678901234567890" at 59 s gives 287082 (6 digits).
	seed := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	if got, err := tools.TOTP(seed, time.Unix(59, 0)); err != nil || got != "287082" {
		t.Fatalf("TOTP = %s %v", got, err)
	}
}
