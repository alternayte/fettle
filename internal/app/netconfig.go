package app

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/alternayte/fettle/internal/computers"
	"github.com/alternayte/fettle/internal/hostnet"
	"github.com/alternayte/fettle/internal/settings"
)

// Instance settings of computers and of the network (§10.5). An environment variable wins
// over each of them and locks it; the desktop user cannot set variables, so the settings
// exist.
func init() {
	settings.Register(settings.Def{Key: "computers.image", Scope: "instance", Default: "", Admin: true, Env: "FETTLE_COMPUTER_IMAGE",
		Doc: "The computer image. Empty uses the image of this build."})
	settings.Register(settings.Def{Key: "computers.extra_ca", Scope: "instance", Default: "", Admin: true, Env: "FETTLE_EXTRA_CA_FILE",
		Doc: "Extra root certificates as PEM, for a network that inspects HTTPS."})
	settings.Register(settings.Def{Key: "computers.proxy", Scope: "instance", Default: "", Admin: true, Env: "HTTPS_PROXY",
		Doc: "The proxy URL for the server and the computers, with the user name if the proxy needs one."})
	settings.Register(settings.Def{Key: "computers.proxy_password", Scope: "instance", Default: "", Admin: true, Env: "HTTPS_PROXY", Secret: true,
		Doc: "The password of the proxy user."})
	settings.Register(settings.Def{Key: "computers.use_host_settings", Scope: "instance", Default: true, Admin: true, Desktop: true,
		Doc: "Use this computer's certificates and proxy."})
}

// proxyEnvNames are the variables that set the proxy of the server, in the order they win.
var proxyEnvNames = []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"}

// envLock returns the environment variable that sets a setting, when one is set.
func envLock(d settings.Def) (string, bool) {
	if d.Env == "" {
		return "", false
	}
	names := []string{d.Env}
	if d.Env == "HTTPS_PROXY" {
		names = proxyEnvNames
	}
	for _, n := range names {
		if os.Getenv(n) != "" {
			return n, true
		}
	}
	return "", false
}

// BuiltinImage is the computer image that this build names: the release image that the
// main.computerImage ldflag sets, else the local build.
func BuiltinImage(ldflag string) string {
	if ldflag != "" {
		return ldflag
	}
	return computers.DevImage
}

// ComputerImage chooses the computer image: FETTLE_COMPUTER_IMAGE, else the computers.image
// setting, else the image of this build.
func ComputerImage(ldflag, setting string) string {
	if v := strings.TrimSpace(os.Getenv("FETTLE_COMPUTER_IMAGE")); v != "" {
		return v
	}
	if v := strings.TrimSpace(setting); v != "" {
		return v
	}
	return BuiltinImage(ldflag)
}

// computerImage returns the image that computers use now.
func (a *App) computerImage(ctx context.Context) string {
	return ComputerImage(a.Cfg.ComputerImage, a.Settings.String(ctx, nil, "computers.image"))
}

// applyImage gives the current image to the computer manager and the providers.
func (a *App) applyImage(ctx context.Context) {
	img := a.computerImage(ctx)
	a.Computers.SetImage(img)
	for k, p := range a.Computers.Providers {
		if k == "e2b" && os.Getenv("FETTLE_E2B_IMAGE") != "" {
			continue
		}
		if s, ok := p.(interface{ SetImage(string) }); ok {
			s.SetImage(img)
		}
	}
}

// netConfig is the network of the server and of the computers: extra roots and a proxy.
type netConfig struct {
	Roots     []byte
	RootsFrom string // env, setting, host or ""
	// Proxy holds the user and the password.
	Proxy     *url.URL
	NoProxy   string
	ProxyFrom string // env, setting, host or ""
}

// hostCache keeps the host values for a while, because reading the keychain takes time.
type hostCache struct {
	mu   sync.Mutex
	at   time.Time
	vals hostnet.Settings
	err  error
}

const hostCacheTTL = 5 * time.Minute

func (a *App) hostSettings(ctx context.Context) (hostnet.Settings, error) {
	a.host.mu.Lock()
	defer a.host.mu.Unlock()
	if !a.host.at.IsZero() && time.Since(a.host.at) < hostCacheTTL {
		return a.host.vals, a.host.err
	}
	rctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	a.host.vals, a.host.err = hostnet.Read(rctx)
	a.host.at = time.Now()
	return a.host.vals, a.host.err
}

// useHost reports whether the host values apply: a desktop build with the option on.
func (a *App) useHost(ctx context.Context) bool {
	return a.Cfg.HostSettings && a.Settings.Bool(ctx, nil, "computers.use_host_settings")
}

// resolveNetwork returns the roots and the proxy. For each, an environment variable wins,
// then the setting, then the host values when the desktop option is on.
func (a *App) resolveNetwork(ctx context.Context) (netConfig, error) {
	var n netConfig
	var host *hostnet.Settings
	hostVals := func() *hostnet.Settings {
		if host == nil {
			host = &hostnet.Settings{}
			if a.useHost(ctx) {
				h, err := a.hostSettings(ctx)
				if err != nil {
					a.Log.Warn("read the certificates and proxy of this computer", "error", err)
				}
				host = &h
			}
		}
		return host
	}
	switch {
	case a.Cfg.ExtraCAFile != "":
		b, err := os.ReadFile(a.Cfg.ExtraCAFile)
		if err != nil {
			return n, fmt.Errorf("read FETTLE_EXTRA_CA_FILE: %w", err)
		}
		n.Roots, n.RootsFrom = b, "env"
	case a.Settings.String(ctx, nil, "computers.extra_ca") != "":
		n.Roots, n.RootsFrom = []byte(a.Settings.String(ctx, nil, "computers.extra_ca")), "setting"
	default:
		if h := hostVals(); len(h.RootsPEM) > 0 {
			n.Roots, n.RootsFrom = h.RootsPEM, "host"
		}
	}
	n.NoProxy = os.Getenv("NO_PROXY")
	if n.NoProxy == "" {
		n.NoProxy = os.Getenv("no_proxy")
	}
	if name, ok := envLock(settings.Def{Env: "HTTPS_PROXY"}); ok {
		u, err := url.Parse(os.Getenv(name))
		if err != nil || u.Host == "" {
			return n, fmt.Errorf("%s is not a proxy URL", name)
		}
		n.Proxy, n.ProxyFrom = u, "env"
		return n, nil
	}
	if raw := strings.TrimSpace(a.Settings.String(ctx, nil, "computers.proxy")); raw != "" {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return n, fmt.Errorf("the computers.proxy setting %q is not a proxy URL", raw)
		}
		pw, err := a.Vault.LookupInstanceSecret(ctx, secretName("computers.proxy_password"))
		if err != nil {
			return n, err
		}
		if user := u.User.Username(); user != "" && len(pw) > 0 {
			u.User = url.UserPassword(user, string(pw))
		}
		n.Proxy, n.ProxyFrom = u, "setting"
		return n, nil
	}
	if h := hostVals(); h.Proxy != "" {
		u, err := url.Parse(h.Proxy)
		if err == nil && u.Host != "" {
			n.Proxy, n.ProxyFrom = u, "host"
			if h.NoProxy != "" {
				n.NoProxy = strings.Trim(n.NoProxy+","+h.NoProxy, ",")
			}
		}
	}
	return n, nil
}

// applyNetwork gives the current roots and proxy to the server HTTP client.
func (a *App) applyNetwork(ctx context.Context) error {
	t, ok := a.HTTP.Transport.(*NetTransport)
	if !ok {
		return nil
	}
	n, err := a.resolveNetwork(ctx)
	if err != nil {
		return err
	}
	key := fmt.Sprintf("%x|%s|%s|%s", sha256.Sum256(n.Roots), n.ProxyFrom, n.Proxy, n.NoProxy)
	a.netMu.Lock()
	defer a.netMu.Unlock()
	if key == a.netKey {
		return nil
	}
	proxy := envProxyFunc()
	if n.ProxyFrom != "env" {
		proxy = fixedProxyFunc(n.Proxy, n.NoProxy)
	}
	if err := t.Apply(n.Roots, proxy); err != nil {
		return err
	}
	a.netKey = key
	return nil
}

// computerNetwork is what a new computer gets: the roots, and the proxy with its password
// apart, so agentd can clear the password from its environment.
func (a *App) computerNetwork(ctx context.Context) (computers.Network, error) {
	n, err := a.resolveNetwork(ctx)
	if err != nil {
		return computers.Network{}, err
	}
	out := computers.Network{RootsPEM: string(n.Roots)}
	if n.Proxy != nil {
		u := *n.Proxy
		if pw, ok := u.User.Password(); ok {
			out.ProxyPassword = pw
			u.User = url.User(u.User.Username())
		}
		out.Proxy = u.String()
	}
	return out, nil
}

// secretName is the vault name of a secret setting.
func secretName(key string) string { return "setting:" + key }

var imageRefRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._\-/:@]{0,254}$`)

// checkInstanceSetting validates the value of a network or image setting before it is
// stored.
func checkInstanceSetting(key string, v any) error {
	s, isText := v.(string)
	switch key {
	case "computers.image":
		if !isText {
			return errors.New("the image must be text")
		}
		if s = strings.TrimSpace(s); s != "" && !imageRefRe.MatchString(s) {
			return fmt.Errorf("%q is not an image reference such as ghcr.io/acme/computer:1.0", s)
		}
	case "computers.extra_ca":
		if !isText {
			return errors.New("the certificates must be PEM text")
		}
		if strings.TrimSpace(s) == "" {
			return nil
		}
		return checkPEM([]byte(s))
	case "computers.proxy":
		if !isText {
			return errors.New("the proxy must be a URL")
		}
		if s = strings.TrimSpace(s); s == "" {
			return nil
		}
		u, err := url.Parse(s)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
			return fmt.Errorf("%q is not a proxy URL such as http://proxy.acme.test:3128", s)
		}
		if _, ok := u.User.Password(); ok {
			return errors.New("put the proxy password in the proxy password field, not in the URL")
		}
		if u.Path != "" && u.Path != "/" {
			return errors.New("a proxy URL has no path")
		}
	case "computers.proxy_password":
		if !isText {
			return errors.New("the password must be text")
		}
		if len(s) > 1024 {
			return errors.New("the password is longer than 1024 characters")
		}
	}
	return nil
}

// checkPEM accepts only PEM certificates, at least one.
func checkPEM(b []byte) error {
	n := 0
	for {
		var blk *pem.Block
		blk, b = pem.Decode(b)
		if blk == nil {
			break
		}
		if blk.Type != "CERTIFICATE" {
			return fmt.Errorf("the PEM text holds a %s block; give certificates only", blk.Type)
		}
		if _, err := x509.ParseCertificate(blk.Bytes); err != nil {
			return fmt.Errorf("a certificate does not parse: %w", err)
		}
		n++
	}
	if n == 0 {
		return errors.New("the text holds no PEM certificate")
	}
	return nil
}
