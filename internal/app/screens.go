package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/agent"
	"github.com/alternayte/fettle/internal/chats"
	"github.com/alternayte/fettle/internal/computers"
	"github.com/alternayte/fettle/internal/learning"
	"github.com/alternayte/fettle/internal/screens"
	"github.com/alternayte/fettle/internal/tools"
)

// setupScreens connects screen leases to the run loop (§10.8).
func (a *App) setupScreens() {
	a.Screens = screens.New(a.DB.Engine, func(ctx context.Context) ([]byte, error) {
		return a.Vault.InstanceSecret(ctx, "screen-token-key", screens.NewKey)
	})
	a.Agent.ScreenUse = func(ctx context.Context, spaceID, botID uuid.UUID) (func(), error) {
		release, err := a.Screens.BotUse(ctx, spaceID, botID)
		if errors.Is(err, screens.ErrControlled) {
			return nil, agent.ErrScreenControlled
		}
		return release, err
	}
}

// ScreenView is the screen of a bot for one person.
type ScreenView struct {
	BotID         string    `json:"bot_id"`
	ViewToken     string    `json:"view_token" doc:"Read-only stream token for /api/v1/screen/stream. Valid 10 minutes."`
	ViewExpiresAt time.Time `json:"view_expires_at"`
	Controller    string    `json:"controller" enum:"none,you,person,bot" doc:"Who uses the screen now."`
}

// ScreenControl is a control lease.
type ScreenControl struct {
	Token     string    `json:"token" doc:"Stream token with input. Valid while you hold the lease."`
	ExpiresAt time.Time `json:"expires_at"`
}

type ScreenPath struct {
	Space uuid.UUID `path:"space" doc:"Space ID"`
	Bot   uuid.UUID `path:"bot"`
}

func screenError(err error) error {
	switch {
	case errors.Is(err, screens.ErrHeld), errors.Is(err, screens.ErrBotBusy):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, errNoScreens), errors.Is(err, tools.ErrNoZone):
		return huma.Error409Conflict(err.Error())
	}
	return apiError(err)
}

var errNoScreens = errors.New("the zone of this bot has no screens; set the zone size to desktop")

// botScreen leases the computer of a bot's zone and shows the bot's browser on its screen.
func (a *App) botScreen(ctx context.Context, spaceID, botID uuid.UUID) (*computers.Lease, error) {
	b, err := a.Bots.Get(ctx, spaceID, botID)
	if err != nil {
		return nil, err
	}
	if b.ZoneID == nil {
		return nil, tools.ErrNoZone
	}
	l, err := a.Computers.Acquire(ctx, *b.ZoneID)
	if err != nil {
		return nil, err
	}
	if !l.Screens {
		l.Release()
		return nil, errNoScreens
	}
	if err := a.Computers.EnsureBotHome(ctx, l, botID); err != nil {
		l.Release()
		return nil, err
	}
	if err := l.Client.BrowserOpen(ctx, tools.BrowserRequest(l, botID)); err != nil {
		l.Release()
		return nil, err
	}
	return l, nil
}

func (a *App) controller(ctx context.Context, botID, userID uuid.UUID) string {
	h, err := a.Screens.Holder(ctx, botID)
	switch {
	case err != nil || h == nil:
		return "none"
	case h.HolderKind == "bot":
		return "bot"
	case h.HolderID == userID.String():
		return "you"
	}
	return "person"
}

func (a *App) registerScreens(api huma.API) {
	huma.Register(api, op("open-screen", http.MethodPost, "/spaces/{space}/bots/{bot}/screen", "Open the screen of a bot read-only. It starts the screen when it is not running.", "screens"), func(ctx context.Context, in *ScreenPath) (*struct{ Body ScreenView }, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, false)
		if err != nil {
			return nil, err
		}
		l, err := a.botScreen(ctx, in.Space, in.Bot)
		if err != nil {
			return nil, screenError(err)
		}
		l.Release()
		tok, exp, err := a.Screens.View(ctx, in.Space, in.Bot, p.UserID)
		if err != nil {
			return nil, err
		}
		return &struct{ Body ScreenView }{Body: ScreenView{BotID: in.Bot.String(), ViewToken: tok, ViewExpiresAt: exp, Controller: a.controller(ctx, in.Bot, p.UserID)}}, nil
	})

	huma.Register(api, op("control-screen", http.MethodPost, "/spaces/{space}/bots/{bot}/screen/control", "Take or renew the control lease of a bot's screen (15 minutes). The bot waits while you control it.", "screens"), func(ctx context.Context, in *ScreenPath) (*struct{ Body ScreenControl }, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, false)
		if err != nil {
			return nil, err
		}
		if _, err := a.Bots.Get(ctx, in.Space, in.Bot); err != nil {
			return nil, apiError(err)
		}
		before := a.controller(ctx, in.Bot, p.UserID)
		tok, exp, err := a.Screens.Control(ctx, in.Space, in.Bot, p.UserID)
		if err != nil {
			return nil, screenError(err)
		}
		if before != "you" {
			if err := a.Audit.Record(ctx, actorOf(p), &in.Space, "screen.control", "bot:"+in.Bot.String(), nil); err != nil {
				return nil, err
			}
			if !a.takeoverAsked(ctx, in.Space, in.Bot) {
				a.signalTakeover(ctx, in.Space, in.Bot, p.UserID.String())
			}
		}
		return &struct{ Body ScreenControl }{Body: ScreenControl{Token: tok, ExpiresAt: exp}}, nil
	})

	huma.Register(api, op("screen-done", http.MethodPost, "/spaces/{space}/bots/{bot}/screen/done", "Return control of a bot's screen. With a takeover card, it answers the card and the bot continues.", "screens"), func(ctx context.Context, in *struct {
		ScreenPath
		Body struct {
			CardID string `json:"card_id,omitempty"`
			Note   string `json:"note,omitempty" maxLength:"2000" doc:"What you did, for the bot."`
		}
	}) (*struct{}, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, false)
		if err != nil {
			return nil, err
		}
		held, err := a.Screens.Release(ctx, in.Bot, p.UserID)
		if err != nil {
			return nil, err
		}
		if held {
			if err := a.Audit.Record(ctx, actorOf(p), &in.Space, "screen.done", "bot:"+in.Bot.String(), nil); err != nil {
				return nil, err
			}
		}
		if in.Body.CardID != "" {
			msg, err := a.Chats.FindCard(ctx, in.Space, in.Body.CardID)
			if err != nil {
				return nil, apiError(err)
			}
			if msg.Card.Kind != "takeover" || msg.Card.Data["bot_id"] != in.Bot.String() {
				return nil, huma.Error422UnprocessableEntity("this card is not a takeover request of this bot")
			}
			if msg.Card.Status == "pending" {
				if _, err := a.Agent.AnswerCard(ctx, actorOf(p), in.Space, in.Body.CardID, "done", in.Body.Note); err != nil && !errors.Is(err, chats.ErrCardAnswered) {
					return nil, apiError(err)
				}
			}
		}
		return nil, a.Agent.WakeScreenWaiters(ctx, in.Bot)
	})
}

// screenStream bridges the browser's VNC client to the bot's screen in its computer. The
// token is the authorization, because a browser cannot set headers on a websocket.
func (a *App) screenStream(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tok := r.URL.Query().Get("t")
	c, err := a.Screens.Check(ctx, tok)
	if err != nil {
		http.Error(w, screens.ErrToken.Error(), http.StatusUnauthorized)
		return
	}
	b, err := a.Bots.Get(ctx, c.Space, c.Bot)
	if err != nil || b.ZoneID == nil {
		http.Error(w, "the bot has no computer", http.StatusNotFound)
		return
	}
	l, err := a.Computers.Acquire(ctx, *b.ZoneID)
	if err != nil {
		http.Error(w, "the computer is not available: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer l.Release()
	vnc, err := l.Client.ScreenDial(ctx, c.Bot.String(), c.Control)
	if err != nil {
		http.Error(w, "the screen is not running: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = vnc.Close() }()
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"binary"}})
	if err != nil {
		return
	}
	ws.SetReadLimit(4 << 20)
	sctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	conn := websocket.NetConn(sctx, ws, websocket.MessageBinary)
	defer func() { _ = conn.Close() }()
	go func() {
		_, _ = io.Copy(vnc, conn)
		cancel()
	}()
	go func() {
		_, _ = io.Copy(conn, vnc)
		cancel()
	}()
	if !c.Control {
		<-sctx.Done()
		return
	}
	// Input stops when the person returns control or the lease expires.
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-sctx.Done():
			return
		case <-t.C:
			h, err := a.Screens.Holder(sctx, c.Bot)
			if err == nil && (h == nil || h.HolderKind != "person" || h.HolderID != c.User.String()) {
				_ = ws.Close(websocket.StatusPolicyViolation, "control returned")
				return
			}
		}
	}
}

// takeoverAsked reports whether the bot asked for a takeover with a card that waits.
func (a *App) takeoverAsked(ctx context.Context, spaceID, botID uuid.UUID) bool {
	cards, err := a.Chats.PendingCards(ctx, spaceID)
	if err != nil {
		return true // without the answer, record no signal
	}
	for _, m := range cards {
		if m.Card != nil && m.Card.Kind == "takeover" && m.Card.Data["bot_id"] == botID.String() {
			return true
		}
	}
	return false
}

// signalTakeover records an unrequested takeover on the bot's newest owner run (§14.1).
func (a *App) signalTakeover(ctx context.Context, spaceID, botID uuid.UUID, userID string) {
	in := learning.Input{SpaceID: spaceID, BotID: &botID, Type: learning.UnrequestedTakeover, Ref: "takeover:" + uuid.NewString(), UserID: userID, Detail: "A person took control of the screen without a request."}
	var runID uuid.UUID
	if err := a.DB.Engine.QueryRow(ctx, "SELECT id FROM runs WHERE bot_id = $1 AND space_id = $2 AND kind = 'owner' ORDER BY created_at DESC LIMIT 1", botID, spaceID).Scan(&runID); err == nil {
		in.RunID = &runID
	}
	a.Agent.Signal(ctx, in)
}
