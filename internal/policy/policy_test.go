package policy

import (
	"testing"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/envelopes"
)

func rule(effect, tool, site string) *Rule {
	r := &Rule{Effect: effect, Tool: tool, SitePattern: site}
	r.SetID(uuid.New())
	return r
}

func TestSCN_POL_004_RulePrecedence(t *testing.T) {
	req := Request{Tool: "send_email", Class: envelopes.ClassSend, Recipient: "team@acme.test"}
	allow := rule("allow", "send_email", "")
	ask := rule("ask", "send_email", "")
	block := rule("block", "*", "")
	cases := []struct {
		name  string
		mode  string
		rules []*Rule
		want  string
	}{
		{"allow alone runs in ask mode", ModeAsk, []*Rule{allow}, Run},
		{"ask wins over allow", ModeNever, []*Rule{allow, ask}, Card},
		{"block wins over ask and allow", ModeNever, []*Rule{allow, ask, block}, Block},
		{"block wins in any order", ModeAsk, []*Rule{block, allow}, Block},
		{"no rule in ask mode asks", ModeAsk, nil, Card},
		{"no rule in never mode runs", ModeNever, nil, Run},
	}
	for _, c := range cases {
		d := Decide(c.mode, c.rules, req)
		if d.Effect != c.want {
			t.Fatalf("%s: %s, want %s", c.name, d.Effect, c.want)
		}
	}
	// Recipient and site patterns limit a rule.
	r := &Rule{Effect: "allow", Tool: "send_email", RecipientPattern: "*@acme.test"}
	r.SetID(uuid.New())
	if Decide(ModeAsk, []*Rule{r}, req).Effect != Run {
		t.Fatal("recipient pattern did not match")
	}
	if Decide(ModeAsk, []*Rule{r}, Request{Tool: "send_email", Class: envelopes.ClassSend, Recipient: "x@evil.test"}).Effect != Card {
		t.Fatal("recipient pattern matched another domain")
	}
	s := &Rule{Effect: "block", SitePattern: "*.evil.test"}
	s.SetID(uuid.New())
	if Decide(ModeNever, []*Rule{s}, Request{Tool: "browser_act", Class: envelopes.ClassWriteExternal, Site: "https://www.evil.test/form"}).Effect != Block {
		t.Fatal("site pattern did not block")
	}
	// An approval card from a normal action offers Always allow; a hard ask does not.
	if d := Decide(ModeAsk, nil, req); !d.AlwaysAllowed {
		t.Fatal("a normal card does not offer Always allow")
	}
	if d := Decide(ModeNever, []*Rule{allow}, Request{Tool: "create_zone", Class: envelopes.ClassAdmin, HardAsk: true}); d.Effect != Card || d.AlwaysAllowed {
		t.Fatalf("hard ask: %+v", d)
	}
}

func TestSCN_POL_007_SpendIsAlwaysRefused(t *testing.T) {
	allowAll := rule("allow", "*", "")
	for _, mode := range []string{ModeAsk, ModeAuto, ModeNever} {
		for _, rules := range [][]*Rule{nil, {allowAll}} {
			d := Decide(mode, rules, Request{Tool: "browser_act", Class: envelopes.ClassSpend})
			if d.Effect != Block {
				t.Fatalf("spend in %s with %d rules: %s", mode, len(rules), d.Effect)
			}
		}
	}
	if c := ClassifyBrowserAct("click", "button", "Buy now", "", false); c != envelopes.ClassSpend {
		t.Fatalf("Buy now is %s", c)
	}
}
