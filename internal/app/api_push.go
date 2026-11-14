package app

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/auth"
	"github.com/alternayte/fettle/internal/push"
	"github.com/alternayte/fettle/internal/settings"
)

func init() {
	settings.Register(settings.Def{Key: "push.finished", Scope: "space", Default: false, Doc: "Send a push notification when a bot finishes work."})
}

// setupPush connects the agent's notifications to Web Push (§15.2).
func (a *App) setupPush() {
	a.Push = push.New(a.DB.Engine, a.Vault, a.HTTP, a.Log)
	if strings.HasPrefix(a.Cfg.BaseURL, "https://") {
		a.Push.Subscriber = a.Cfg.BaseURL
	}
	a.Push.Sent = func(_, result string) { a.Metrics.PushDeliveries.WithLabelValues(result).Inc() }
	a.Agent.Notify = func(ctx context.Context, spaceID uuid.UUID, kind, title, body, url string) {
		// Delivery never holds up a run.
		go a.Push.NotifySpace(context.WithoutCancel(ctx), spaceID, push.Notification{Type: kind, Title: title, Body: body, URL: url})
	}
	a.Agent.RoutineNotify = func(ctx context.Context, id uuid.UUID) string {
		var n string
		_ = a.DB.Engine.QueryRow(ctx, "SELECT notify FROM routines WHERE id = $1", id).Scan(&n)
		return n
	}
}

type pushSubBody struct {
	Endpoint string `json:"endpoint" minLength:"1" maxLength:"2000"`
	Keys     struct {
		P256dh string `json:"p256dh" minLength:"1" maxLength:"200"`
		Auth   string `json:"auth" minLength:"1" maxLength:"200"`
	} `json:"keys"`
}

func (a *App) registerPush(api huma.API) {
	huma.Register(api, op("push-key", http.MethodGet, "/push/key", "The VAPID public key that browsers subscribe with", "push"), func(ctx context.Context, _ *struct{}) (*struct {
		Body struct {
			PublicKey     string `json:"public_key"`
			Subscriptions int    `json:"subscriptions"`
		}
	}, error) {
		pub, _, err := a.Push.VAPID(ctx)
		if err != nil {
			return nil, err
		}
		n, _ := a.Push.Count(ctx, auth.From(ctx).UserID)
		out := &struct {
			Body struct {
				PublicKey     string `json:"public_key"`
				Subscriptions int    `json:"subscriptions"`
			}
		}{}
		out.Body.PublicKey, out.Body.Subscriptions = pub, n
		return out, nil
	})
	huma.Register(api, op("push-subscribe", http.MethodPost, "/push/subscriptions", "Store a push subscription of the caller", "push"), func(ctx context.Context, in *struct {
		UserAgent string `header:"User-Agent"`
		Body      pushSubBody
	}) (*struct{}, error) {
		err := a.Push.Subscribe(ctx, auth.From(ctx).UserID, in.Body.Endpoint, in.Body.Keys.P256dh, in.Body.Keys.Auth, in.UserAgent)
		if errors.Is(err, push.ErrBadSubscription) {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		return nil, err
	})
	huma.Register(api, op("push-unsubscribe", http.MethodPost, "/push/subscriptions/remove", "Remove a push subscription of the caller", "push"), func(ctx context.Context, in *struct {
		Body struct {
			Endpoint string `json:"endpoint" minLength:"1" maxLength:"2000"`
		}
	}) (*struct{}, error) {
		return nil, a.Push.Unsubscribe(ctx, auth.From(ctx).UserID, in.Body.Endpoint)
	})
}
