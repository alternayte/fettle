//go:build contract

package e2b_test

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/alternayte/fettle/internal/computers"
	"github.com/alternayte/fettle/internal/computers/conformance"
	fakee2b "github.com/alternayte/fettle/internal/fakes/e2b"
	"github.com/alternayte/fettle/internal/providers/e2b"
)

// SCN-CMP-008: against the fake E2B API, the provider builds each template once, creates,
// pauses, resumes, forks and destroys sandboxes, and reaches agentd in the computer image
// through the port URL of the bridge.
func TestSCN_CMP_008_E2BAgainstFakeAPI(t *testing.T) {
	image := os.Getenv("FETTLE_COMPUTER_IMAGE")
	if image == "" {
		image = "fettle-computer:dev"
	}
	if err := exec.Command("docker", "image", "inspect", image).Run(); err != nil {
		t.Fatalf("the computer image %s is missing; run just image: %v", image, err)
	}
	fake, err := fakee2b.New("acme-e2b-key", fakee2b.NewDocker())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fake.Close)
	p := e2b.New("acme-e2b-key", image, "contract")
	p.API, p.HTTP, p.PollEvery = fake.URL, fake.Client(), 50*time.Millisecond
	ctx := context.Background()

	if err := p.Prepare(ctx, nil); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	t.Run("provider", func(t *testing.T) { conformance.Run(t, p, "") })
	t.Run("manager", func(t *testing.T) { conformance.Manager(t, "e2b", p, image) })
	t.Run("native_fork", func(t *testing.T) {
		s, token, cert := spec(t, `{"mode":"open"}`)
		m, err := p.Create(ctx, s)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = m.Destroy(context.Background()) })
		if err := waitHealth(t, m, token, cert); err != nil {
			t.Fatalf("health: %v", err)
		}
		f, err := m.Fork(ctx)
		if err != nil {
			t.Fatalf("fork: %v", err)
		}
		t.Cleanup(func() { _ = f.Destroy(context.Background()) })
		if err := waitHealth(t, f, token, cert); err != nil {
			t.Fatalf("the fork does not run agentd with the parent's token and certificate: %v", err)
		}
	})

	for _, size := range []computers.Size{computers.SizeSmall, computers.SizeBrowser, computers.SizeDesktop} {
		if n := fake.Builds(p.Template(size)); n != 1 {
			t.Fatalf("template %s built %d times, want once", p.Template(size), n)
		}
	}
	calls := map[string]int{}
	for _, c := range fake.Calls() {
		calls[c]++
	}
	var pauses, connects, forks, kills int
	for c, n := range calls {
		switch {
		case strings.HasSuffix(c, "/pause"):
			pauses += n
		case strings.HasSuffix(c, "/connect"):
			connects += n
		case strings.HasSuffix(c, "/fork"):
			forks += n
		case strings.HasPrefix(c, "DELETE "):
			kills += n
		}
	}
	if pauses == 0 || connects == 0 || forks == 0 || kills == 0 {
		t.Fatalf("the lifecycle calls: pause %d, connect %d, fork %d, kill %d", pauses, connects, forks, kills)
	}
	if fake.PortHits(e2b.BridgePort) == 0 {
		t.Fatal("no request went through the port URL of the bridge")
	}
	if left := fake.Sandboxes(); len(left) != 0 {
		t.Fatalf("sandboxes left after the suite: %v", left)
	}
}

// waitHealth waits until agentd of a machine answers, as the computer manager does.
func waitHealth(t *testing.T, m computers.Machine, token string, cert []byte) error {
	t.Helper()
	var err error
	for range 120 {
		if _, err = health(t, m, token, cert); err == nil {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return err
}
