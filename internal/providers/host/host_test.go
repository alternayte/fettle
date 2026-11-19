package host_test

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	agentdpkg "github.com/alternayte/fettle/agentd"
	"github.com/alternayte/fettle/internal/computers"
	"github.com/alternayte/fettle/internal/computers/conformance"
	"github.com/alternayte/fettle/internal/providers/host"
)

func agentd(t *testing.T) string {
	if b := os.Getenv("FETTLE_AGENTD_BIN"); b != "" {
		return b
	}
	bin := filepath.Join(t.TempDir(), "fettle-agentd")
	_, file, _, _ := runtime.Caller(0)
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/fettle-agentd")
	cmd.Dir = filepath.Join(filepath.Dir(file), "..", "..", "..")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build agentd: %v\n%s", err, out)
	}
	return bin
}

// TestSCN_CMP_016_HostConformance covers the provider part of SCN-CMP-016; the hard-ask card
// to enable the host provider is in the app tests.
func TestSCN_CMP_016_HostConformance(t *testing.T) {
	p := host.New(agentd(t), t.TempDir())
	t.Run("provider", func(t *testing.T) { conformance.Run(t, p, "") })
	t.Run("manager", func(t *testing.T) { conformance.Manager(t, "host", p, "") })
}

// TestHostAgentdStopsWithItsParent checks that agentd does not outlive the process that
// started it. A child process starts a machine and exits without Destroy, as a killed server
// or a test that hits its timeout does. Before the fix, such agentd processes ran on and held
// loopback ports that Docker later gave to other computers.
func TestHostAgentdStopsWithItsParent(t *testing.T) {
	if os.Getenv("FETTLE_HOST_ORPHAN_ROOT") != "" {
		orphanChild()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestHostAgentdStopsWithItsParent$")
	cmd.Env = append(os.Environ(), "FETTLE_HOST_ORPHAN_ROOT="+t.TempDir(), "FETTLE_AGENTD_BIN="+agentd(t))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
	var addr string
	for sc := bufio.NewScanner(strings.NewReader(string(out))); sc.Scan(); {
		if a, ok := strings.CutPrefix(sc.Text(), "agentd "); ok {
			addr = a
		}
	}
	if addr == "" {
		t.Fatalf("the child printed no agentd address:\n%s", out)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err != nil {
			return
		}
		_ = c.Close()
		if time.Now().After(deadline) {
			t.Fatalf("agentd on %s still runs 10 s after its parent exited", addr)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// orphanChild starts a host machine, waits until agentd listens, prints its address and
// exits without Destroy.
func orphanChild() {
	fail := func(err error) {
		fmt.Println("error", err)
		os.Exit(1)
	}
	p := host.New(os.Getenv("FETTLE_AGENTD_BIN"), os.Getenv("FETTLE_HOST_ORPHAN_ROOT"))
	cert, key, err := agentdpkg.GenerateCert([]string{"fettle-computer"})
	if err != nil {
		fail(err)
	}
	ctx := context.Background()
	m, err := p.Create(ctx, computers.Spec{ZoneID: uuid.NewString(), Env: map[string]string{
		"FETTLE_AGENTD_TOKEN": "tok-" + uuid.NewString(), "FETTLE_AGENTD_TLS_CERT": string(cert), "FETTLE_AGENTD_TLS_KEY": string(key), "FETTLE_AGENTD_NETWORK": `{"mode":"open"}`,
	}})
	if err != nil {
		fail(err)
	}
	raw, err := m.AgentdURL(ctx)
	if err != nil {
		fail(err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		fail(err)
	}
	for start := time.Now(); ; time.Sleep(50 * time.Millisecond) {
		c, err := net.DialTimeout("tcp", u.Host, time.Second)
		if err == nil {
			_ = c.Close()
			break
		}
		if time.Since(start) > 30*time.Second {
			fail(fmt.Errorf("agentd does not listen on %s", u.Host))
		}
	}
	fmt.Println("agentd", u.Host)
	os.Exit(0)
}
