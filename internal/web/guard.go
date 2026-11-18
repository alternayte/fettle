package web

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// Policy is the network policy of the zone of a bot. The zero value is the open policy.
type Policy struct {
	Allowlist bool
	Hosts     []string
}

// Allows reports whether the policy lets a request reach host. It matches the rules of
// agentd: "*.x" allows x and its subdomains, and an entry can be an IP address.
func (p Policy) Allows(host string) bool {
	if !p.Allowlist {
		return true
	}
	host = normHost(host)
	hostIP, hostIsIP := parseIP(host)
	for _, h := range p.Hosts {
		h = normHost(h)
		if h == "" {
			continue
		}
		if base, ok := strings.CutPrefix(h, "*."); ok {
			if host == base || strings.HasSuffix(host, "."+base) {
				return true
			}
			continue
		}
		if ip, ok := parseIP(h); ok {
			if hostIsIP && ip == hostIP {
				return true
			}
			continue
		}
		if host == h {
			return true
		}
	}
	return false
}

func normHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	h = strings.TrimSuffix(h, ".")
	return strings.TrimSuffix(strings.TrimPrefix(h, "["), "]")
}

func parseIP(h string) (netip.Addr, bool) {
	a, err := netip.ParseAddr(h)
	if err != nil {
		return netip.Addr{}, false
	}
	return a.Unmap().WithZone(""), true
}

var refusedPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8",     // this network
		"100.64.0.0/10", // carrier-grade NAT, which also holds some cloud metadata addresses
		"192.0.0.0/24",  // IETF protocol assignments
		"198.18.0.0/15", // benchmark networks
		"240.0.0.0/4",   // reserved, and the broadcast address
		"fec0::/10",     // old site-local addresses
		"100::/64",      // discard
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// refusedAddr reports whether web_fetch must not reach an address: loopback, private,
// link-local (with the cloud metadata address 169.254.169.254), CGNAT, unspecified and
// other special addresses.
func refusedAddr(a netip.Addr) bool {
	a = a.Unmap().WithZone("")
	if a.Is6() {
		// NAT64 and 6to4 addresses carry an IPv4 address.
		if p := netip.MustParsePrefix("64:ff9b::/96"); p.Contains(a) {
			b := a.As16()
			return refusedAddr(netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}))
		}
		if p := netip.MustParsePrefix("2002::/16"); p.Contains(a) {
			b := a.As16()
			return refusedAddr(netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}))
		}
	}
	if !a.IsValid() || a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsInterfaceLocalMulticast() || a.IsMulticast() || a.IsUnspecified() {
		return true
	}
	for _, p := range refusedPrefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// RefusedError is a request that the web rules refuse. Its message names the host.
type RefusedError struct {
	Host   string
	Reason string
}

func (e *RefusedError) Error() string { return "web_fetch refused " + e.Host + ": " + e.Reason }

func offAllowlist(host string) error {
	return &RefusedError{Host: host, Reason: "the host is not on the allowlist of this bot's zone."}
}

func privateAddr(host string, a netip.Addr) error {
	if h, ok := parseIP(host); ok && h == a.Unmap().WithZone("") {
		return &RefusedError{Host: host, Reason: "it is a private or local address. web_fetch does not read private networks."}
	}
	return &RefusedError{Host: host, Reason: "it resolves to " + a.String() + ", a private or local address. web_fetch does not read private networks."}
}

// HostsEnv names the test-only dial override: "host=ip:port,host=ip:port". A mapped host
// dials that address, and only a mapped host can reach a loopback address. Tests and
// `fettle fake serve` use it for the .test fixture hosts.
const HostsEnv = "FETTLE_WEB_HOSTS"

func hostOverrides() map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(os.Getenv(HostsEnv), ",") {
		h, addr, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && h != "" && addr != "" {
			out[normHost(h)] = strings.TrimSpace(addr)
		}
	}
	return out
}

var dialer = &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}

// currentTransport is a transport that swaps its *http.Transport at run time, such as the
// transport of the server client when the proxy or root settings change.
type currentTransport interface {
	Current() *http.Transport
}

// baseTransport returns the transport of the server client as it is now, or nil when it is
// neither an *http.Transport nor a currentTransport, and so its dial cannot change.
func baseTransport(base *http.Client) *http.Transport {
	if base == nil || base.Transport == nil {
		t, _ := http.DefaultTransport.(*http.Transport)
		return t
	}
	if c, ok := base.Transport.(currentTransport); ok {
		return c.Current()
	}
	t, _ := base.Transport.(*http.Transport)
	return t
}

// Client returns the client of web_search: the server client with the test-only dial
// override. Search providers can be on a private network, such as a SearXNG server.
func Client(base *http.Client) *http.Client {
	over := hostOverrides()
	tr := baseTransport(base)
	if len(over) == 0 || tr == nil {
		if base == nil {
			return http.DefaultClient
		}
		return base
	}
	t := tr.Clone()
	next := t.DialContext
	if next == nil {
		next = dialer.DialContext
	}
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if host, _, err := net.SplitHostPort(addr); err == nil {
			if to, ok := over[normHost(host)]; ok {
				return dialer.DialContext(ctx, network, to)
			}
		}
		return next(ctx, network, addr)
	}
	c := *base
	c.Transport = t
	return &c
}

// guard is the transport of web_fetch. It checks the zone policy and the address rules for
// each request, also each redirect, before the request leaves.
type guard struct {
	next   http.RoundTripper
	policy Policy
	over   map[string]string
	// proxy is the proxy function of the server transport; nil when it is unknown.
	proxy func(*http.Request) (*url.URL, error)
	// dialChecks is true when the dial checks the resolved addresses itself.
	dialChecks bool
	resolver   *net.Resolver
}

// check refuses a host that is off the allowlist, or an IP literal with a refused address.
func (g *guard) check(u *url.URL) error {
	host := normHost(u.Hostname())
	if host == "" {
		return fmt.Errorf("the URL %q has no host", u.String())
	}
	if !g.policy.Allows(host) {
		return offAllowlist(host)
	}
	if _, ok := g.over[host]; ok {
		return nil
	}
	if ip, ok := parseIP(host); ok && refusedAddr(ip) {
		return privateAddr(host, ip)
	}
	return nil
}

func (g *guard) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := g.check(req.URL); err != nil {
		return nil, err
	}
	host := normHost(req.URL.Hostname())
	viaProxy := true
	if g.proxy != nil {
		pu, err := g.proxy(req)
		if err != nil {
			return nil, err
		}
		viaProxy = pu != nil
	}
	// Through a proxy the dial goes to the proxy, so resolve the target here and check it.
	if (viaProxy || !g.dialChecks) && g.over[host] == "" {
		if _, isIP := parseIP(host); !isIP {
			addrs, err := g.resolver.LookupNetIP(req.Context(), "ip", host)
			if err != nil {
				return nil, fmt.Errorf("web_fetch cannot resolve %s: %w", host, err)
			}
			for _, a := range addrs {
				if refusedAddr(a) {
					return nil, privateAddr(host, a)
				}
			}
		}
	}
	return g.next.RoundTrip(req)
}

// proxyAddrs records the addresses of the proxies that the transport chose, so that the
// guarded dial can reach a proxy on a private network.
type proxyAddrs struct {
	mu sync.Mutex
	m  map[string]bool
}

func (p *proxyAddrs) add(u *url.URL) {
	if u == nil {
		return
	}
	host, port := u.Hostname(), u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443", "socks5": "1080", "socks5h": "1080"}[u.Scheme]
	}
	p.mu.Lock()
	p.m[net.JoinHostPort(normHost(host), port)] = true
	p.mu.Unlock()
}

func (p *proxyAddrs) has(addr string) bool {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.m[net.JoinHostPort(normHost(host), port)]
}

// maxRedirects is the most redirects that web_fetch follows.
const maxRedirects = 5

// fetchClient returns the client of web_fetch: the server client (proxy and roots) with a
// dial that refuses private and local addresses after DNS resolution, the zone policy on
// each request, and at most 5 redirects.
func fetchClient(base *http.Client, policy Policy) *http.Client {
	over := hostOverrides()
	g := &guard{policy: policy, over: over, resolver: net.DefaultResolver}
	if tr := baseTransport(base); tr != nil {
		t := tr.Clone()
		proxies := &proxyAddrs{m: map[string]bool{}}
		if tr.Proxy != nil {
			g.proxy = tr.Proxy
			t.Proxy = func(r *http.Request) (*url.URL, error) {
				u, err := tr.Proxy(r)
				proxies.add(u)
				return u, err
			}
		} else {
			g.proxy = func(*http.Request) (*url.URL, error) { return nil, nil }
		}
		t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			host = normHost(host)
			if to, ok := over[host]; ok {
				return dialer.DialContext(ctx, network, to)
			}
			if proxies.has(addr) {
				return dialer.DialContext(ctx, network, addr)
			}
			addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, fmt.Errorf("web_fetch cannot resolve %s: %w", host, err)
			}
			for _, a := range addrs {
				if refusedAddr(a) {
					return nil, privateAddr(host, a)
				}
			}
			var last error
			for _, a := range addrs {
				c, err := dialer.DialContext(ctx, network, net.JoinHostPort(a.Unmap().String(), port))
				if err == nil {
					return c, nil
				}
				last = err
			}
			if last == nil {
				last = fmt.Errorf("%s has no address", host)
			}
			return nil, last
		}
		t.DialTLSContext = nil
		g.next, g.dialChecks = t, true
	} else {
		g.next = base.Transport
	}
	c := &http.Client{Transport: g, Timeout: 60 * time.Second}
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > maxRedirects {
			return fmt.Errorf("web_fetch stopped at %s after %d redirects", normHost(req.URL.Hostname()), maxRedirects)
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return fmt.Errorf("web_fetch refused a redirect to %s: it reads http and https URLs only", req.URL.Scheme)
		}
		return g.check(req.URL)
	}
	return c
}
