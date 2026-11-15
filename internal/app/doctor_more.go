package app

import (
	"context"
	"errors"
	"os"
	"runtime"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/providers/applecontainer"
)

func init() {
	doctorChecks = append(doctorChecks, doctorComputers, doctorAgentd, doctorConnectors, doctorPush)
}

// doctorComputers checks each computer provider and the host conditions of D-15.
func doctorComputers(ctx context.Context, a *App) []Check {
	var out []Check
	kinds := make([]string, 0, len(a.Computers.Providers))
	for k := range a.Computers.Providers {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		name := "computer provider " + k
		p, ok := a.Computers.Providers[k].(pinger)
		if !ok {
			out = append(out, pass(name, "runs agentd as a child process"))
			continue
		}
		pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := p.Ping(pctx)
		cancel()
		switch {
		case errors.Is(err, applecontainer.ErrNotInstalled):
			// The first preparation installs the runtime; that is not a fault.
			out = append(out, pass(name, err.Error()))
		case err != nil:
			out = append(out, fail(name, err))
		default:
			out = append(out, pass(name, "answers"))
		}
	}
	if _, ok := a.Computers.Providers["docker"]; !ok {
		out = append(out, Check{Name: "computer provider docker", Reason: "Docker is not reachable; start Docker Desktop or Docker Engine"})
	}
	if runtime.GOOS == "darwin" {
		if err := applecontainer.Supported(); err != nil {
			out = append(out, Check{Name: "Apple container", Reason: err.Error()})
		}
	}
	if runtime.GOOS == "linux" {
		if _, err := os.Stat("/dev/kvm"); err != nil {
			out = append(out, Check{Name: "/dev/kvm", Reason: "/dev/kvm is missing, so this host cannot run Firecracker computers; turn on KVM or use docker"})
		} else {
			out = append(out, pass("/dev/kvm", "present"))
		}
	}
	out = append(out, pass("default provider", a.Computers.Default))
	return out
}

// doctorAgentd sends a health request to the agentd of each computer that runs in this
// process, with the pinned certificate and the computer token.
func doctorAgentd(ctx context.Context, a *App) []Check {
	hs := a.Computers.HealthAll(ctx)
	if len(hs) == 0 {
		return []Check{pass("agentd", "no computer runs in this process")}
	}
	var out []Check
	for _, h := range hs {
		name := "agentd of computer " + h.Computer.String()
		if h.Err != nil {
			out = append(out, fail(name, h.Err))
		} else {
			out = append(out, pass(name, "answers with version "+h.Version))
		}
	}
	return out
}

// doctorConnectors lists the tools of each remote connector through its first account.
// A stdio connector runs only inside a zone computer, so its connector test checks it.
func doctorConnectors(ctx context.Context, a *App) []Check {
	rows, err := a.DB.Engine.Query(ctx, "SELECT DISTINCT space_id FROM connectors")
	if err != nil {
		return []Check{fail("connectors", err)}
	}
	var spaces []uuid.UUID
	for rows.Next() {
		var s uuid.UUID
		if rows.Scan(&s) == nil {
			spaces = append(spaces, s)
		}
	}
	rows.Close()
	var out []Check
	for _, s := range spaces {
		cs, err := a.Connectors.List(ctx, s)
		if err != nil {
			out = append(out, fail("connectors", err))
			continue
		}
		for _, c := range cs {
			if c.Kind == "mcp_stdio" {
				continue
			}
			name := "connector " + c.Name
			accs, err := a.Connectors.Accounts(ctx, c.ID())
			if err != nil {
				out = append(out, fail(name, err))
				continue
			}
			if len(accs) == 0 {
				out = append(out, fail(name, errors.New("the connector has no account; connect one in Settings, Connectors")))
				continue
			}
			cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			tools, err := a.Connectors.Tools(cctx, c, accs[0], uuid.Nil)
			cancel()
			if err != nil {
				out = append(out, Check{Name: name, Reason: a.Vault.Redactor.String(ctx, s, err.Error())})
			} else {
				out = append(out, pass(name, c.URL+" lists "+strconv.Itoa(len(tools))+" tools"))
			}
		}
	}
	return out
}

// doctorPush checks that the Web Push keys exist.
func doctorPush(ctx context.Context, a *App) []Check {
	if a.Push == nil {
		return nil
	}
	pub, _, err := a.Push.VAPID(ctx)
	if err != nil {
		return []Check{fail("push keys", err)}
	}
	if pub == "" {
		return []Check{{Name: "push keys", Reason: "no Web Push keys exist"}}
	}
	return []Check{pass("push keys", "the VAPID key pair exists")}
}
