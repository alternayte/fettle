package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/alternayte/fettle/internal/app"
	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/bots"
	"github.com/alternayte/fettle/internal/chats"
	"github.com/alternayte/fettle/internal/envelopes"
	"github.com/alternayte/fettle/internal/runs"
	"github.com/alternayte/fettle/internal/testenv"
	"github.com/alternayte/fettle/internal/usage"
	"github.com/alternayte/fettle/internal/vault"
	"github.com/google/uuid"
)

// storageSuite is the storage test suite of SCN-CORE-002. Every case runs on SQLite and PostgreSQL.
var storageSuite = []struct {
	name string
	run  func(t *testing.T, a *app.App, space uuid.UUID, bot *bots.Bot, chat uuid.UUID)
}{
	{"bot_versions", func(t *testing.T, a *app.App, sp uuid.UUID, b *bots.Bot, _ uuid.UUID) {
		ctx := context.Background()
		if _, err := a.Bots.Update(ctx, audit.System, sp, b.ID(), bots.Input{Name: b.Name, Job: "j", Instructions: "v2"}); err != nil {
			t.Fatal(err)
		}
		vs, err := a.Bots.Versions(ctx, sp, b.ID())
		if err != nil || len(vs) != 2 {
			t.Fatalf("versions %d %v", len(vs), err)
		}
	}},
	{"memory_limit", func(t *testing.T, a *app.App, sp uuid.UUID, b *bots.Bot, _ uuid.UUID) {
		id := b.ID()
		if _, err := a.Bots.SetMemory(context.Background(), audit.System, sp, &id, string(make([]byte, 5000)), "user"); err == nil {
			t.Fatal("5 KB memory accepted")
		}
	}},
	{"messages_page", func(t *testing.T, a *app.App, sp uuid.UUID, b *bots.Bot, chat uuid.UUID) {
		ctx := context.Background()
		for i := 0; i < 7; i++ {
			if err := a.Chats.Insert(ctx, &chats.Message{SpaceID: sp, ChatID: chat, AuthorKind: "user", AuthorID: "u", Body: "m"}, nil); err != nil {
				t.Fatal(err)
			}
		}
		page, next, err := a.Chats.Page(ctx, chat, nil, 5)
		if err != nil || len(page) != 5 || next == "" {
			t.Fatalf("page %d next %q %v", len(page), next, err)
		}
	}},
	{"settings", func(t *testing.T, a *app.App, sp uuid.UUID, _ *bots.Bot, _ uuid.UUID) {
		if _, err := a.Settings.Set(context.Background(), audit.System, &sp, "agent.max_turns", 9); err != nil {
			t.Fatal(err)
		}
		if a.Settings.Int(context.Background(), &sp, "agent.max_turns") != 9 {
			t.Fatal("setting not stored")
		}
	}},
	{"vault", func(t *testing.T, a *app.App, sp uuid.UUID, _ *bots.Bot, _ uuid.UUID) {
		it, err := a.Vault.Create(context.Background(), audit.System, sp, vault.Input{Name: "n", Kind: vault.KindSecret, Value: "value-1234"})
		if err != nil {
			t.Fatal(err)
		}
		if v, _ := a.Vault.Reveal(context.Background(), sp, it.ID()); v != "value-1234" {
			t.Fatal("vault round trip")
		}
	}},
	{"envelope_run_journal", func(t *testing.T, a *app.App, sp uuid.UUID, b *bots.Bot, chat uuid.UUID) {
		ctx := context.Background()
		env := &envelopes.Envelope{SpaceID: sp, Kind: "message", Origin: "user", OwnerBotID: ptr(b.ID()), ChatID: &chat, Goal: "g", Capabilities: []string{"read"}}
		var runID uuid.UUID
		err := a.DB.Engine.WithTx(ctx, func(ctx context.Context) error {
			if err := a.Envs.Create(ctx, env, nil); err != nil {
				return err
			}
			r := &runs.Run{SpaceID: sp, EnvelopeID: env.ID(), BotID: b.ID(), Kind: "owner"}
			if err := a.Runs.Create(ctx, r, "background"); err != nil {
				return err
			}
			runID = r.ID()
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		big := make([]byte, 20000)
		for i := range big {
			big[i] = 'x'
		}
		if _, err := a.Journal.Append(ctx, runID, runs.NoFence, nil, runs.New{Type: runs.EvToolCall, Data: map[string]string{"result": string(big)}}); err != nil {
			t.Fatal(err)
		}
		evs, err := a.Journal.Load(ctx, runID)
		if err != nil || len(evs) != 1 || len(evs[0].Data) < 20000 || evs[0].PayloadKey == nil {
			t.Fatalf("journal load: %v", err)
		}
	}},
	{"usage_view", func(t *testing.T, a *app.App, sp uuid.UUID, b *bots.Bot, _ uuid.UUID) {
		ctx := context.Background()
		id := b.ID()
		if err := a.Usage.Record(ctx, &usage.UsageEvent{SpaceID: sp, Kind: "model", BotID: &id, Role: "owner", ModelName: "m", TokensIn: 10, TokensOut: 5, Unit: "tokens", Quantity: 15, CostUSD: 0.5}); err != nil {
			t.Fatal(err)
		}
		rows, err := a.Usage.View(ctx, sp, "day", time.Now().Add(-time.Hour))
		if err != nil || len(rows) != 1 || rows[0].CostUSD != 0.5 {
			t.Fatalf("view %+v %v", rows, err)
		}
	}},
	{"search", func(t *testing.T, a *app.App, sp uuid.UUID, b *bots.Bot, _ uuid.UUID) {
		ctx := context.Background()
		if err := a.Chats.Index(ctx, b.ID(), "message", "r", "the walrus sings", time.Now()); err != nil {
			t.Fatal(err)
		}
		hits, _, err := a.Chats.Search(ctx, b.ID(), "walrus", 0, 5)
		if err != nil || len(hits) != 1 {
			t.Fatalf("search %d %v", len(hits), err)
		}
	}},
	{"audit_append_only_list", func(t *testing.T, a *app.App, sp uuid.UUID, _ *bots.Bot, _ uuid.UUID) {
		rows, err := a.Audit.List(context.Background(), audit.Filter{SpaceID: &sp, Action: "bot.create"})
		if err != nil || len(rows) == 0 {
			t.Fatalf("audit %d %v", len(rows), err)
		}
	}},
}

func ptr[T any](v T) *T { return &v }

func TestSCN_CORE_002_StorageSuiteOnBothDialects(t *testing.T) {
	counts := map[string]int{}
	for _, d := range testenv.Dialects {
		t.Run(d, func(t *testing.T) {
			cfg := testenv.Config(t, d)
			t.Setenv("FETTLE_NO_KEYCHAIN", "1")
			a, err := app.New(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(a.DB.Close)
			owner, _ := a.LocalOwner(context.Background())
			ms, _ := a.Spaces.ForUser(context.Background(), owner.ID())
			sp := ms[0].Space.ID()
			bot, _ := a.Bots.Primary(context.Background(), sp)
			chat, _ := a.Chats.DirectFor(context.Background(), sp, bot.ID())
			for _, c := range storageSuite {
				t.Run(c.name, func(t *testing.T) {
					c.run(t, a, sp, bot, chat.ID())
					if !t.Failed() {
						counts[d]++
					}
				})
			}
		})
	}
	if counts["sqlite"] != len(storageSuite) || counts["postgres"] != len(storageSuite) {
		t.Fatalf("passing cases: sqlite %d, postgres %d, suite %d", counts["sqlite"], counts["postgres"], len(storageSuite))
	}
}
