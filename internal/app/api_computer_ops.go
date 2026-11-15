package app

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/alternayte/fettle/agentd/client"
	"github.com/alternayte/fettle/internal/computers"
)

// ComputerTest is the result of a computer test: start or wake the zone's computer and run
// one command through agentd.
type ComputerTest struct {
	Zone       string `json:"zone"`
	Provider   string `json:"provider"`
	Pass       bool   `json:"pass"`
	Output     string `json:"output"`
	StartMS    int64  `json:"start_ms" doc:"Time to get a running computer."`
	ExecMS     int64  `json:"exec_ms" doc:"Time of the test command."`
	Error      string `json:"error,omitempty"`
	ComputerID string `json:"computer_id,omitempty"`
}

func (a *App) registerComputerOps(api huma.API) {
	huma.Register(api, op("test-zone-computer", http.MethodPost, "/spaces/{space}/zones/{zone}/computer/test", "Start or wake the zone's computer and run one command through agentd", "computers"), func(ctx context.Context, in *ZonePath) (*struct{ Body ComputerTest }, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		z, err := a.Zones.Get(ctx, in.Space, in.Zone)
		if err != nil {
			return nil, zoneError(err)
		}
		out := ComputerTest{Zone: z.Name}
		if row, err := a.Computers.ProviderOf(ctx, z); err == nil {
			out.Provider = row.Kind
		}
		start := time.Now()
		l, err := a.Computers.Acquire(ctx, z.ID())
		out.StartMS = time.Since(start).Milliseconds()
		if err != nil {
			// A test reports why the computer did not start; it is not an API error.
			out.Error = err.Error()
			return &struct{ Body ComputerTest }{Body: out}, nil
		}
		defer l.Release()
		out.ComputerID = l.Computer.String()
		var b strings.Builder
		t0 := time.Now()
		res, err := l.Client.Exec(ctx, client.ExecRequest{Shell: "echo fettle-ok && uname -sm", TimeoutMS: 30000}, func(_, data string) { b.WriteString(data) })
		out.ExecMS = time.Since(t0).Milliseconds()
		out.Output = strings.TrimSpace(b.String())
		switch {
		case err != nil:
			out.Error = err.Error()
		case res.ExitCode != 0:
			out.Error = "the test command ended with a non-zero exit code"
		default:
			out.Pass = strings.HasPrefix(out.Output, "fettle-ok")
		}
		_ = a.Audit.Record(ctx, actorOf(p), &in.Space, "computer.test", "zone:"+z.ID().String(), map[string]string{"pass": boolText(out.Pass)})
		return &struct{ Body ComputerTest }{Body: out}, nil
	})

	huma.Register(api, op("stop-zone-computer", http.MethodPost, "/spaces/{space}/zones/{zone}/computer/stop", "Put the zone's computer to sleep. Its workspace stays; the next work wakes it.", "computers"), func(ctx context.Context, in *ZonePath) (*struct{ Body ZoneView }, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		z, err := a.Zones.Get(ctx, in.Space, in.Zone)
		if err != nil {
			return nil, zoneError(err)
		}
		c, err := a.Computers.Current(ctx, z.ID())
		if err != nil {
			return nil, err
		}
		if c != nil && c.State == computers.StateRunning {
			if err := a.Computers.Sleep(ctx, c.ID()); err != nil {
				return nil, err
			}
			if err := a.Audit.Record(ctx, actorOf(p), &in.Space, "computer.stop", "computer:"+c.ID().String(), nil); err != nil {
				return nil, err
			}
		}
		return &struct{ Body ZoneView }{Body: a.zoneView(ctx, z)}, nil
	})
}

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
