package agentd

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

// FilterAddr is the address of the filter proxy inside the computer.
const FilterAddr = "127.0.0.1:3128"

// FilterUser is the system user of the filter proxy. nftables lets only this user reach the
// upstream proxy.
const FilterUser = "fettle-net"

// FilterConfig is the configuration of the filter proxy. agentd sends it on standard input,
// so the password is in no environment and no command line.
type FilterConfig struct {
	Listen   string        `json:"listen"`
	Upstream string        `json:"upstream"`
	Password string        `json:"password,omitempty"`
	Network  NetworkPolicy `json:"network"`
}

// filterProxy is the forward proxy that every command and the browser of the computer use
// when the server sets a proxy. It checks each target host against the zone policy and
// sends the allowed traffic to the upstream proxy, signed in when it has credentials.
type filterProxy struct {
	cfg      FilterConfig
	upstream *url.URL
	auth     string
	dial     func(ctx context.Context, network, addr string) (net.Conn, error)
	tls      *tls.Config
	plain    *httputil.ReverseProxy
	logger   *slog.Logger
}

func newFilterProxy(cfg FilterConfig, logger *slog.Logger) (*filterProxy, error) {
	u, err := url.Parse(cfg.Upstream)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return nil, fmt.Errorf("the upstream proxy %q is not an http:// or https:// URL", cfg.Upstream)
	}
	if u.Port() == "" {
		port := "80"
		if u.Scheme == "https" {
			port = "443"
		}
		u.Host = net.JoinHostPort(u.Hostname(), port)
	}
	f := &filterProxy{cfg: cfg, upstream: u, logger: logger}
	if user := u.User.Username(); user != "" {
		f.auth = "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+cfg.Password))
	}
	u.User = nil
	d := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	f.dial = d.DialContext
	f.tls = &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12}
	tr := &http.Transport{
		Proxy:                 func(*http.Request) (*url.URL, error) { return f.upstream, nil },
		DialContext:           func(ctx context.Context, network, addr string) (net.Conn, error) { return f.dial(ctx, network, addr) },
		TLSClientConfig:       f.tls,
		ResponseHeaderTimeout: 60 * time.Second,
		IdleConnTimeout:       90 * time.Second,
	}
	if f.auth != "" {
		tr.ProxyConnectHeader = http.Header{"Proxy-Authorization": {f.auth}}
	}
	f.plain = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL, pr.Out.Host = pr.In.URL, pr.In.Host
			if f.auth != "" {
				pr.Out.Header.Set("Proxy-Authorization", f.auth)
			}
		},
		Transport: tr,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			f.logger.Warn("filter proxy: upstream failed", "host", r.URL.Hostname(), "error", err.Error())
			refuse(w, http.StatusBadGateway, "The proxy did not answer: "+err.Error())
		},
	}
	return f, nil
}

// hostAllowed reports whether a zone policy lets the computer reach host on port. An open
// zone allows every host. In an allowlist zone, a host matches an entry of the same name,
// and an entry *.x matches x and every subdomain of x, on port 80 or 443.
func hostAllowed(p NetworkPolicy, host, port string) bool {
	if p.Mode != NetworkAllowlist {
		return true
	}
	if port != "80" && port != "443" {
		return false
	}
	host = strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[]")), ".")
	for _, h := range p.AllowHosts {
		h = strings.TrimSuffix(strings.TrimSpace(strings.ToLower(h)), ".")
		if base, ok := strings.CutPrefix(h, "*."); ok {
			if host == base || strings.HasSuffix(host, "."+base) {
				return true
			}
			continue
		}
		if h != "" && host == h {
			return true
		}
	}
	return false
}

// refuse writes a plain text answer that a person can read in a terminal.
func refuse(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Connection", "close")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, msg+"\n")
}

func (f *filterProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		f.connect(w, r)
		return
	}
	if r.URL.Host == "" || (r.URL.Scheme != "http" && r.URL.Scheme != "") {
		refuse(w, http.StatusBadRequest, "This is the proxy of the computer. Send it proxy requests only.")
		return
	}
	port := r.URL.Port()
	if port == "" {
		port = "80"
	}
	if !hostAllowed(f.cfg.Network, r.URL.Hostname(), port) {
		f.logger.Info("filter proxy: refused", "host", r.URL.Hostname())
		refuse(w, http.StatusForbidden, r.URL.Hostname()+" is not on the allowlist of this zone.")
		return
	}
	r.Header.Del("Proxy-Authorization")
	f.plain.ServeHTTP(w, r)
}

// connect opens a tunnel to the target through the upstream proxy.
func (f *filterProxy) connect(w http.ResponseWriter, r *http.Request) {
	target := r.Host
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		refuse(w, http.StatusBadRequest, "CONNECT needs a host and a port.")
		return
	}
	if !hostAllowed(f.cfg.Network, host, port) {
		f.logger.Info("filter proxy: refused", "host", host, "port", port)
		refuse(w, http.StatusForbidden, host+" is not on the allowlist of this zone.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	up, br, err := f.tunnel(ctx, target)
	if err != nil {
		f.logger.Warn("filter proxy: tunnel failed", "host", host, "error", err.Error())
		refuse(w, http.StatusBadGateway, err.Error())
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		_ = up.Close()
		refuse(w, http.StatusInternalServerError, "The proxy cannot open a tunnel here.")
		return
	}
	client, cbuf, err := hj.Hijack()
	if err != nil {
		_ = up.Close()
		return
	}
	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
		_ = client.Close()
		_ = up.Close()
		return
	}
	splice(client, cbuf.Reader, up, br)
}

// tunnel dials the upstream proxy and asks it for a tunnel to target.
func (f *filterProxy) tunnel(ctx context.Context, target string) (net.Conn, *bufio.Reader, error) {
	conn, err := f.dial(ctx, "tcp", f.upstream.Host)
	if err != nil {
		return nil, nil, proxyFail("The proxy did not answer: %v", err)
	}
	if f.upstream.Scheme == "https" {
		tc := tls.Client(conn, f.tls)
		if err := tc.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, nil, proxyFail("The proxy did not answer: %v", err)
		}
		conn = tc
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: target}, Host: target, Header: http.Header{}}
	if f.auth != "" {
		req.Header.Set("Proxy-Authorization", f.auth)
	}
	if err := req.Write(conn); err != nil {
		_ = conn.Close()
		return nil, nil, proxyFail("The proxy did not answer: %v", err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		_ = conn.Close()
		return nil, nil, proxyFail("The proxy did not answer: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		_ = conn.Close()
		if resp.StatusCode == http.StatusProxyAuthRequired {
			return nil, nil, proxyFail("The proxy refused the sign-in. Check the proxy user and password in Settings, Computers.")
		}
		return nil, nil, proxyFail("The proxy refused the connection to %s: %s", target, resp.Status)
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, br, nil
}

// proxyMessage is an answer of the filter proxy for a person to read, so it starts with a
// capital and ends with a full stop.
type proxyMessage struct{ msg string }

func (e *proxyMessage) Error() string { return e.msg }

func proxyFail(format string, args ...any) error {
	return &proxyMessage{msg: fmt.Sprintf(format, args...)}
}

// splice copies both ways until one side closes, then closes both.
func splice(a net.Conn, ar io.Reader, b net.Conn, br io.Reader) {
	var once sync.Once
	closeBoth := func() { _ = a.Close(); _ = b.Close() }
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(b, ar)
		once.Do(closeBoth)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(a, br)
		once.Do(closeBoth)
	}()
	wg.Wait()
}

// RunFilterProxy reads a FilterConfig from in and serves the filter proxy until ctx ends.
// agentd starts it as the fettle-net user.
func RunFilterProxy(ctx context.Context, in io.Reader, logger *slog.Logger) error {
	var cfg FilterConfig
	if err := json.NewDecoder(io.LimitReader(in, 1<<20)).Decode(&cfg); err != nil {
		return fmt.Errorf("filter proxy configuration: %w", err)
	}
	f, err := newFilterProxy(cfg, logger)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("filter proxy listen: %w", err)
	}
	srv := &http.Server{Handler: f, ReadHeaderTimeout: 30 * time.Second, ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelWarn)}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	logger.Info("filter proxy started", "addr", ln.Addr().String(), "mode", cfg.Network.Mode)
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
