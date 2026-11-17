package app

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/alternayte/fettle/internal/auth"
	"github.com/alternayte/fettle/internal/computers"
	"github.com/alternayte/fettle/internal/providers/kiln"
)

// ProviderAvailability tells whether a provider works on this server, and why not.
type ProviderAvailability struct {
	Kind      string `json:"kind"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	// Status is the background preparation of this provider.
	Status computers.PrepareStatus `json:"status"`
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
		st := a.Computers.PrepareStatusOf(k)
		av := ProviderAvailability{Kind: k, Available: true, Status: st}
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
	huma.Register(api, op("check-kiln", http.MethodPost, "/computer-setup/kiln-check", "Test the connection to a remote Kiln host and its token (instance admins)", "computers"), func(ctx context.Context, in *struct {
		Body struct {
			URL   string `json:"url" minLength:"1" maxLength:"2048"`
			Token string `json:"token" minLength:"1" maxLength:"4096"`
		}
	}) (*struct {
		Body struct {
			Pass       bool   `json:"pass"`
			Error      string `json:"error,omitempty"`
			Configured bool   `json:"configured" doc:"This server already uses a Kiln host (FETTLE_KILN_URL)."`
		}
	}, error) {
		if p := auth.From(ctx); p == nil || !p.IsAdmin {
			return nil, huma.Error403Forbidden("only an instance admin adds computer providers")
		}
		u, err := url.Parse(in.Body.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return nil, huma.Error422UnprocessableEntity("Use the URL of the Kiln host, such as https://kiln.acme.test.")
		}
		k := kiln.New(in.Body.URL, in.Body.Token, a.computerImage(ctx), a.Cfg.Version)
		k.HTTP = a.HTTP
		out := &struct {
			Body struct {
				Pass       bool   `json:"pass"`
				Error      string `json:"error,omitempty"`
				Configured bool   `json:"configured" doc:"This server already uses a Kiln host (FETTLE_KILN_URL)."`
			}
		}{}
		_, out.Body.Configured = a.Computers.Providers["kiln"]
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		if err := k.Ping(cctx); err != nil {
			out.Body.Error = strings.ReplaceAll(err.Error(), in.Body.Token, "[secret:Kiln token]")
		} else {
			out.Body.Pass = true
		}
		return out, nil
	})
	huma.Register(api, op("check-e2b", http.MethodPost, "/computer-setup/e2b-check", "Test an E2B API key (instance admins)", "computers"), func(ctx context.Context, in *struct {
		Body struct {
			Key string `json:"key" minLength:"1" maxLength:"4096"`
		}
	}) (*struct {
		Body struct {
			Pass       bool   `json:"pass"`
			Error      string `json:"error,omitempty"`
			Configured bool   `json:"configured" doc:"This server already uses E2B (FETTLE_E2B_API_KEY)."`
		}
	}, error) {
		if p := auth.From(ctx); p == nil || !p.IsAdmin {
			return nil, huma.Error403Forbidden("only an instance admin adds computer providers")
		}
		out := &struct {
			Body struct {
				Pass       bool   `json:"pass"`
				Error      string `json:"error,omitempty"`
				Configured bool   `json:"configured" doc:"This server already uses E2B (FETTLE_E2B_API_KEY)."`
			}
		}{}
		_, out.Body.Configured = a.Computers.Providers["e2b"]
		cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		if err := a.newE2B(ctx, in.Body.Key).Ping(cctx); err != nil {
			out.Body.Error = strings.ReplaceAll(err.Error(), in.Body.Key, "[secret:E2B API key]")
		} else {
			out.Body.Pass = true
		}
		return out, nil
	})
	huma.Register(api, op("get-computer-setup", http.MethodGet, "/computer-setup", "The default computer provider, what works on this server, and the preparation progress", "computers"), func(ctx context.Context, _ *struct{}) (*struct{ Body ComputerSetup }, error) {
		return &struct{ Body ComputerSetup }{Body: a.computerSetup(ctx)}, nil
	})
	huma.Register(api, op("prepare-computers", http.MethodPost, "/computer-setup/prepare", "Prepare a provider in the background, the default one unless provider names another: its runtime and the computer image", "computers"), func(ctx context.Context, in *struct {
		Provider string `query:"provider" doc:"A provider kind. Default: the default provider."`
	}) (*struct{ Body ComputerSetup }, error) {
		if p := auth.From(ctx); p == nil || !p.IsAdmin {
			return nil, huma.Error403Forbidden("only an instance admin prepares computer providers")
		}
		s := a.computerSetup(ctx)
		kind := s.Default
		if in.Provider != "" {
			kind = ""
			for _, pv := range s.Providers {
				if pv.Kind == in.Provider && pv.Available {
					kind = pv.Kind
				}
			}
			if kind == "" {
				return nil, huma.Error409Conflict("the " + in.Provider + " provider does not work on this server")
			}
		}
		if kind == "" {
			return nil, huma.Error409Conflict("no computer provider works on this server")
		}
		if err := a.Computers.StartPrepare(kind); err != nil {
			return nil, err
		}
		return &struct{ Body ComputerSetup }{Body: a.computerSetup(ctx)}, nil
	})
}
