package vault_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"log/slog"
	"strings"
	"testing"

	"github.com/alternayte/drel"
	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/spaces"
	"github.com/alternayte/fettle/internal/testenv"
	"github.com/alternayte/fettle/internal/vault"
	"github.com/google/uuid"
)

func newSpace(t *testing.T, e *drel.Engine) uuid.UUID {
	t.Helper()
	sp := &spaces.Space{Name: "acme", TimeZone: "UTC"}
	if err := e.WithTx(context.Background(), func(ctx context.Context) error {
		drel.Repo(drel.MustFromContext(ctx), spaces.SpaceMeta).Add(sp)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return sp.ID()
}

func masterKey(t *testing.T) []byte {
	k, err := vault.ParseMasterKey(testenv.TestMasterKey)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

var owner = audit.Actor{Kind: "user", ID: uuid.NewString()}

func TestSCN_VLT_001_LoginStoredAsCiphertext(t *testing.T) {
	testenv.Each(t, func(t *testing.T, dialect string) {
		db, _ := testenv.Open(t, dialect)
		ctx := context.Background()
		var logs bytes.Buffer
		prev := slog.Default()
		slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
		t.Cleanup(func() { slog.SetDefault(prev) })

		space := newSpace(t, db.Engine)
		v := vault.New(db.Engine, audit.New(db.Engine), masterKey(t))
		const password = "hunter2-correct-horse"
		item, err := v.Create(ctx, owner, space, vault.Input{Name: "shop", Kind: vault.KindLogin, Site: "https://shop.test", Username: "owner@acme.test", Value: password})
		if err != nil {
			t.Fatal(err)
		}
		if item.Last4 != "orse" {
			t.Fatalf("last4 = %q", item.Last4)
		}

		// No column of any row holds the password or the raw data key.
		cols := map[string]string{
			"vault_items": "name || '|' || site || '|' || username || '|' || ciphertext || '|' || nonce || '|' || last4",
			"data_keys":   "wrapped_key",
			"audit_log":   "action || '|' || target || '|' || CAST(details AS TEXT)",
		}
		for table, expr := range cols {
			rows, err := db.Engine.Query(ctx, "SELECT "+expr+" FROM "+table)
			if err != nil {
				t.Fatal(err)
			}
			n := 0
			for rows.Next() {
				n++
				var s string
				if err := rows.Scan(&s); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(s, password) || strings.Contains(s, base64.StdEncoding.EncodeToString([]byte(password))) {
					t.Fatalf("%s holds the password in plain text", table)
				}
			}
			rows.Close()
			if n == 0 {
				t.Fatalf("%s has no rows", table)
			}
		}

		// The data key is wrapped: another master key cannot read the item.
		other := vault.New(db.Engine, audit.New(db.Engine), vault.NewKey())
		if _, err := other.Reveal(ctx, space, item.ID()); err == nil {
			t.Fatal("a different master key decrypted the item")
		}
		got, err := v.Reveal(ctx, space, item.ID())
		if err != nil || got != password {
			t.Fatalf("reveal = %q, %v", got, err)
		}
		if strings.Contains(logs.String(), password) {
			t.Fatal("a log line holds the password")
		}
		// Redaction replaces the value with its name.
		if s := v.Redactor.String(ctx, space, "token is "+password+"."); s != "token is [secret:shop]." {
			t.Fatalf("redacted = %q", s)
		}
	})
}

func TestSCN_VLT_005_RekeyRewrapsDataKeys(t *testing.T) {
	testenv.Each(t, func(t *testing.T, dialect string) {
		db, _ := testenv.Open(t, dialect)
		ctx := context.Background()
		a := masterKey(t)
		b := vault.NewKey()
		v := vault.New(db.Engine, audit.New(db.Engine), a)
		var ids []uuid.UUID
		var sps []uuid.UUID
		for i := 0; i < 3; i++ {
			sp := newSpace(t, db.Engine)
			it, err := v.Create(ctx, owner, sp, vault.Input{Name: "k", Kind: vault.KindSecret, Value: "value-" + sp.String()})
			if err != nil {
				t.Fatal(err)
			}
			ids, sps = append(ids, it.ID()), append(sps, sp)
		}
		n, err := v.Rekey(ctx, b)
		if err != nil || n != 3 {
			t.Fatalf("rekey = %d, %v", n, err)
		}
		withB := vault.New(db.Engine, audit.New(db.Engine), b)
		withA := vault.New(db.Engine, audit.New(db.Engine), a)
		for i := range ids {
			if got, err := withB.Reveal(ctx, sps[i], ids[i]); err != nil || got != "value-"+sps[i].String() {
				t.Fatalf("item %d with B: %q, %v", i, got, err)
			}
			if _, err := withA.Reveal(ctx, sps[i], ids[i]); err == nil {
				t.Fatalf("item %d still decrypts with A", i)
			}
		}
	})
}

