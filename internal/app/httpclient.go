package app

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync/atomic"
	"time"

	"golang.org/x/net/http/httpproxy"
)

// NewHTTPClient returns the client for all outbound HTTP (C-08, REQ-CORE-007). It uses
// HTTP_PROXY, HTTPS_PROXY and NO_PROXY, and trusts the system CAs plus extraCAFile. The app
// later applies the proxy and roots of the settings through its NetTransport.
func NewHTTPClient(extraCAFile string) (*http.Client, error) {
	var extra []byte
	if extraCAFile != "" {
		b, err := os.ReadFile(extraCAFile)
		if err != nil {
			return nil, fmt.Errorf("read FETTLE_EXTRA_CA_FILE: %w", err)
		}
		extra = b
	}
	t := &NetTransport{}
	if err := t.Apply(extra, envProxyFunc()); err != nil {
		return nil, fmt.Errorf("FETTLE_EXTRA_CA_FILE: %w", err)
	}
	return &http.Client{Transport: t}, nil
}

// NetTransport is the transport of the server HTTP client. Apply replaces its proxy and
// roots, so a change of the settings takes effect without a restart.
type NetTransport struct {
	cur atomic.Pointer[http.Transport]
}

// RoundTrip implements http.RoundTripper with the current transport.
func (t *NetTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return t.cur.Load().RoundTrip(r)
}

// CloseIdleConnections closes the idle connections of the current transport.
func (t *NetTransport) CloseIdleConnections() {
	if c := t.cur.Load(); c != nil {
		c.CloseIdleConnections()
	}
}

// Apply builds a transport that trusts the system CAs plus extraPEM and uses proxy. Requests
// that already run finish on the old transport.
func (t *NetTransport) Apply(extraPEM []byte, proxy func(*http.Request) (*url.URL, error)) error {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if len(extraPEM) > 0 && !pool.AppendCertsFromPEM(extraPEM) {
		return fmt.Errorf("the extra roots hold no PEM certificates")
	}
	tr := &http.Transport{
		Proxy:                 proxy,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ExpectContinueTimeout: time.Second,
		TLSClientConfig:       &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
	}
	if old := t.cur.Swap(tr); old != nil {
		old.CloseIdleConnections()
	}
	return nil
}

// envProxyFunc reads HTTP_PROXY, HTTPS_PROXY and NO_PROXY when the client is built, not once
// per process as http.ProxyFromEnvironment does.
func envProxyFunc() func(*http.Request) (*url.URL, error) {
	f := httpproxy.FromEnvironment().ProxyFunc()
	return func(r *http.Request) (*url.URL, error) { return f(r.URL) }
}

// fixedProxyFunc sends every request through proxy, except loopback and the hosts in noProxy.
func fixedProxyFunc(proxy *url.URL, noProxy string) func(*http.Request) (*url.URL, error) {
	if proxy == nil {
		return func(*http.Request) (*url.URL, error) { return nil, nil }
	}
	f := (&httpproxy.Config{HTTPProxy: proxy.String(), HTTPSProxy: proxy.String(), NoProxy: noProxy}).ProxyFunc()
	return func(r *http.Request) (*url.URL, error) { return f(r.URL) }
}
