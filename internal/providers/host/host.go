// Package host is the host computer provider: agentd runs as a child process of Fettle with
// its home under the data folder. It has no isolation, so enabling it is a hard ask (§10.4).
package host

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alternayte/fettle/internal/computers"
)

// Provider runs agentd processes on this machine.
type Provider struct {
	// AgentdBin is the path of fettle-agentd.
	AgentdBin string
	// Root is the folder that holds one home per computer.
	Root string

	mu       sync.Mutex
	machines map[string]*machine
}

// New returns the host provider.
func New(agentdBin, root string) *Provider {
	return &Provider{AgentdBin: agentdBin, Root: root, machines: map[string]*machine{}}
}

// Kind implements computers.Provider.
func (p *Provider) Kind() string { return "host" }

// Caps implements computers.Provider.
func (p *Provider) Caps() computers.Caps { return computers.Caps{} }

// Prepare checks that the agentd binary exists.
func (p *Provider) Prepare(_ context.Context, progress func(computers.Progress)) error {
	if _, err := os.Stat(p.AgentdBin); err != nil {
		if progress != nil {
			progress(computers.Progress{Step: "agentd", State: "fail", Detail: err.Error()})
		}
		return fmt.Errorf("fettle-agentd is missing at %s: %w", p.AgentdBin, err)
	}
	if progress != nil {
		progress(computers.Progress{Step: "agentd", State: "pass", Detail: p.AgentdBin})
	}
	return os.MkdirAll(p.Root, 0o700)
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// Create starts agentd for a zone.
func (p *Provider) Create(ctx context.Context, s computers.Spec) (computers.Machine, error) {
	if err := p.Prepare(ctx, nil); err != nil {
		return nil, err
	}
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	ref := fmt.Sprintf("%s-%d", strings.ReplaceAll(s.ZoneID, "-", "")[:12], time.Now().UnixNano())
	home := filepath.Join(p.Root, ref, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	m := &machine{p: p, ref: ref, home: home, port: port, spec: s}
	if err := m.start(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.machines[ref] = m
	p.mu.Unlock()
	return m, nil
}

// Attach returns a machine that this process started; after a restart it is gone.
func (p *Provider) Attach(_ context.Context, ref string) (computers.Machine, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	m, ok := p.machines[ref]
	if !ok {
		return nil, computers.ErrGone
	}
	return m, nil
}

type machine struct {
	p    *Provider
	ref  string
	home string
	port int
	spec computers.Spec

	mu   sync.Mutex
	cmd  *exec.Cmd
	done chan struct{} // closed when the agentd process of cmd exits
}

// running reports whether agentd runs. The caller holds m.mu.
func (m *machine) running() bool {
	if m.cmd == nil {
		return false
	}
	select {
	case <-m.done:
		return false
	default:
		return true
	}
}

func (m *machine) start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running() {
		return nil
	}
	cmd := exec.Command(m.p.AgentdBin)
	env := []string{
		"FETTLE_AGENTD_ADDR=127.0.0.1:" + strconv.Itoa(m.port),
		"FETTLE_AGENTD_HOME=" + m.home,
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + m.home,
	}
	keys := make([]string, 0, len(m.spec.Env))
	for k := range m.spec.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+m.spec.Env[k])
	}
	cmd.Env = env
	cmd.Dir = m.home
	cmd.SysProcAttr = groupAttr()
	logf, err := os.OpenFile(filepath.Join(filepath.Dir(m.home), "agentd.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		_ = logf.Close()
		return err
	}
	done := make(chan struct{})
	m.cmd, m.done = cmd, done
	go func() {
		_ = cmd.Wait()
		_ = logf.Close()
		close(done)
	}()
	return nil
}

func (m *machine) Ref() string { return m.ref }

func (m *machine) AgentdURL(context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.running() {
		return "", errors.New("agentd is not running")
	}
	return "https://127.0.0.1:" + strconv.Itoa(m.port), nil
}

func (m *machine) stop() error {
	m.mu.Lock()
	cmd, done := m.cmd, m.done
	m.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	terminateGroup(cmd)
	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
	}
	return killGroup(cmd)
}

// Sleep stops agentd; the home folder stays.
func (m *machine) Sleep(context.Context) error { return m.stop() }

// Resume starts agentd again on the same port and home.
func (m *machine) Resume(context.Context) error { return m.start() }

// Fork is a copy fork, which the computer manager does.
func (m *machine) Fork(context.Context) (computers.Machine, error) {
	return nil, computers.ErrUnsupported
}

// Destroy stops agentd and removes its home.
func (m *machine) Destroy(context.Context) error {
	_ = m.stop()
	m.p.mu.Lock()
	delete(m.p.machines, m.ref)
	m.p.mu.Unlock()
	return os.RemoveAll(filepath.Dir(m.home))
}

func (m *machine) Stats(context.Context) (computers.Stats, error) { return computers.Stats{}, nil }
