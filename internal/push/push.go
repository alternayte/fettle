// Package push sends Web Push notifications with VAPID (§15.2). A notification opens the
// exact card or run.
package push

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/SherClockHolmes/webpush-go"
	"github.com/alternayte/drel"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/vault"
)

// Notification types (§15.2).
const (
	TypeApproval = "approval"
	TypeQuestion = "question"
	TypeTakeover = "takeover"
	TypeFinished = "finished"
	TypeFailed   = "failed"
	TypeBudget   = "budget"
)

// Notification is the payload that the service worker shows.
type Notification struct {
	Type  string `json:"type"`
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"` // path inside the app: the card or the run
	Tag   string `json:"tag,omitempty"`
}

// Service stores subscriptions and sends notifications.
type Service struct {
	e     *drel.Engine
	vault *vault.Vault
	http  *http.Client
	// Subscriber names the sender for push services (mailto: or the server URL).
	Subscriber string
	log        *slog.Logger
	// Sent counts deliveries by type and result, for /metrics.
	Sent func(kind, result string)
}

// New returns the push service.
func New(e *drel.Engine, v *vault.Vault, client *http.Client, log *slog.Logger) *Service {
	return &Service{e: e, vault: v, http: client, log: log, Subscriber: "mailto:fettle@localhost"}
}

type keys struct {
	Private string `json:"private"`
	Public  string `json:"public"`
}

// VAPID returns the instance key pair; the first call makes it.
func (s *Service) VAPID(ctx context.Context) (public, private string, err error) {
	raw, err := s.vault.InstanceSecret(ctx, "vapid", func() ([]byte, error) {
		priv, pub, err := webpush.GenerateVAPIDKeys()
		if err != nil {
			return nil, err
		}
		return json.Marshal(keys{Private: priv, Public: pub})
	})
	if err != nil {
		return "", "", err
	}
	var k keys
	if err := json.Unmarshal(raw, &k); err != nil {
		return "", "", err
	}
	return k.Public, k.Private, nil
}

// ErrBadSubscription means the browser sent an incomplete subscription.
var ErrBadSubscription = errors.New("the push subscription needs an https endpoint and its keys")

// Subscribe stores a subscription of a user. The same endpoint moves to the newest user.
func (s *Service) Subscribe(ctx context.Context, userID uuid.UUID, endpoint, p256dh, auth, ua string) error {
	if !strings.HasPrefix(endpoint, "https://") && !strings.HasPrefix(endpoint, "http://") || p256dh == "" || auth == "" {
		return ErrBadSubscription
	}
	return s.e.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		if _, err := tx.Exec(ctx, "DELETE FROM push_subscriptions WHERE endpoint = $1", endpoint); err != nil {
			return err
		}
		drel.Repo(tx, SubscriptionMeta).Add(&Subscription{UserID: userID, Endpoint: endpoint, P256dh: p256dh, Auth: auth, UserAgent: ua})
		return nil
	})
}

// Unsubscribe removes a subscription of a user.
func (s *Service) Unsubscribe(ctx context.Context, userID uuid.UUID, endpoint string) error {
	_, err := s.e.Exec(ctx, "DELETE FROM push_subscriptions WHERE endpoint = $1 AND user_id = $2", endpoint, userID)
	return err
}

// Count returns the subscriptions of a user.
func (s *Service) Count(ctx context.Context, userID uuid.UUID) (int, error) {
	var n int
	err := s.e.QueryRow(ctx, "SELECT COUNT(*) FROM push_subscriptions WHERE user_id = $1", userID).Scan(&n)
	return n, err
}

// NotifySpace sends a notification to every subscription of the members of a space.
func (s *Service) NotifySpace(ctx context.Context, spaceID uuid.UUID, n Notification) {
	rows, err := s.e.Query(ctx, "SELECT user_id FROM space_members WHERE space_id = $1", spaceID)
	if err != nil {
		s.log.Warn("push: list members", "error", err)
		return
	}
	var users []uuid.UUID
	for rows.Next() {
		var u uuid.UUID
		if rows.Scan(&u) == nil {
			users = append(users, u)
		}
	}
	rows.Close()
	if len(users) == 0 {
		return
	}
	subs, err := drel.NewRepository(s.e, SubscriptionMeta).Where(Subscriptions.UserID.In(users...)).All(ctx)
	if err != nil {
		s.log.Warn("push: list subscriptions", "error", err)
		return
	}
	if len(subs) == 0 {
		return
	}
	pub, priv, err := s.VAPID(ctx)
	if err != nil {
		s.log.Warn("push: VAPID keys", "error", err)
		return
	}
	body, _ := json.Marshal(n)
	for _, sub := range subs {
		resp, err := webpush.SendNotificationWithContext(ctx, body, &webpush.Subscription{Endpoint: sub.Endpoint, Keys: webpush.Keys{P256dh: sub.P256dh, Auth: sub.Auth}}, &webpush.Options{
			HTTPClient: s.http, Subscriber: s.Subscriber, VAPIDPublicKey: pub, VAPIDPrivateKey: priv, TTL: 3600, Urgency: webpush.UrgencyHigh,
		})
		result := "ok"
		switch {
		case err != nil:
			result = "error"
			s.log.Warn("push: delivery failed", "error", err.Error())
		default:
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
				result = "gone"
				_, _ = s.e.Exec(ctx, "DELETE FROM push_subscriptions WHERE id = $1", sub.ID())
			} else if resp.StatusCode/100 != 2 {
				result = fmt.Sprintf("http_%d", resp.StatusCode)
			}
		}
		if s.Sent != nil {
			s.Sent(n.Type, result)
		}
	}
}
