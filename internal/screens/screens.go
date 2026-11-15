// Package screens issues screen view tokens and control leases (§10.8).
package screens

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/alternayte/drel"
	"github.com/google/uuid"
)

// Lifetimes from §10.8.
const (
	ViewTTL    = 10 * time.Minute
	ControlTTL = 15 * time.Minute
	// BotTTL bounds one browser action of a bot; the bot releases the screen when the
	// action ends.
	BotTTL = 10 * time.Minute
)

var (
	// ErrHeld means another person controls the screen.
	ErrHeld = errors.New("another person controls this screen")
	// ErrBotBusy means the bot's run uses the screen now.
	ErrBotBusy = errors.New("the bot uses this screen now; wait until its action ends")
	// ErrControlled means a person controls the screen, so the bot must wait.
	ErrControlled = errors.New("a person controls the screen")
	// ErrToken means the token is not valid or has expired.
	ErrToken = errors.New("the screen link has expired; open the screen again")
)

// Claims is the content of a screen token.
type Claims struct {
	Space   uuid.UUID `json:"s"`
	Bot     uuid.UUID `json:"b"`
	User    uuid.UUID `json:"u"`
	Control bool      `json:"c,omitempty"`
	Exp     int64     `json:"e"`
}

// Service issues tokens and keeps leases.
type Service struct {
	e    *drel.Engine
	load func(ctx context.Context) ([]byte, error)
	mu   sync.Mutex
	key  []byte
	now  func() time.Time
}

// New returns the service. load returns the key that signs the tokens; the service
// calls it once, on first use.
func New(e *drel.Engine, load func(ctx context.Context) ([]byte, error)) *Service {
	return &Service{e: e, load: load, now: time.Now}
}

func (s *Service) keyOf(ctx context.Context) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key == nil {
		k, err := s.load(ctx)
		if err != nil {
			return nil, err
		}
		s.key = k
	}
	return s.key, nil
}

// SetClock replaces the clock (tests).
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// NewKey makes a signing key.
func NewKey() ([]byte, error) {
	k := make([]byte, 32)
	_, err := rand.Read(k)
	return k, err
}

func (s *Service) sign(ctx context.Context, c Claims) (string, error) {
	key, err := s.keyOf(ctx)
	if err != nil {
		return "", err
	}
	body, _ := json.Marshal(c)
	m := hmac.New(sha256.New, key)
	m.Write(body)
	enc := base64.RawURLEncoding
	return enc.EncodeToString(body) + "." + enc.EncodeToString(m.Sum(nil)), nil
}

// View returns a read-only token for the screen of a bot.
func (s *Service) View(ctx context.Context, space, bot, user uuid.UUID) (string, time.Time, error) {
	exp := s.now().Add(ViewTTL)
	tok, err := s.sign(ctx, Claims{Space: space, Bot: bot, User: user, Exp: exp.Unix()})
	return tok, exp, err
}

// acquire takes the lease of a bot's screen for a holder, when the screen is free or the
// holder has it already. It reports false when someone else holds it.
func (s *Service) acquire(ctx context.Context, space, bot uuid.UUID, kind, holder string, ttl time.Duration) (bool, error) {
	now := s.now().UTC()
	n, err := s.e.Exec(ctx, `INSERT INTO screen_leases (id, bot_id, space_id, holder_kind, holder_id, expires_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (bot_id) DO UPDATE SET holder_kind = excluded.holder_kind, holder_id = excluded.holder_id,
			expires_at = excluded.expires_at, updated_at = excluded.updated_at
		WHERE screen_leases.expires_at < excluded.created_at
			OR (screen_leases.holder_kind = excluded.holder_kind AND screen_leases.holder_id = excluded.holder_id)`,
		uuid.Must(uuid.NewV7()), bot, space, kind, holder, now.Add(ttl), now, now)
	return n == 1, err
}

// Holder returns the live lease of a bot's screen, or nil.
func (s *Service) Holder(ctx context.Context, bot uuid.UUID) (*Lease, error) {
	l, err := drel.NewRepository(s.e, LeaseMeta).Where(Leases.BotID.Eq(bot)).FirstOrNil(ctx)
	if err != nil || l == nil {
		return nil, err
	}
	if !l.ExpiresAt.After(s.now()) {
		return nil, nil
	}
	return l, nil
}

// Control gives a person the control lease of a bot's screen, or renews it. It refuses
// while another person holds it or while the bot acts on the screen.
func (s *Service) Control(ctx context.Context, space, bot, user uuid.UUID) (string, time.Time, error) {
	ok, err := s.acquire(ctx, space, bot, "person", user.String(), ControlTTL)
	if err != nil {
		return "", time.Time{}, err
	}
	if !ok {
		l, err := s.Holder(ctx, bot)
		if err != nil {
			return "", time.Time{}, err
		}
		if l != nil && l.HolderKind == "bot" {
			return "", time.Time{}, ErrBotBusy
		}
		return "", time.Time{}, ErrHeld
	}
	exp := s.now().Add(ControlTTL)
	tok, err := s.sign(ctx, Claims{Space: space, Bot: bot, User: user, Control: true, Exp: exp.Unix()})
	return tok, exp, err
}

// Release ends the control lease of a person. It reports whether the person held it.
func (s *Service) Release(ctx context.Context, bot, user uuid.UUID) (bool, error) {
	n, err := s.e.Exec(ctx, "DELETE FROM screen_leases WHERE bot_id = $1 AND holder_kind = 'person' AND holder_id = $2", bot, user.String())
	return n > 0, err
}

// BotUse takes the screen for one action of the bot. It returns ErrControlled while a
// person controls the screen. The release function frees the screen.
func (s *Service) BotUse(ctx context.Context, space, bot uuid.UUID) (func(), error) {
	ok, err := s.acquire(ctx, space, bot, "bot", bot.String(), BotTTL)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrControlled
	}
	return func() {
		_, _ = s.e.Exec(context.WithoutCancel(ctx), "DELETE FROM screen_leases WHERE bot_id = $1 AND holder_kind = 'bot'", bot)
	}, nil
}

// Check reads a token. A view token is valid until it expires; a control token is valid
// while its person still holds the lease.
func (s *Service) Check(ctx context.Context, token string) (Claims, error) {
	var c Claims
	body, sig, ok := strings.Cut(token, ".")
	if !ok {
		return c, ErrToken
	}
	enc := base64.RawURLEncoding
	raw, err1 := enc.DecodeString(body)
	got, err2 := enc.DecodeString(sig)
	if err1 != nil || err2 != nil {
		return c, ErrToken
	}
	key, err := s.keyOf(ctx)
	if err != nil {
		return c, err
	}
	m := hmac.New(sha256.New, key)
	m.Write(raw)
	if !hmac.Equal(got, m.Sum(nil)) || json.Unmarshal(raw, &c) != nil {
		return c, ErrToken
	}
	if s.now().Unix() >= c.Exp {
		return c, ErrToken
	}
	if c.Control {
		l, err := s.Holder(ctx, c.Bot)
		if err != nil {
			return c, err
		}
		if l == nil || l.HolderKind != "person" || l.HolderID != c.User.String() {
			return c, ErrToken
		}
	}
	return c, nil
}
