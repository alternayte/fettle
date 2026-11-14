package vault_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"testing"

	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/testenv"
	"github.com/alternayte/fettle/internal/vault"
)

func TestSealStreamRoundTripAndTamper(t *testing.T) {
	db, _ := testenv.Open(t, "sqlite")
	ctx := context.Background()
	sp := newSpace(t, db.Engine)
	v := vault.New(db.Engine, audit.New(db.Engine), masterKey(t))
	for _, n := range []int{0, 1, vault.SegmentSize, vault.SegmentSize + 1, 3*vault.SegmentSize + 17} {
		plain := make([]byte, n)
		_, _ = rand.Read(plain)
		var buf bytes.Buffer
		w, err := v.SealStream(ctx, sp, &buf, "ws:a:1")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(plain)
		_ = w.Close()
		sealed := buf.Bytes()
		r, _ := v.OpenStream(ctx, sp, bytes.NewReader(sealed), "ws:a:1")
		got, err := io.ReadAll(r)
		if err != nil || !bytes.Equal(got, plain) {
			t.Fatalf("size %d: round trip failed: %v", n, err)
		}
		if bytes.Contains(sealed, plain[:min(n, 32)]) && n >= 32 {
			t.Fatal("plaintext visible")
		}
		r, _ = v.OpenStream(ctx, sp, bytes.NewReader(sealed), "ws:a:2")
		if _, err := io.ReadAll(r); err == nil {
			t.Fatalf("size %d: another revision AAD decrypted", n)
		}
		if len(sealed) > 40 {
			bad := append([]byte(nil), sealed...)
			bad[len(bad)-5] ^= 1
			r, _ = v.OpenStream(ctx, sp, bytes.NewReader(bad), "ws:a:1")
			if _, err := io.ReadAll(r); err == nil {
				t.Fatalf("size %d: tampered archive decrypted", n)
			}
		}
		if n > vault.SegmentSize {
			cut := sealed[:len(sealed)-10]
			r, _ = v.OpenStream(ctx, sp, bytes.NewReader(cut), "ws:a:1")
			if _, err := io.ReadAll(r); err == nil {
				t.Fatalf("size %d: truncated archive decrypted", n)
			}
		}
	}
}
