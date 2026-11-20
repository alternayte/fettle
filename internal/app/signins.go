package app

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"golang.org/x/net/publicsuffix"

	"github.com/alternayte/fettle/agentd/client"
	"github.com/alternayte/fettle/internal/agent"
	"github.com/alternayte/fettle/internal/auth"
	"github.com/alternayte/fettle/internal/computers"
	"github.com/alternayte/fettle/internal/tools"
)

// A person shares a sign-in for one site from their own Chrome with one bot
// (docs/specs/share-a-sign-in.md). The Fettle extension sends the cookies and the local
// storage of the site; agentd puts them into the bot's browser profile, and Fettle keeps no
// copy.

//go:embed pair.html
var pairPage []byte

// shareMaxCookies bounds one share; a site with more cookies is not a sign-in.
const shareMaxCookies = 400

// SiteDomain returns the registrable domain of a host ("github.com" for "api.github.com"),
// or the host itself for an IP address, localhost or a host without a public suffix.
func SiteDomain(host string) string {
	h := strings.TrimSuffix(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(host)), "."), ".")
	if d, err := publicsuffix.EffectiveTLDPlusOne(h); err == nil {
		return d
	}
	return h
}

func inDomain(host, domain string) bool {
	h := strings.TrimPrefix(strings.ToLower(host), ".")
	return h == domain || strings.HasSuffix(h, "."+domain)
}

// shareRoute reports whether an extension token may call this route: the bots it can share
// with, and the share.
func shareRoute(method, path string) bool {
	if method == http.MethodGet && path == APIPrefix+"/extension/targets" {
		return true
	}
	if method != http.MethodPost || !strings.HasPrefix(path, APIPrefix+"/spaces/") {
		return false
	}
	seg := strings.Split(strings.TrimPrefix(path, APIPrefix+"/spaces/"), "/")
	return len(seg) == 4 && seg[1] == "bots" && seg[3] == "sign-ins"
}

// tokenScope refuses every route except the share routes to a token that can only share a
// sign-in.
func tokenScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := auth.From(r.Context()); p != nil && p.Scope == auth.ScopeShareSignIn && !shareRoute(r.Method, r.URL.Path) && r.Method != http.MethodOptions {
			writeJSON(w, http.StatusForbidden, map[string]any{"status": 403, "title": "Forbidden", "detail": "This token can only share a sign-in."})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// extensionCORS lets the Fettle extension call the pairing and share routes. The extension
// sends a bearer token and no cookie, so the origin of an extension is enough.
func extensionCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o := r.Header.Get("Origin")
		path := r.URL.Path
		if strings.HasPrefix(o, "chrome-extension://") && (strings.HasPrefix(path, APIPrefix+"/extension/") || shareRoute(http.MethodPost, path)) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", o)
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			h.Set("Access-Control-Allow-Methods", "GET, POST")
			h.Set("Access-Control-Max-Age", "600")
			h.Add("Vary", "Origin")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// pairHandler serves the page that the extension reads the pairing code from. The code is
// in the URL fragment, so it reaches no log.
func pairHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(pairPage)
}

// serverURL is the address that the extension reaches this server at.
func (a *App) serverURL() string {
	if a.Cfg.Mode == ModeServer {
		return strings.TrimRight(a.Cfg.BaseURL, "/")
	}
	host := a.Cfg.Addr
	if strings.HasPrefix(host, ":") {
		host = "127.0.0.1" + host
	}
	return "http://" + host
}

// botBrowser leases the computer of a bot's zone and opens the bot's persistent browser
// context. The zone must have a browser.
func (a *App) botBrowser(ctx context.Context, spaceID, botID uuid.UUID) (*computers.Lease, string, error) {
	b, err := a.Bots.Get(ctx, spaceID, botID)
	if err != nil {
		return nil, "", err
	}
	if b.ZoneID == nil {
		return nil, "", tools.ErrNoZone
	}
	z, err := a.Zones.Get(ctx, spaceID, *b.ZoneID)
	if err != nil {
		return nil, "", err
	}
	if z.Size != "browser" && z.Size != "desktop" {
		return nil, "", errNoBrowser
	}
	l, err := a.Computers.Acquire(ctx, *b.ZoneID)
	if err != nil {
		return nil, "", err
	}
	if err := a.Computers.EnsureBotHome(ctx, l, botID); err != nil {
		l.Release()
		return nil, "", err
	}
	req := tools.BrowserRequest(l, botID)
	if err := l.Client.BrowserOpen(ctx, req); err != nil {
		l.Release()
		return nil, "", err
	}
	return l, req.ID, nil
}

var errNoBrowser = errors.New("the zone of this bot has no browser; set the zone size to browser or desktop")

func signInError(err error) error {
	if errors.Is(err, errNoBrowser) || errors.Is(err, tools.ErrNoZone) {
		return huma.Error409Conflict(err.Error())
	}
	return apiError(err)
}

// Pairing is a one-time code for the extension.
type Pairing struct {
	Code      string    `json:"code" doc:"The code to type in the extension when the default browser is not Chrome."`
	URL       string    `json:"url" doc:"The pairing page. The extension reads the code from it."`
	Server    string    `json:"server"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ExtensionToken is a local-mode extension token, without its value.
type ExtensionToken struct {
	ID         string     `json:"id"`
	Last4      string     `json:"last4"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

// ShareTarget is a bot that a person can share a sign-in with.
type ShareTarget struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Requests []string `json:"requests" doc:"Sites of open takeover requests of this bot."`
}

// ShareSpace is a space with the bots that can take a sign-in.
type ShareSpace struct {
	ID     string        `json:"id"`
	Name   string        `json:"name"`
	People int           `json:"people" doc:"The number of people who can use these bots."`
	Bots   []ShareTarget `json:"bots"`
}

// SharedSignIn is the result of a share.
type SharedSignIn struct {
	Site      string `json:"site"`
	Cookies   int    `json:"cookies"`
	Storage   int    `json:"storage"`
	Continued bool   `json:"continued" doc:"A run that waited for this sign-in continues."`
}

type SignInPath struct {
	Space uuid.UUID `path:"space" doc:"Space ID"`
	Bot   uuid.UUID `path:"bot"`
}

func (a *App) registerSignIns(api huma.API) {
	huma.Register(api, op("create-extension-pairing", http.MethodPost, "/extension/pairings", "Start the pairing of the Chrome extension. The code works once, for 10 minutes.", "sign-ins"), func(ctx context.Context, _ *struct{}) (*struct{ Body Pairing }, error) {
		p := auth.From(ctx)
		if p == nil {
			return nil, huma.Error401Unauthorized("Sign in first.")
		}
		code, exp, err := a.Extensions.NewPairing(ctx, p.UserID)
		if err != nil {
			return nil, err
		}
		srv := a.serverURL()
		return &struct{ Body Pairing }{Body: Pairing{Code: code, URL: srv + "/pair#code=" + code, Server: srv, ExpiresAt: exp}}, nil
	})

	huma.Register(api, op("claim-extension-pairing", http.MethodPost, "/extension/claim", "Use a pairing code. It returns a token that can only share a sign-in.", "sign-ins"), func(ctx context.Context, in *struct {
		Body struct {
			Code string `json:"code" minLength:"1" maxLength:"40"`
		}
	}) (*struct {
		Body struct {
			Token   string `json:"token"`
			Server  string `json:"server"`
			Account string `json:"account"`
		}
	}, error) {
		userID, err := a.Extensions.Claim(ctx, in.Body.Code)
		if errors.Is(err, auth.ErrPairingCode) {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		if err != nil {
			return nil, err
		}
		u, err := auth.NewUserStore(a.DB.Engine).Get(ctx, userID)
		if err != nil || u == nil || u.Status != "active" {
			return nil, huma.Error422UnprocessableEntity(auth.ErrPairingCode.Error())
		}
		var token string
		if a.Cfg.Mode == ModeServer {
			token, err = a.SignIn.CreateExtensionKey(ctx, u.Email)
		} else {
			token, _, err = a.Extensions.CreateToken(ctx, userID)
		}
		if err != nil {
			return nil, err
		}
		a.Vault.Redactor.RegisterGlobal("extension token", token)
		if err := a.Audit.Record(ctx, actorOf(&auth.Principal{UserID: userID}), nil, "extension.pair", "user:"+userID.String(), nil); err != nil {
			return nil, err
		}
		out := &struct {
			Body struct {
				Token   string `json:"token"`
				Server  string `json:"server"`
				Account string `json:"account"`
			}
		}{}
		out.Body.Token, out.Body.Server, out.Body.Account = token, a.serverURL(), u.Email
		return out, nil
	})

	huma.Register(api, op("list-extension-tokens", http.MethodGet, "/extension/tokens", "The extension tokens of the desktop app. In server mode the extension uses an access token, which Account lists.", "sign-ins"), func(ctx context.Context, _ *struct{}) (*struct {
		Body struct {
			Tokens []ExtensionToken `json:"tokens"`
		}
	}, error) {
		p := auth.From(ctx)
		if p == nil {
			return nil, huma.Error401Unauthorized("Sign in first.")
		}
		rows, err := a.Extensions.List(ctx, p.UserID)
		if err != nil {
			return nil, err
		}
		out := &struct {
			Body struct {
				Tokens []ExtensionToken `json:"tokens"`
			}
		}{}
		out.Body.Tokens = []ExtensionToken{}
		for _, r := range rows {
			out.Body.Tokens = append(out.Body.Tokens, ExtensionToken{ID: r.ID().String(), Last4: r.Last4, CreatedAt: r.CreatedAt(), LastUsedAt: r.LastUsedAt})
		}
		return out, nil
	})

	huma.Register(api, op("revoke-extension-token", http.MethodDelete, "/extension/tokens/{id}", "Revoke an extension token.", "sign-ins"), func(ctx context.Context, in *struct {
		ID uuid.UUID `path:"id"`
	}) (*struct{}, error) {
		p := auth.From(ctx)
		if p == nil {
			return nil, huma.Error401Unauthorized("Sign in first.")
		}
		if err := a.Extensions.Revoke(ctx, p.UserID, in.ID); errors.Is(err, auth.ErrNoExtensionToken) {
			return nil, huma.Error404NotFound(err.Error())
		} else if err != nil {
			return nil, err
		}
		return nil, a.Audit.Record(ctx, actorOf(p), nil, "extension.revoke", "extension_token:"+in.ID.String(), nil)
	})

	huma.Register(api, op("list-share-targets", http.MethodGet, "/extension/targets", "The bots that you can share a sign-in with, and their open sign-in requests. With url, it also names the site that a share from that address covers.", "sign-ins"), func(ctx context.Context, in *struct {
		URL string `query:"url" maxLength:"4096" doc:"The address of the tab to share from."`
	}) (*struct {
		Body struct {
			Account string       `json:"account"`
			Site    string       `json:"site,omitempty" doc:"The registrable domain of url, such as github.com."`
			Spaces  []ShareSpace `json:"spaces"`
		}
	}, error) {
		p := auth.From(ctx)
		if p == nil {
			return nil, huma.Error401Unauthorized("Sign in first.")
		}
		ms, err := a.Spaces.ForUser(ctx, p.UserID)
		if err != nil {
			return nil, err
		}
		out := &struct {
			Body struct {
				Account string       `json:"account"`
				Site    string       `json:"site,omitempty" doc:"The registrable domain of url, such as github.com."`
				Spaces  []ShareSpace `json:"spaces"`
			}
		}{}
		out.Body.Account, out.Body.Spaces = p.Email, []ShareSpace{}
		if u, err := url.Parse(in.URL); err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" {
			out.Body.Site = SiteDomain(u.Hostname())
		}
		for _, m := range ms {
			sp := ShareSpace{ID: m.Space.ID().String(), Name: m.Space.Name, Bots: []ShareTarget{}}
			if mem, err := a.Spaces.Members(ctx, m.Space.ID()); err == nil {
				sp.People = len(mem)
			}
			requests := map[string][]string{}
			if cards, err := a.Chats.PendingCards(ctx, m.Space.ID()); err == nil {
				for _, c := range cards {
					if c.Card != nil && c.Card.Kind == "takeover" && c.Card.Data["site"] != "" {
						requests[c.Card.Data["bot_id"]] = append(requests[c.Card.Data["bot_id"]], c.Card.Data["site"])
					}
				}
			}
			zs, err := a.Zones.List(ctx, m.Space.ID())
			if err != nil {
				return nil, err
			}
			browser := map[uuid.UUID]bool{}
			for _, z := range zs {
				browser[z.ID()] = z.Size == "browser" || z.Size == "desktop"
			}
			bs, err := a.Bots.List(ctx, m.Space.ID(), false)
			if err != nil {
				return nil, err
			}
			for _, b := range bs {
				if b.ZoneID == nil || !browser[*b.ZoneID] {
					continue
				}
				reqs := requests[b.ID().String()]
				if reqs == nil {
					reqs = []string{}
				}
				sp.Bots = append(sp.Bots, ShareTarget{ID: b.ID().String(), Name: b.Name, Requests: reqs})
			}
			out.Body.Spaces = append(out.Body.Spaces, sp)
		}
		return out, nil
	})

	huma.Register(api, op("share-sign-in", http.MethodPost, "/spaces/{space}/bots/{bot}/sign-ins", "Put a sign-in that you share from your browser into the browser profile of a bot. Fettle keeps no copy.", "sign-ins"), func(ctx context.Context, in *struct {
		SignInPath
		Body struct {
			URL     string                 `json:"url" minLength:"1" maxLength:"4096" doc:"The address of the tab that you share from."`
			Cookies []client.SessionCookie `json:"cookies" maxItems:"400"`
			Storage map[string]string      `json:"storage,omitempty" doc:"The local storage of the origin of url."`
		}
	}) (*struct{ Body SharedSignIn }, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, false)
		if err != nil {
			return nil, err
		}
		u, err := url.Parse(in.Body.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
			return nil, huma.Error422UnprocessableEntity("url must be the http or https address of the tab")
		}
		site := SiteDomain(u.Hostname())
		if len(in.Body.Cookies) == 0 && len(in.Body.Storage) == 0 {
			return nil, huma.Error422UnprocessableEntity("the share holds no cookie and no local storage; sign in to " + site + " in Chrome first")
		}
		if len(in.Body.Cookies) > shareMaxCookies || len(in.Body.Storage) > shareMaxCookies {
			return nil, huma.Error422UnprocessableEntity(fmt.Sprintf("a share holds at most %d cookies and %d storage items", shareMaxCookies, shareMaxCookies))
		}
		for _, k := range in.Body.Cookies {
			if !inDomain(k.Domain, site) {
				return nil, huma.Error422UnprocessableEntity(fmt.Sprintf("the cookie %q is for %s, not for %s", k.Name, k.Domain, site))
			}
		}
		// The values pass through this server once. The redactor knows them for that time, so
		// no value reaches a log or an event.
		var values []string
		for _, k := range in.Body.Cookies {
			values = append(values, k.Value)
		}
		for _, v := range in.Body.Storage {
			values = append(values, v)
		}
		for _, v := range values {
			a.Vault.Redactor.Register(in.Space, "shared sign-in", v)
		}
		defer func() {
			for _, v := range values {
				a.Vault.Redactor.Forget(in.Space, v)
			}
		}()
		l, ctxID, err := a.botBrowser(ctx, in.Space, in.Bot)
		if err != nil {
			return nil, signInError(err)
		}
		req := client.SessionRequest{Cookies: in.Body.Cookies}
		if len(in.Body.Storage) > 0 {
			req.Origin, req.Storage = u.Scheme+"://"+u.Host, in.Body.Storage
		}
		err = l.Client.BrowserSession(ctx, ctxID, req)
		l.Release()
		if err != nil {
			return nil, err
		}
		res := SharedSignIn{Site: site, Cookies: len(in.Body.Cookies), Storage: len(in.Body.Storage)}
		if err := a.Audit.Record(ctx, actorOf(p), &in.Space, "signin.share", "bot:"+in.Bot.String(), map[string]string{"site": site, "cookies": fmt.Sprint(res.Cookies), "storage": fmt.Sprint(res.Storage)}); err != nil {
			return nil, err
		}
		// A share answers the open takeover requests of this bot for this site, as Done does.
		cards, err := a.Chats.PendingCards(ctx, in.Space)
		if err != nil {
			return nil, err
		}
		note := "I shared my sign-in for " + site + " from Chrome. Open the site and check that you are signed in. If you are still signed out, call request_takeover again."
		for _, c := range cards {
			if c.Card == nil || c.Card.Kind != "takeover" || c.Card.Data["bot_id"] != in.Bot.String() || c.Card.Data["site"] == "" || SiteDomain(c.Card.Data["site"]) != site {
				continue
			}
			if _, err := a.Agent.AnswerCard(ctx, actorOf(p), in.Space, c.Card.ID, "done", note); err == nil {
				res.Continued = true
			}
		}
		return &struct{ Body SharedSignIn }{Body: res}, nil
	})

	huma.Register(api, op("list-bot-sign-ins", http.MethodGet, "/spaces/{space}/bots/{bot}/sign-ins", "The sites that hold cookies in the browser profile of a bot. It starts the computer when it sleeps.", "sign-ins"), func(ctx context.Context, in *SignInPath) (*struct {
		Body struct {
			Sites []string `json:"sites"`
		}
	}, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, false); err != nil {
			return nil, err
		}
		l, ctxID, err := a.botBrowser(ctx, in.Space, in.Bot)
		if err != nil {
			return nil, signInError(err)
		}
		hosts, err := l.Client.BrowserCookieHosts(ctx, ctxID)
		l.Release()
		if err != nil {
			return nil, err
		}
		out := &struct {
			Body struct {
				Sites []string `json:"sites"`
			}
		}{}
		out.Body.Sites = []string{}
		seen := map[string]bool{}
		for _, h := range hosts {
			if d := SiteDomain(h); !seen[d] {
				seen[d] = true
				out.Body.Sites = append(out.Body.Sites, d)
			}
		}
		return out, nil
	})

	huma.Register(api, op("remove-bot-sign-in", http.MethodDelete, "/spaces/{space}/bots/{bot}/sign-ins/{site}", "Sign a bot out of a site: remove the cookies and the stored data of the site from its browser profile.", "sign-ins"), func(ctx context.Context, in *struct {
		SignInPath
		Site string `path:"site" maxLength:"253"`
	}) (*struct{}, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, false)
		if err != nil {
			return nil, err
		}
		site := SiteDomain(agent.SiteHost(in.Site))
		if site == "" {
			return nil, huma.Error422UnprocessableEntity("site must be a host name, such as github.com")
		}
		l, ctxID, err := a.botBrowser(ctx, in.Space, in.Bot)
		if err != nil {
			return nil, signInError(err)
		}
		err = l.Client.BrowserSignOut(ctx, ctxID, site)
		l.Release()
		if err != nil {
			return nil, err
		}
		return nil, a.Audit.Record(ctx, actorOf(p), &in.Space, "signin.remove", "bot:"+in.Bot.String(), map[string]string{"site": site})
	})
}
