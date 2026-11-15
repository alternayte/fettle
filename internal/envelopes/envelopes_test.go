package envelopes_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/envelopes"
	"github.com/alternayte/fettle/internal/testenv"
)

func TestSCN_TEAM_001_InboxSourcesAndChildLimits(t *testing.T) {
	testenv.Each(t, func(t *testing.T, dialect string) {
		db, _ := testenv.Open(t, dialect)
		ctx := context.Background()
		svc := envelopes.New(db.Engine)
		var space uuid.UUID
		if err := db.Engine.QueryRow(ctx, "SELECT id FROM spaces LIMIT 1").Scan(&space); err != nil {
			space = uuid.New()
			if _, err := db.Engine.Exec(ctx, "INSERT INTO spaces (id, name, time_zone, created_at, updated_at) VALUES ($1, $2, $3, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)", space, "Acme", "UTC"); err != nil {
				t.Fatal(err)
			}
		}
		bot := uuid.New()
		budget := envelopes.Budget{USD: 2, Tokens: 1000, WallMS: 60000, HopsRemaining: 4, MaxSubagents: 5}
		root := &envelopes.Envelope{SpaceID: space, Kind: "message", Origin: "user", Requester: envelopes.Requester{Type: "user", ID: "u1"}, OwnerBotID: &bot, Goal: "Plan the week.", Capabilities: envelopes.AllClasses[:5], Budget: budget}
		if err := svc.Create(ctx, root, nil); err != nil {
			t.Fatalf("message: %v", err)
		}
		if root.RootID != root.ID() || len(root.Acceptance) != 1 || root.Acceptance[0] != envelopes.DefaultCheck {
			t.Fatalf("root %+v", root)
		}
		for _, src := range []struct{ kind, origin string }{{"routine", "routine"}, {"webhook", "webhook"}} {
			e := &envelopes.Envelope{SpaceID: space, Kind: src.kind, Origin: src.origin, Requester: envelopes.Requester{Type: "routine", ID: "r1"}, OwnerBotID: &bot, Goal: "Send the digest.", Capabilities: []string{envelopes.ClassRead}, Budget: budget}
			if err := svc.Create(ctx, e, nil); err != nil {
				t.Fatalf("%s: %v", src.kind, err)
			}
		}
		parent := &envelopes.Remainder{Budget: envelopes.Budget{USD: 1, Tokens: 500, WallMS: 60000, HopsRemaining: 3, MaxSubagents: 5}, Capabilities: root.Capabilities, SpaceID: space}
		step := "fanout"
		for _, kind := range []string{"handoff", "part", "verify"} {
			c := &envelopes.Envelope{ParentID: ptr(root.ID()), RootID: root.ID(), SpaceID: space, Kind: kind, Origin: "bot", Requester: envelopes.Requester{Type: "bot", ID: bot.String()}, OwnerBotID: &bot, Goal: "Part of the plan.", Capabilities: []string{envelopes.ClassRead}, Budget: envelopes.Budget{USD: 0.5, Tokens: 100, WallMS: 1000, HopsRemaining: 2}}
			if kind != "handoff" {
				c.Step = &step
			}
			if err := svc.Create(ctx, c, parent); err != nil {
				t.Fatalf("%s: %v", kind, err)
			}
		}
		over := &envelopes.Envelope{ParentID: ptr(root.ID()), RootID: root.ID(), SpaceID: space, Kind: "handoff", Origin: "bot", Requester: envelopes.Requester{Type: "bot", ID: bot.String()}, OwnerBotID: &bot, Goal: "Too big.", Capabilities: []string{envelopes.ClassRead}, Budget: envelopes.Budget{USD: 1.5, HopsRemaining: 1}}
		err := svc.Create(ctx, over, parent)
		var ve *envelopes.ValidationError
		if !errors.As(err, &ve) || !strings.Contains(err.Error(), "above what is left") {
			t.Fatalf("a child above the remainder: %v", err)
		}
		wider := &envelopes.Envelope{ParentID: ptr(root.ID()), RootID: root.ID(), SpaceID: space, Kind: "handoff", Origin: "bot", Requester: envelopes.Requester{Type: "bot", ID: bot.String()}, OwnerBotID: &bot, Goal: "Pay it.", Capabilities: []string{envelopes.ClassSpend}, Budget: envelopes.Budget{USD: 0.1}}
		if err := svc.Create(ctx, wider, &envelopes.Remainder{Budget: parent.Budget, Capabilities: []string{envelopes.ClassRead}, SpaceID: space}); !errors.As(err, &ve) {
			t.Fatalf("a child with a capability the parent lacks: %v", err)
		}
	})
}

func ptr[T any](v T) *T { return &v }
