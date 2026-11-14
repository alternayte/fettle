package agentd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"strings"
	"testing"
)

func TestExcluderDefaultsAndPatterns(t *testing.T) {
	ex, err := newExcluder([]string{"tmp", "data/raw", "*.bak"})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		"notes.md":                                     false,
		"node_modules":                                 true,
		"web/node_modules/pkg/index.js":                true,
		"a/.cache/x":                                   true,
		".venv/bin/python":                             true,
		"src/__pycache__/m.pyc":                        true,
		"run.log":                                      true,
		"logs/run.log.txt":                             false,
		".browser/Default/Cache":                       true,
		".browser/Default/Code Cache/js":               true,
		".browser/Default/GPUCache/index":              true,
		".browser/Default/Service Worker/CacheStorage": true,
		".browser/Default/Service Worker/ScriptCache":  false,
		".browser/Default/Preferences":                 false,
		"Cache/outside-browser":                        false,
		"tmp/x":                                        true,
		"data/raw/x.csv":                               true,
		"data/clean/x.csv":                             false,
		"old.bak":                                      true,
	}
	for p, want := range cases {
		if got := ex.excluded(p); got != want {
			t.Errorf("excluded(%q) = %v, want %v", p, got, want)
		}
	}
	if _, err := newExcluder([]string{"[bad"}); err == nil {
		t.Fatal("bad pattern: want error")
	}
}

func TestRulesetAllowlist(t *testing.T) {
	lookup := func(_ context.Context, host string) ([]netip.Addr, error) {
		switch host {
		case "docs.test":
			return []netip.Addr{netip.MustParseAddr("192.0.2.10"), netip.MustParseAddr("2001:db8::10")}, nil
		case "example.com":
			return []netip.Addr{netip.MustParseAddr("::ffff:198.51.100.7")}, nil
		}
		return nil, errors.New("no such host")
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	policy := NetworkPolicy{Mode: NetworkAllowlist, AllowHosts: []string{"docs.test", "*.example.com", "missing.test", "203.0.113.5"}}
	rs, err := buildRuleset(context.Background(), policy, lookup, logger)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"policy drop;",
		`oif "lo" accept`,
		"ct state established,related accept",
		"udp dport 53 accept",
		"ip daddr { 192.0.2.10, 198.51.100.7, 203.0.113.5 } tcp dport { 80, 443 } accept",
		"ip6 daddr { 2001:db8::10 } tcp dport { 80, 443 } accept",
	} {
		if !strings.Contains(rs, want) {
			t.Errorf("ruleset lacks %q:\n%s", want, rs)
		}
	}
	again, err := buildRuleset(context.Background(), policy, lookup, logger)
	if err != nil || rulesetHash(again) != rulesetHash(rs) {
		t.Fatal("ruleset is not stable")
	}
	empty, err := buildRuleset(context.Background(), NetworkPolicy{Mode: NetworkAllowlist}, lookup, logger)
	if err != nil || strings.Contains(empty, "daddr") || !strings.Contains(empty, "policy drop;") {
		t.Fatalf("empty allowlist: %v\n%s", err, empty)
	}
}
