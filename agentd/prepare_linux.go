//go:build linux

package agentd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// prepareMachine runs the boot step. As root it applies the network rules,
// prepares the home folder and drops to the configured user for good.
// Without root it changes nothing and reports that no rules apply.
func prepareMachine(ctx context.Context, cfg Config, logger *slog.Logger) (NetworkState, error) {
	state := NetworkState{Mode: cfg.Network.Mode}
	if os.Geteuid() != 0 {
		if cfg.Network.Mode == NetworkAllowlist {
			logger.Warn("network rules not applied: agentd does not run as root")
		}
		if len(cfg.CA) > 0 || cfg.Proxy != "" {
			logger.Warn("extra roots and proxy not applied: agentd does not run as root")
		}
		return state, nil
	}
	u, err := user.Lookup(cfg.User)
	if err != nil {
		return state, fmt.Errorf("user %q: %w", cfg.User, err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return state, fmt.Errorf("user %q: uid %q: %w", cfg.User, u.Uid, err)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return state, fmt.Errorf("user %q: gid %q: %w", cfg.User, u.Gid, err)
	}
	if uid == 0 || gid == 0 {
		return state, fmt.Errorf("user %q has uid or gid 0; agentd refuses to run tools as root", cfg.User)
	}
	// Providers without a hosts option (Apple container) pass test DNS entries here.
	if err := addHosts(os.Getenv("FETTLE_AGENTD_HOSTS")); err != nil {
		return state, err
	}
	state.Env = map[string]string{}
	if len(cfg.CA) > 0 {
		if err := installRoots("/", cfg.CA, runCommand(ctx)); err != nil {
			return state, fmt.Errorf("install the extra root certificates: %w", err)
		}
		for k, v := range rootsEnv() {
			state.Env[k] = v
		}
		logger.Info("extra root certificates installed")
	}
	if cfg.Proxy != "" {
		ruleset, err := startFilter(ctx, cfg, logger)
		if err != nil {
			return state, err
		}
		if err := applyRuleset(ctx, ruleset); err != nil {
			return state, err
		}
		state.Applied = true
		state.RulesHash = rulesetHash(ruleset)
		state.Proxy = "http://" + FilterAddr
		for k, v := range proxyEnv(state.Proxy) {
			state.Env[k] = v
		}
		logger.Info("network rules applied", "hash", state.RulesHash, "proxy", true, "hosts", len(cfg.Network.AllowHosts))
	} else if cfg.Network.Mode == NetworkAllowlist {
		ruleset, err := buildRuleset(ctx, cfg.Network, systemLookup, logger)
		if err != nil {
			return state, err
		}
		if err := applyRuleset(ctx, ruleset); err != nil {
			return state, err
		}
		state.Applied = true
		state.RulesHash = rulesetHash(ruleset)
		logger.Info("network rules applied", "hash", state.RulesHash, "hosts", len(cfg.Network.AllowHosts))
	}
	if err := prepareFolders(cfg.Home, uid, gid); err != nil {
		return state, err
	}
	if err := dropPrivileges(uid, gid); err != nil {
		return state, err
	}
	for k, v := range map[string]string{"HOME": cfg.Home, "USER": cfg.User, "LOGNAME": cfg.User} {
		if err := os.Setenv(k, v); err != nil {
			return state, err
		}
	}
	logger.Info("dropped root", "user", cfg.User, "uid", uid, "gid", gid)
	return state, nil
}

// runCommand runs a boot command and returns its output in the error.
func runCommand(ctx context.Context) func(name string, args ...string) error {
	return func(name string, args ...string) error {
		out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(out)))
		}
		return nil
	}
}

// startFilter starts the filter proxy as the fettle-net user and returns the nftables
// ruleset that lets only that user reach the upstream proxy. The configuration, with the
// proxy password, goes to the process on standard input.
func startFilter(ctx context.Context, cfg Config, logger *slog.Logger) (string, error) {
	u, err := user.Lookup(FilterUser)
	if err != nil {
		return "", fmt.Errorf("user %q: %w", FilterUser, err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return "", fmt.Errorf("user %q: uid %q: %w", FilterUser, u.Uid, err)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return "", fmt.Errorf("user %q: gid %q: %w", FilterUser, u.Gid, err)
	}
	up, err := url.Parse(cfg.Proxy)
	if err != nil {
		return "", err
	}
	port := up.Port()
	if port == "" {
		port = "80"
		if up.Scheme == "https" {
			port = "443"
		}
	}
	pn, err := strconv.Atoi(port)
	if err != nil || pn <= 0 || pn > 65535 {
		return "", fmt.Errorf("the proxy port %q is not valid", port)
	}
	var addrs []netip.Addr
	if a, err := netip.ParseAddr(up.Hostname()); err == nil {
		addrs = []netip.Addr{a}
	} else if addrs, err = systemLookup(ctx, up.Hostname()); err != nil {
		return "", fmt.Errorf("the proxy host %s does not resolve: %w", up.Hostname(), err)
	}
	var targets []netip.AddrPort
	for _, a := range addrs {
		targets = append(targets, netip.AddrPortFrom(a, uint16(pn)))
	}
	ruleset, err := buildProxyRuleset(uid, targets)
	if err != nil {
		return "", err
	}
	conf, err := json.Marshal(FilterConfig{Listen: FilterAddr, Upstream: cfg.Proxy, Password: cfg.ProxyPassword, Network: cfg.Network})
	if err != nil {
		return "", err
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	cmd := exec.Command(exe, "filter-proxy")
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(conf)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), Groups: []uint32{}}, Setsid: true}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start the filter proxy: %w", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		select {
		case err := <-exited:
			return "", fmt.Errorf("the filter proxy stopped: %w", err)
		default:
		}
		if c, err := net.DialTimeout("tcp", FilterAddr, 200*time.Millisecond); err == nil {
			_ = c.Close()
			logger.Info("filter proxy ready", "addr", FilterAddr, "user", FilterUser)
			go func() {
				if err := <-exited; err != nil {
					logger.Error("filter proxy stopped", "error", err.Error())
				}
			}()
			return ruleset, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	return "", errors.New("the filter proxy did not start in 10 seconds")
}

func applyRuleset(ctx context.Context, ruleset string) error {
	cmd := exec.CommandContext(ctx, "nft", "-f", "-")
	cmd.Stdin = strings.NewReader(ruleset)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("nft: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func prepareFolders(home string, uid, gid int) error {
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	if err := os.Chown(home, uid, gid); err != nil {
		return err
	}
	for _, dir := range []string{filepath.Join(home, ".fettle"), filepath.Join(home, ".fettle", "exec")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		if err := os.Lchown(dir, uid, gid); err != nil {
			return err
		}
	}
	return nil
}

// dropPrivileges sets no_new_privs, the groups, the gid and the uid, then
// checks that root cannot come back.
func dropPrivileges(uid, gid int) error {
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("set no_new_privs: %w", err)
	}
	if err := syscall.Setgroups([]int{gid}); err != nil {
		return fmt.Errorf("setgroups: %w", err)
	}
	if err := syscall.Setgid(gid); err != nil {
		return fmt.Errorf("setgid: %w", err)
	}
	if err := syscall.Setuid(uid); err != nil {
		return fmt.Errorf("setuid: %w", err)
	}
	if os.Getuid() != uid || os.Geteuid() != uid || os.Getgid() != gid || os.Getegid() != gid {
		return errors.New("privilege drop did not take effect")
	}
	if err := syscall.Setuid(0); err == nil {
		return errors.New("privilege drop failed: setuid(0) still works")
	}
	if err := syscall.Setgid(0); err == nil {
		return errors.New("privilege drop failed: setgid(0) still works")
	}
	return nil
}

func systemLookup(ctx context.Context, host string) ([]netip.Addr, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// addHosts appends "name=ip,name=ip" entries to /etc/hosts.
func addHosts(spec string) error {
	if strings.TrimSpace(spec) == "" {
		return nil
	}
	var b strings.Builder
	for _, kv := range strings.Split(spec, ",") {
		name, ip, ok := strings.Cut(strings.TrimSpace(kv), "=")
		addr, err := netip.ParseAddr(ip)
		if !ok || err != nil || name == "" || strings.ContainsAny(name, " \t\n#") {
			return fmt.Errorf("FETTLE_AGENTD_HOSTS entry %q is not name=ip", kv)
		}
		fmt.Fprintf(&b, "%s\t%s\n", addr, name)
	}
	f, err := os.OpenFile("/etc/hosts", os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(b.String()); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// proxyEnv is the environment that sends the tools of commands through the filter proxy.
func proxyEnv(filter string) map[string]string {
	env := map[string]string{"NODE_USE_ENV_PROXY": "1"}
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		env[k] = filter
	}
	env["NO_PROXY"] = "localhost,127.0.0.1"
	env["no_proxy"] = env["NO_PROXY"]
	return env
}
