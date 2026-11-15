package app

import (
	"context"
	"net/http"
	"sort"

	"github.com/danielgtaylor/huma/v2"

	"github.com/alternayte/fettle/internal/computers"
)

// ProviderAvailability tells whether a provider works on this server, and why not.
type ProviderAvailability struct {
	Kind      string `json:"kind"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// ComputerSetup is the computer step of the first run (§15.3, step 2).
type ComputerSetup struct {
	Default   string                  `json:"default"`
	Providers []ProviderAvailability  `json:"providers"`
	Status    computers.PrepareStatus `json:"status"`
}

type pinger interface{ Ping(context.Context) error }

func (a *App) computerSetup(ctx context.Context) ComputerSetup {
	out := ComputerSetup{Default: a.Computers.Default, Providers: []ProviderAvailability{}}
	kinds := make([]string, 0, len(a.Computers.Providers))
	for k := range a.Computers.Providers {
		if k != "host" {
			kinds = append(kinds, k)
		}
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		av := ProviderAvailability{Kind: k, Available: true}
		st := a.Computers.PrepareStatusOf(k)
		// A provider that still prepares (its runtime is not here yet) counts as available.
		if p, ok := a.Computers.Providers[k].(pinger); ok && st.State != "ready" && st.State != "running" && k != "applecontainer" {
			if err := p.Ping(ctx); err != nil {
				av.Available, av.Reason = false, err.Error()
			}
		}
		out.Providers = append(out.Providers, av)
	}
	if _, ok := a.Computers.Providers[out.Default]; !ok {
		out.Default = ""
	}
	for _, p := range out.Providers {
		if p.Kind == out.Default && !p.Available {
			out.Default = ""
		}
	}
	if out.Default != "" {
		out.Status = a.Computers.PrepareStatusOf(out.Default)
	} else {
		out.Status = computers.PrepareStatus{State: "idle", Steps: []computers.Progress{}}
	}
	return out
}

func (a *App) registerPrepare(api huma.API) {
	huma.Register(api, op("get-computer-setup", http.MethodGet, "/computer-setup", "The default computer provider, what works on this server, and the preparation progress", "computers"), func(ctx context.Context, _ *struct{}) (*struct{ Body ComputerSetup }, error) {
		return &struct{ Body ComputerSetup }{Body: a.computerSetup(ctx)}, nil
	})
	huma.Register(api, op("prepare-computers", http.MethodPost, "/computer-setup/prepare", "Prepare the default provider in the background: its runtime and the computer image", "computers"), func(ctx context.Context, _ *struct{}) (*struct{ Body ComputerSetup }, error) {
		s := a.computerSetup(ctx)
		if s.Default == "" {
			return nil, huma.Error409Conflict("no computer provider works on this server")
		}
		if err := a.Computers.StartPrepare(s.Default); err != nil {
			return nil, err
		}
		return &struct{ Body ComputerSetup }{Body: a.computerSetup(ctx)}, nil
	})
}
