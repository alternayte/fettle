package app

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/auth"
	"github.com/alternayte/fettle/internal/models"
)

// Check is one doctor check (REQ-CORE-006).
type Check struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Reason string `json:"reason"`
}

// DoctorCheck adds checks; feature packages register more as they arrive.
type DoctorCheck func(ctx context.Context, a *App) []Check

var doctorChecks = []DoctorCheck{doctorStorage, doctorMasterKey, doctorProxy, doctorModels}

func pass(name, reason string) Check { return Check{Name: name, Pass: true, Reason: reason} }
func fail(name string, err error) Check {
	return Check{Name: name, Pass: false, Reason: err.Error()}
}

func doctorStorage(ctx context.Context, a *App) []Check {
	var out []Check
	kind := "SQLite at " + a.Cfg.SQLitePath()
	if a.DB.Postgres {
		kind = "PostgreSQL"
	}
	if err := a.DB.Engine.Ping(ctx); err != nil {
		out = append(out, fail("database", err))
	} else {
		out = append(out, pass("database", kind))
	}
	if err := a.DB.Pending(ctx); err != nil {
		out = append(out, fail("migrations", err))
	} else {
		out = append(out, pass("migrations", "all migrations are applied"))
	}
	if err := a.Objects.Check(ctx); err != nil {
		out = append(out, fail("object store", err))
	} else {
		out = append(out, pass("object store", a.Cfg.ObjectStore))
	}
	return out
}

func doctorMasterKey(ctx context.Context, a *App) []Check {
	if err := a.Vault.CheckMaster(ctx); err != nil {
		return []Check{fail("master key", err)}
	}
	return []Check{pass("master key", "from the "+string(a.MasterKeySource)+"; it unwraps every data key")}
}

func doctorProxy(ctx context.Context, a *App) []Check {
	n, err := a.resolveNetwork(ctx)
	if err != nil {
		return []Check{fail("proxy and CA", err)}
	}
	from := map[string]string{"env": "the environment", "setting": "Settings, Computers", "host": "this computer"}
	var parts []string
	if n.Proxy != nil {
		u := *n.Proxy
		u.User = nil
		parts = append(parts, "proxy "+u.String()+" from "+from[n.ProxyFrom])
	} else {
		parts = append(parts, "no proxy")
	}
	if len(n.Roots) > 0 {
		c := 0
		b := n.Roots
		for {
			var blk *pem.Block
			blk, b = pem.Decode(b)
			if blk == nil {
				break
			}
			if _, err := x509.ParseCertificate(blk.Bytes); err == nil {
				c++
			}
		}
		if c == 0 {
			return []Check{fail("proxy and CA", fmt.Errorf("the extra roots from %s have no certificates", from[n.RootsFrom]))}
		}
		parts = append(parts, fmt.Sprintf("%d extra CA certificates from %s", c, from[n.RootsFrom]))
	}
	return []Check{pass("proxy and CA", strings.Join(parts, ", "))}
}

// doctorModels sends one plain request to each model provider of each space.
func doctorModels(ctx context.Context, a *App) []Check {
	var out []Check
	rows, err := a.DB.Engine.Query(ctx, "SELECT DISTINCT space_id FROM model_providers")
	if err != nil {
		return []Check{fail("model providers", err)}
	}
	var spaces []string
	for rows.Next() {
		var s string
		if rows.Scan(&s) == nil {
			spaces = append(spaces, s)
		}
	}
	rows.Close()
	for _, s := range spaces {
		ps, err := a.Models.List(ctx, mustUUID(s))
		if err != nil {
			out = append(out, fail("model providers", err))
			continue
		}
		for _, p := range ps {
			name := "model provider " + p.Name
			c, err := a.Models.Conn(ctx, p)
			if err != nil {
				out = append(out, fail(name, err))
				continue
			}
			model := p.Smoke.Model
			if model == "" && len(p.Models) > 0 {
				model = p.Models[0].ID
			}
			ad, _ := models.AdapterFor(p.APIType)
			cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			_, err = ad.Call(cctx, a.HTTP, c, models.Request{Model: model, MaxTokens: 16, Messages: []models.Message{{Role: "user", Parts: []models.Part{models.TextPart("Reply with the word ready.")}}}}, nil)
			cancel()
			if err != nil {
				out = append(out, Check{Name: name, Reason: a.Vault.Redactor.Global(err.Error())})
			} else {
				out = append(out, pass(name, p.BaseURL+" answers with "+model))
			}
		}
	}
	if len(out) == 0 {
		out = append(out, Check{Name: "model providers", Reason: "no model provider is set; add one in Settings"})
	}
	return out
}

// Doctor runs every check.
func (a *App) Doctor(ctx context.Context) []Check {
	var out []Check
	for _, c := range doctorChecks {
		out = append(out, c(ctx, a)...)
	}
	return out
}

func (a *App) registerDiagnostics(api huma.API) {
	huma.Register(api, op("diagnostics", http.MethodGet, "/admin/diagnostics", "Run the doctor checks", "admin"), func(ctx context.Context, _ *struct{}) (*struct {
		Body struct {
			Checks []Check `json:"checks"`
		}
	}, error) {
		if p := auth.From(ctx); p == nil || !p.IsAdmin {
			return nil, huma.Error403Forbidden("Only an instance admin can do this.")
		}
		out := &struct {
			Body struct {
				Checks []Check `json:"checks"`
			}
		}{}
		out.Body.Checks = a.Doctor(ctx)
		return out, nil
	})
}

func mustUUID(s string) uuid.UUID {
	u, _ := uuid.Parse(s)
	return u
}
