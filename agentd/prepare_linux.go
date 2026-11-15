//go:build linux

package agentd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
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
	if cfg.Network.Mode == NetworkAllowlist {
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
