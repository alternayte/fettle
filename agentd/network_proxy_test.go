package agentd

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// upstreamProxy is a test forward proxy that needs a sign-in. It records the CONNECT
// targets and answers each tunnel with a fixed banner, and plain requests with their URL.
type upstreamProxy struct {
	mu      sync.Mutex
	targets []string
	auth    string
}

func (u *upstreamProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Proxy-Authorization") != u.auth {
		w.WriteHeader(http.StatusProxyAuthRequired)
		return
	}
	u.mu.Lock()
	u.targets = append(u.targets, r.Host)
	u.mu.Unlock()
	if r.Method != http.MethodConnect {
		_, _ = io.WriteString(w, "plain "+r.URL.String())
		return
	}
	conn, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\n\r\ntunnel to "+r.Host)
}

func (u *upstreamProxy) seen() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.targets...)
}

// connectThrough sends CONNECT target to the proxy at addr and returns the status line and
// the bytes after the answer.
func connectThrough(t *testing.T, addr, target string) (int, string) {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: target}, Host: target, Header: http.Header{}}
	if err := req.Write(c); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	b, _ := io.ReadAll(br)
	return resp.StatusCode, string(b)
}

func TestSCN_CMP_019_FilterProxyChecksTheZoneAndSignsIn(t *testing.T) {
	up := &upstreamProxy{auth: "Basic " + base64.StdEncoding.EncodeToString([]byte("bot@acme.test:acme-proxy-pass"))}
	ups := httptest.NewServer(up)
	t.Cleanup(ups.Close)
	upURL, _ := url.Parse(ups.URL)
	upURL.User = url.User("bot@acme.test")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	start := func(policy NetworkPolicy, password string) string {
		f, err := newFilterProxy(FilterConfig{Upstream: upURL.String(), Password: password, Network: policy}, logger)
		if err != nil {
			t.Fatal(err)
		}
		s := httptest.NewServer(f)
		t.Cleanup(s.Close)
		return strings.TrimPrefix(s.URL, "http://")
	}

	allow := start(NetworkPolicy{Mode: NetworkAllowlist, AllowHosts: []string{"docs.acme.test", "*.shop.acme.test"}}, "acme-proxy-pass")
	for _, target := range []string{"docs.acme.test:443", "shop.acme.test:443", "eu.shop.acme.test:443"} {
		code, body := connectThrough(t, allow, target)
		if code != http.StatusOK || body != "tunnel to "+target {
			t.Fatalf("CONNECT %s: %d %q", target, code, body)
		}
	}
	for _, target := range []string{"evil.acme.test:443", "docs.acme.test.evil.test:443", "notshop.acme.test:443", "docs.acme.test:22"} {
		code, body := connectThrough(t, allow, target)
		if code != http.StatusForbidden || !strings.Contains(body, "is not on the allowlist of this zone.") {
			t.Fatalf("CONNECT %s: %d %q, want a refusal", target, code, body)
		}
	}
	for _, h := range up.seen() {
		if strings.Contains(h, "evil") || strings.Contains(h, "notshop") || strings.HasSuffix(h, ":22") {
			t.Fatalf("a refused host reached the upstream proxy: %s", h)
		}
	}

	// Plain HTTP goes through the same check and carries the sign-in.
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(&url.URL{Scheme: "http", Host: allow})}}
	resp, err := client.Get("http://docs.acme.test/page")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(b) != "plain http://docs.acme.test/page" {
		t.Fatalf("plain allowed: %d %q", resp.StatusCode, b)
	}
	resp, err = client.Get("http://evil.acme.test/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || !strings.Contains(string(b), "evil.acme.test is not on the allowlist") {
		t.Fatalf("plain refused: %d %q", resp.StatusCode, b)
	}

	// An open zone allows every host and port.
	open := start(NetworkPolicy{Mode: NetworkOpen}, "acme-proxy-pass")
	if code, body := connectThrough(t, open, "anything.acme.test:8443"); code != http.StatusOK || body != "tunnel to anything.acme.test:8443" {
		t.Fatalf("open zone: %d %q", code, body)
	}

	// A wrong password gets a message that names the setting, not a bare 407.
	wrong := start(NetworkPolicy{Mode: NetworkOpen}, "wrong")
	if code, body := connectThrough(t, wrong, "docs.acme.test:443"); code != http.StatusBadGateway || !strings.Contains(body, "Settings, Computers") {
		t.Fatalf("wrong password: %d %q", code, body)
	}

	// An upstream proxy that does not answer.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := ln.Addr().String()
	_ = ln.Close()
	f, err := newFilterProxy(FilterConfig{Upstream: "http://" + dead, Network: NetworkPolicy{Mode: NetworkOpen}}, logger)
	if err != nil {
		t.Fatal(err)
	}
	ds := httptest.NewServer(f)
	t.Cleanup(ds.Close)
	if code, body := connectThrough(t, strings.TrimPrefix(ds.URL, "http://"), "docs.acme.test:443"); code != http.StatusBadGateway || !strings.Contains(body, "The proxy did not answer") {
		t.Fatalf("dead upstream: %d %q", code, body)
	}
}

func TestProxyRulesetLetsOnlyTheFilterUserOut(t *testing.T) {
	rs, err := buildProxyRuleset(998, []netip.AddrPort{netip.MustParseAddrPort("192.0.2.10:3128"), netip.MustParseAddrPort("[2001:db8::10]:3128"), netip.MustParseAddrPort("192.0.2.10:3128")})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"policy drop;",
		"oif \"lo\" accept",
		"meta skuid 998 ip daddr 192.0.2.10 tcp dport 3128 accept",
		"meta skuid 998 ip6 daddr 2001:db8::10 tcp dport 3128 accept",
		"meta skuid 998 udp dport 53 accept",
	} {
		if !strings.Contains(rs, want) {
			t.Fatalf("ruleset lacks %q:\n%s", want, rs)
		}
	}
	if strings.Count(rs, "192.0.2.10") != 1 || strings.Contains(rs, "dport { 80, 443 }") {
		t.Fatalf("ruleset:\n%s", rs)
	}
	if _, err := buildProxyRuleset(998, nil); err == nil {
		t.Fatal("a proxy without an address gives a ruleset")
	}
}

func testCA(t *testing.T, cn string) []byte {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: cn}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestSCN_CMP_020_ExtraRootsGoIntoTheSystemStoreAndChromium(t *testing.T) {
	root := t.TempDir()
	bundle := append(testCA(t, "Acme Inspection Root"), testCA(t, "Acme Second Root")...)
	var ran []string
	if err := installRoots(root, bundle, func(name string, args ...string) error {
		ran = append(ran, name)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(ran) != 1 || ran[0] != "update-ca-certificates" {
		t.Fatalf("commands: %v", ran)
	}
	for _, n := range []string{"fettle-01.crt", "fettle-02.crt"} {
		b, err := os.ReadFile(filepath.Join(root, "usr/local/share/ca-certificates", n))
		if err != nil || !strings.Contains(string(b), "BEGIN CERTIFICATE") {
			t.Fatalf("%s: %v", n, err)
		}
	}
	b, err := os.ReadFile(filepath.Join(root, "etc/chromium/policies/managed/fettle.json"))
	if err != nil {
		t.Fatal(err)
	}
	var policy struct {
		CACertificates []string `json:"CACertificates"`
	}
	if err := json.Unmarshal(b, &policy); err != nil || len(policy.CACertificates) != 2 {
		t.Fatalf("policy %s: %v", b, err)
	}
	der, _ := base64.StdEncoding.DecodeString(policy.CACertificates[0])
	if c, err := x509.ParseCertificate(der); err != nil || c.Subject.CommonName != "Acme Inspection Root" {
		t.Fatalf("policy certificate: %v", err)
	}
	if env := rootsEnv(); env["SSL_CERT_FILE"] != caBundle || env["NODE_EXTRA_CA_CERTS"] != caBundle || env["REQUESTS_CA_BUNDLE"] != caBundle || env["UV_SYSTEM_CERTS"] != "1" {
		t.Fatalf("env %v", env)
	}

	// A wrong value fails at boot, and a failing command stops the boot.
	if err := installRoots(t.TempDir(), []byte("not a certificate"), nil); err == nil {
		t.Fatal("text without a certificate installs")
	}
	if err := installRoots(t.TempDir(), bundle, func(string, ...string) error { return errors.New("no store") }); err == nil {
		t.Fatal("a failed update-ca-certificates passes")
	}
}

func TestSCN_CMP_021_BrowserNetworkErrorsInPlainWords(t *testing.T) {
	allow := NetworkPolicy{Mode: NetworkAllowlist, AllowHosts: []string{"docs.acme.test"}}
	cases := []struct {
		raw, host string
		policy    NetworkPolicy
		want      string
	}{
		{"page load error net::ERR_CERT_AUTHORITY_INVALID", "docs.acme.test", NetworkPolicy{}, "This network inspects HTTPS. Add your company root certificate in Settings, Computers. (net::ERR_CERT_AUTHORITY_INVALID)"},
		{"page load error net::ERR_CERT_COMMON_NAME_INVALID", "", NetworkPolicy{}, "This network inspects HTTPS."},
		{"page load error net::ERR_NAME_NOT_RESOLVED", "shop.acme.test", NetworkPolicy{}, "The computer could not look up shop.acme.test. (net::ERR_NAME_NOT_RESOLVED)"},
		{"page load error net::ERR_CONNECTION_REFUSED", "evil.acme.test", allow, "evil.acme.test is not on the allowlist of this zone."},
		{"page load error net::ERR_CONNECTION_RESET", "evil.acme.test", allow, "evil.acme.test is not on the allowlist of this zone."},
		{"page load error net::ERR_TUNNEL_CONNECTION_FAILED", "evil.acme.test", allow, "evil.acme.test is not on the allowlist of this zone."},
		{"page load error net::ERR_TUNNEL_CONNECTION_FAILED", "docs.acme.test", allow, "The proxy did not answer."},
		{"page load error net::ERR_PROXY_CONNECTION_FAILED", "docs.acme.test", NetworkPolicy{}, "The proxy did not answer. (net::ERR_PROXY_CONNECTION_FAILED)"},
		{"page load error net::ERR_CONNECTION_REFUSED", "docs.acme.test", NetworkPolicy{Mode: NetworkOpen}, "page load error net::ERR_CONNECTION_REFUSED"},
		{"page load error net::ERR_ABORTED", "docs.acme.test", NetworkPolicy{}, "page load error net::ERR_ABORTED"},
	}
	for _, c := range cases {
		got := cleanNav(errors.New(c.raw), c.host, c.policy)
		if !strings.HasPrefix(got, c.want) {
			t.Errorf("%s on %s: %q, want %q", c.raw, c.host, got, c.want)
		}
	}
}

func TestExecHintForCertificateErrors(t *testing.T) {
	o := &execSink{}
	o.noteTLS([]byte("curl: (60) SSL certificate pro"))
	o.noteTLS([]byte("blem: unable to get local issuer certificate\n"))
	if !o.certErr {
		t.Fatal("a certificate error split over two chunks is not seen")
	}
	other := &execSink{}
	other.noteTLS([]byte("curl: (6) Could not resolve host: docs.acme.test\n"))
	if other.certErr {
		t.Fatal("a DNS error reads as a certificate error")
	}
}
