package settings_test

import (
	"context"
	"testing"

	"github.com/alternayte/drel"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/settings"
	"github.com/alternayte/fettle/internal/spaces"
	"github.com/alternayte/fettle/internal/testenv"
)

func TestSCN_CORE_009_SettingsAreVersioned(t *testing.T) {
	testenv.Each(t, func(t *testing.T, dialect string) {
		db, _ := testenv.Open(t, dialect)
		ctx := context.Background()
		sp := &spaces.Space{Name: "acme", TimeZone: "UTC"}
		_ = db.Engine.WithTx(ctx, func(ctx context.Context) error {
			drel.Repo(drel.MustFromContext(ctx), spaces.SpaceMeta).Add(sp)
			return nil
		})
		id := sp.ID()
		s := settings.New(db.Engine, audit.New(db.Engine))
		a := audit.Actor{Kind: "user", ID: uuid.NewString()}
		b := audit.Actor{Kind: "user", ID: uuid.NewString()}
		if _, err := s.Set(ctx, a, &id, "space.time_zone", "Europe/London"); err != nil {
			t.Fatal(err)
		}
		v, err := s.Set(ctx, b, &id, "space.time_zone", "Asia/Tokyo")
		if err != nil {
			t.Fatal(err)
		}
		if v.Revision != 3 {
			t.Fatalf("revision = %d, want 3", v.Revision)
		}
		h, err := s.History(ctx, &id, "space.time_zone")
		if err != nil {
			t.Fatal(err)
		}
		if len(h) != 2 {
			t.Fatalf("history rows = %d", len(h))
		}
		if *h[0].OldValue != `"UTC"` || h[0].NewValue != `"Europe/London"` || h[0].UpdatedBy.String() != a.ID || h[0].At == "" {
			t.Fatalf("first change = %+v", h[0])
		}
		if *h[1].OldValue != `"Europe/London"` || h[1].NewValue != `"Asia/Tokyo"` || h[1].UpdatedBy.String() != b.ID {
			t.Fatalf("second change = %+v", h[1])
		}
		if got := s.String(ctx, &id, "space.time_zone"); got != "Asia/Tokyo" {
			t.Fatalf("value = %q", got)
		}
		if _, err := s.Set(ctx, a, &id, "space.time_zone", 7); err == nil {
			t.Fatal("a number was accepted for a text setting")
		}
	})
}
