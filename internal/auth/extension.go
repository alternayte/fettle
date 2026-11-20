package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/alternayte/drel"
	"github.com/google/uuid"
)

// Scopes of a server-mode access token. A key from before scopes holds ScopeAll.
const (
	ScopeAll         = "all"
	ScopeShareSignIn = "share-sign-in"
)

// ExtensionPrefix starts every local-mode extension token.
const ExtensionPrefix = "fext_"

// PairingTTL is how long a pairing code works.
const PairingTTL = 10 * time.Minute

// ErrPairingCode means that a pairing code is wrong, used or expired.
var ErrPairingCode = errors.New("the pairing code is wrong or expired; start the pairing again in Fettle")

// ErrNoExtensionToken means that an extension token does not exist for this account.
var ErrNoExtensionToken = errors.New("extension token not found")

// Extensions holds the pairing codes and the local-mode tokens of the Chrome extension.
type Extensions struct {
	e   *drel.Engine
	now func() time.Time
}

// NewExtensions returns the extension store.
func NewExtensions(e *drel.Engine) *Extensions {
	return &Extensions{e: e, now: time.Now}
}

func hashOf(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// codeAlphabet has no 0, 1, I or O, so a person can type a code without a mistake.
const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// normalizeCode accepts a code with spaces, dashes and small letters.
func normalizeCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(code) {
		if strings.ContainsRune(codeAlphabet, r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// NewPairing creates a one-time code for an account. The code has 60 bits, in three groups
// of four characters.
func (x *Extensions) NewPairing(ctx context.Context, userID uuid.UUID) (string, time.Time, error) {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}
	var b strings.Builder
	for i, v := range raw {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		b.WriteByte(codeAlphabet[int(v)%len(codeAlphabet)])
	}
	code := b.String()
	exp := x.now().UTC().Add(PairingTTL)
	if _, err := x.e.Exec(ctx, "DELETE FROM extension_pairings WHERE expires_at < $1", x.now().UTC()); err != nil {
		return "", time.Time{}, err
	}
	err := x.e.WithTx(ctx, func(ctx context.Context) error {
		drel.Repo(drel.MustFromContext(ctx), ExtensionPairingMeta).Add(&ExtensionPairing{UserID: userID, Hash: hashOf(normalizeCode(code)), ExpiresAt: exp})
		return nil
	})
	return code, exp, err
}

// Claim uses a pairing code once and returns its account.
func (x *Extensions) Claim(ctx context.Context, code string) (uuid.UUID, error) {
	h := hashOf(normalizeCode(code))
	var userID uuid.UUID
	var exp time.Time
	if err := x.e.QueryRow(ctx, "SELECT user_id, expires_at FROM extension_pairings WHERE hash = $1", h).Scan(&userID, &exp); err != nil {
		return uuid.Nil, ErrPairingCode
	}
	n, err := x.e.Exec(ctx, "DELETE FROM extension_pairings WHERE hash = $1", h)
	if err != nil {
		return uuid.Nil, err
	}
	// Two claims of one code race on the delete; only the one that removed the row wins.
	if n != 1 || x.now().After(exp) {
		return uuid.Nil, ErrPairingCode
	}
	return userID, nil
}

// CreateToken creates a local-mode extension token and returns its plaintext once.
func (x *Extensions) CreateToken(ctx context.Context, userID uuid.UUID) (string, *ExtensionToken, error) {
	raw := make([]byte, 30)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	plain := ExtensionPrefix + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw))
	t := &ExtensionToken{UserID: userID, Hash: hashOf(plain), Last4: plain[len(plain)-4:]}
	err := x.e.WithTx(ctx, func(ctx context.Context) error {
		drel.Repo(drel.MustFromContext(ctx), ExtensionTokenMeta).Add(t)
		return nil
	})
	return plain, t, err
}

// Check returns the token row of a plaintext token, or ErrNoExtensionToken.
func (x *Extensions) Check(ctx context.Context, plain string) (*ExtensionToken, error) {
	if !strings.HasPrefix(plain, ExtensionPrefix) {
		return nil, ErrNoExtensionToken
	}
	t, err := drel.NewRepository(x.e, ExtensionTokenMeta).Where(ExtensionTokens.Hash.Eq(hashOf(plain))).FirstOrNil(ctx)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, ErrNoExtensionToken
	}
	now := x.now().UTC()
	if t.LastUsedAt == nil || now.Sub(*t.LastUsedAt) > time.Minute {
		_, _ = x.e.Exec(ctx, "UPDATE extension_tokens SET last_used_at = $1 WHERE id = $2", now, t.ID())
		t.LastUsedAt = &now
	}
	return t, nil
}

// List returns the extension tokens of an account, newest first.
func (x *Extensions) List(ctx context.Context, userID uuid.UUID) ([]*ExtensionToken, error) {
	return drel.NewRepository(x.e, ExtensionTokenMeta).Where(ExtensionTokens.UserID.Eq(userID)).OrderBy(ExtensionTokens.CreatedAt.Desc()).All(ctx)
}

// Revoke deletes an extension token of an account.
func (x *Extensions) Revoke(ctx context.Context, userID, id uuid.UUID) error {
	n, err := x.e.Exec(ctx, "DELETE FROM extension_tokens WHERE id = $1 AND user_id = $2", id, userID)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNoExtensionToken
	}
	return nil
}
