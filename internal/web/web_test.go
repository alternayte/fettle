package web_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	fakeweb "github.com/alternayte/fettle/internal/fakes/web"
	"github.com/alternayte/fettle/internal/web"
)

// fixtures runs the fixture web server and maps its hosts through the dial override.
func fixtures(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(fakeweb.New())
	t.Cleanup(srv.Close)
	addr := srv.Listener.Addr().String()
	t.Setenv(web.HostsEnv, "docs.test="+addr+",evil.test="+addr+",search.test="+addr)
	return srv
}

func TestWebFetchReadsHTMLAndPDF(t *testing.T) {
	fixtures(t)
	ctx := context.Background()
	text, err := web.Fetch(ctx, http.DefaultClient, web.Policy{}, "http://docs.test/beta")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "steel frame and two motors") || !strings.Contains(text, "Source: http://docs.test/beta") {
		t.Fatalf("HTML text %q", text)
	}
	text, err = web.Fetch(ctx, http.DefaultClient, web.Policy{}, "http://docs.test/manual.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "The Alpha Desk carries 80 kg.") {
		t.Fatalf("PDF text %q", text)
	}
}

func TestWebFetchRefusesPrivateAndOffAllowlistHosts(t *testing.T) {
	srv := fixtures(t)
	ctx := context.Background()
	port := srv.Listener.Addr().String()[strings.LastIndex(srv.Listener.Addr().String(), ":"):]
	allow := web.Policy{Allowlist: true, Hosts: []string{"docs.test"}}
	for _, c := range []struct {
		name, url, host string
		policy          web.Policy
	}{
		{"loopback IP", "http://127.0.0.1" + port + "/alpha", "127.0.0.1", web.Policy{}},
		{"name of a loopback address", "http://localhost" + port + "/alpha", "localhost", web.Policy{}},
		{"private IP", "http://10.20.30.40/", "10.20.30.40", web.Policy{}},
		{"metadata address", "http://169.254.169.254/latest/meta-data/", "169.254.169.254", web.Policy{}},
		{"host off the allowlist", "http://evil.test/", "evil.test", allow},
		{"redirect to a loopback IP", "http://docs.test/moved?to=http://127.0.0.1" + port + "/alpha", "127.0.0.1", web.Policy{}},
		{"redirect off the allowlist", "http://docs.test/moved?to=http://evil.test/", "evil.test", allow},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := web.Fetch(ctx, http.DefaultClient, c.policy, c.url)
			if err == nil || !strings.Contains(err.Error(), "refused "+c.host) {
				t.Fatalf("Fetch(%s) = %v, want a refusal that names %s", c.url, err, c.host)
			}
		})
	}
	// A wildcard entry allows the host and its subdomains; a redirect on the list is followed.
	wild := web.Policy{Allowlist: true, Hosts: []string{"*.docs.test"}}
	if text, err := web.Fetch(ctx, http.DefaultClient, wild, "http://docs.test/moved?to=/gamma"); err != nil || !strings.Contains(text, "folds flat") {
		t.Fatalf("wildcard allowlist: %q %v", text, err)
	}
}

func TestWebFetchThroughAProxyChecksTheTarget(t *testing.T) {
	srv := fixtures(t)
	// The fixture server routes on the Host header, so it also answers as a forward proxy.
	proxy, _ := url.Parse(srv.URL)
	hc := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}}
	ctx := context.Background()
	text, err := web.Fetch(ctx, hc, web.Policy{}, "http://docs.test/alpha")
	if err != nil || !strings.Contains(text, "bamboo top") {
		t.Fatalf("through the proxy: %q %v", text, err)
	}
	if _, err := web.Fetch(ctx, hc, web.Policy{}, "http://localhost/alpha"); err == nil || !strings.Contains(err.Error(), "refused localhost") {
		t.Fatalf("a loopback target through the proxy: %v", err)
	}
}

func TestWebFetchThroughAProxyLetsTheProxyResolveAnUnknownName(t *testing.T) {
	srv := fixtures(t)
	proxy, _ := url.Parse(srv.URL)
	hc := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}}
	ctx := context.Background()
	// A work network often resolves public names only in the proxy. The request goes to
	// the proxy, so the error is not a resolve error of this host.
	_, err := web.Fetch(ctx, hc, web.Policy{}, "http://no-such-name.invalid/alpha")
	if err != nil && strings.Contains(err.Error(), "cannot resolve") {
		t.Fatalf("through the proxy: %v", err)
	}
	// Without a proxy, the name still cannot be resolved.
	_, err = web.Fetch(ctx, &http.Client{Transport: &http.Transport{}}, web.Policy{}, "http://no-such-name.invalid/alpha")
	if err == nil || !strings.Contains(err.Error(), "cannot resolve") {
		t.Fatalf("without a proxy: %v", err)
	}
	// An allowlist still applies to a name that goes to the proxy.
	_, err = web.Fetch(ctx, hc, web.Policy{Allowlist: true, Hosts: []string{"docs.test"}}, "http://no-such-name.invalid/alpha")
	if err == nil || strings.Contains(err.Error(), "cannot resolve") {
		t.Fatalf("off the allowlist through the proxy: %v", err)
	}
}

func TestWebSearchProviders(t *testing.T) {
	fixtures(t)
	old := web.Endpoints
	web.Endpoints = map[string]string{web.KindTavily: "http://search.test", web.KindExa: "http://search.test", web.KindBrave: "http://search.test"}
	t.Cleanup(func() { web.Endpoints = old })
	ctx := context.Background()
	hc := web.Client(http.DefaultClient)
	for _, c := range []struct {
		p    web.Provider
		cost float64
	}{
		{web.Provider{Kind: web.KindTavily, Key: fakeweb.SearchKey}, 0.008},
		{web.Provider{Kind: web.KindExa, Key: fakeweb.SearchKey}, fakeweb.ExaCostUSD},
		{web.Provider{Kind: web.KindSearXNG, BaseURL: "http://search.test"}, 0},
		{web.Provider{Kind: web.KindBrave, Key: fakeweb.SearchKey}, 0.005},
	} {
		t.Run(c.p.Kind, func(t *testing.T) {
			res, err := web.Search(ctx, hc, c.p, "desks", 2)
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Hits) != 2 || res.Hits[0].URL != "http://docs.test/alpha" || res.Hits[0].Snippet != fakeweb.Products[0].Fact {
				t.Fatalf("hits %+v", res.Hits)
			}
			if res.CostUSD != c.cost {
				t.Fatalf("cost %v, want %v", res.CostUSD, c.cost)
			}
		})
	}
	if _, err := web.Search(ctx, hc, web.Provider{Kind: web.KindTavily, Key: "wrong-key-0000"}, "desks", 2); err == nil || !strings.Contains(err.Error(), "refused the key") {
		t.Fatalf("wrong key: %v", err)
	}
	if _, err := web.Search(ctx, hc, web.Provider{Kind: web.KindSearXNG, BaseURL: "http://search.test/nojson"}, "desks", 2); err == nil || !strings.Contains(err.Error(), "Turn on the json format in SearXNG settings.yml") {
		t.Fatalf("SearXNG without JSON: %v", err)
	}
}
