package agentd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/netip"
	"sort"
	"strings"
)

// lookupFunc resolves a host name to addresses.
type lookupFunc func(ctx context.Context, host string) ([]netip.Addr, error)

// buildRuleset returns the nftables ruleset for an allowlist policy. The
// ruleset refuses outbound traffic except loopback, established traffic, DNS,
// and TCP 80 and 443 to the addresses of the allowed hosts at boot. A
// wildcard host such as *.example.com allows the addresses of example.com,
// because static rules cannot follow names that are not known at boot. A
// host that does not resolve is left out and logged.
func buildRuleset(ctx context.Context, policy NetworkPolicy, lookup lookupFunc, logger *slog.Logger) (string, error) {
	if policy.Mode != NetworkAllowlist {
		return "", fmt.Errorf("ruleset: mode %q has no rules", policy.Mode)
	}
	v4 := map[netip.Addr]bool{}
	v6 := map[netip.Addr]bool{}
	for _, h := range policy.AllowHosts {
		host := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(strings.ToLower(h)), "*."), ".")
		if host == "" {
			continue
		}
		var addrs []netip.Addr
		if a, err := netip.ParseAddr(host); err == nil {
			addrs = []netip.Addr{a}
		} else {
			addrs, err = lookup(ctx, host)
			if err != nil {
				logger.Warn("network: allowed host does not resolve", "host", h, "error", err.Error())
				continue
			}
		}
		for _, a := range addrs {
			a = a.Unmap()
			if a.Is4() {
				v4[a] = true
			} else {
				v6[a.WithZone("")] = true
			}
		}
	}
	var b strings.Builder
	b.WriteString("table inet fettle\n")
	b.WriteString("delete table inet fettle\n")
	b.WriteString("table inet fettle {\n")
	b.WriteString("\tchain output {\n")
	b.WriteString("\t\ttype filter hook output priority 0; policy drop;\n")
	b.WriteString("\t\toif \"lo\" accept\n")
	b.WriteString("\t\tct state established,related accept\n")
	b.WriteString("\t\tudp dport 53 accept\n")
	b.WriteString("\t\ttcp dport 53 accept\n")
	if len(v4) > 0 {
		fmt.Fprintf(&b, "\t\tip daddr { %s } tcp dport { 80, 443 } accept\n", joinAddrs(v4))
	}
	if len(v6) > 0 {
		fmt.Fprintf(&b, "\t\tip6 daddr { %s } tcp dport { 80, 443 } accept\n", joinAddrs(v6))
	}
	// Refuse the rest at once, so a blocked site fails fast instead of timing out.
	b.WriteString("\t\tmeta l4proto tcp reject with tcp reset\n")
	b.WriteString("\t\treject with icmpx type admin-prohibited\n")
	b.WriteString("\t}\n")
	b.WriteString("}\n")
	return b.String(), nil
}

func joinAddrs(set map[netip.Addr]bool) string {
	list := make([]netip.Addr, 0, len(set))
	for a := range set {
		list = append(list, a)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Less(list[j]) })
	parts := make([]string, len(list))
	for i, a := range list {
		parts[i] = a.String()
	}
	return strings.Join(parts, ", ")
}

func rulesetHash(ruleset string) string {
	sum := sha256.Sum256([]byte(ruleset))
	return hex.EncodeToString(sum[:])
}
