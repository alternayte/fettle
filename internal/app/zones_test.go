package app_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/apptest"
	"github.com/alternayte/fettle/internal/testenv"
)

func TestSCN_CMP_001_NewSpaceHasPersonalAndBrowsingZones(t *testing.T) {
	testenv.Each(t, func(t *testing.T, dialect string) {
		e := apptest.Start(t, dialect)
		var zs struct {
			Zones []struct {
				ID           string   `json:"id"`
				Name         string   `json:"name"`
				ProviderID   string   `json:"provider_id"`
				ProviderKind string   `json:"provider_kind"`
				Network      string   `json:"network"`
				AllowHosts   []string `json:"allow_hosts"`
				Size         string   `json:"size"`
				IdleMinutes  int      `json:"idle_minutes"`
			} `json:"zones"`
		}
		e.MustDo("GET", e.SP("/zones"), nil, &zs)
		names := map[string]bool{}
		for _, z := range zs.Zones {
			names[z.Name] = true
			if z.Network != "open" || len(z.AllowHosts) != 0 || z.IdleMinutes != 10 || z.ProviderID != "" || z.Size != "browser" {
				t.Fatalf("zone %s has no defaults: %+v", z.Name, z)
			}
			var n int
			if err := e.App.DB.Engine.QueryRow(context.Background(), "SELECT COUNT(*) FROM zone_grants WHERE zone_id = $1", uuid.MustParse(z.ID)).Scan(&n); err != nil || n != 0 {
				t.Fatalf("zone %s starts with %d grants", z.Name, n)
			}
		}
		if len(zs.Zones) != 2 || !names["personal"] || !names["browsing"] {
			t.Fatalf("zones %v", names)
		}
	})
}
