package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/auth"
	"github.com/alternayte/fettle/internal/spaces"
)

// MemberView is one person of a space.
type MemberView struct {
	UserID string    `json:"user_id"`
	Email  string    `json:"email"`
	Name   string    `json:"name"`
	Role   string    `json:"role" enum:"owner,member"`
	Since  time.Time `json:"since"`
	You    bool      `json:"you"`
}

// InviteView is an invite that still works. URL is set only when it is created.
type InviteView struct {
	ID        string    `json:"id"`
	Role      string    `json:"role" enum:"owner,member"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
	URL       string    `json:"url,omitempty"`
}

// InviteInfo is what an invite link shows before it is accepted.
type InviteInfo struct {
	Space     string    `json:"space"`
	Role      string    `json:"role"`
	ExpiresAt time.Time `json:"expires_at"`
}

func memberError(err error) error {
	switch {
	case errors.Is(err, spaces.ErrInviteInvalid):
		return huma.Error410Gone(err.Error())
	case errors.Is(err, spaces.ErrLastOwner):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, spaces.ErrBadRole):
		return huma.Error422UnprocessableEntity(err.Error())
	case errors.Is(err, spaces.ErrNotMember):
		return huma.Error404NotFound("This person is not a member of this space.")
	}
	return err
}

// serverOnly refuses invites in local mode, which has one owner (§16).
func (a *App) serverOnly() error {
	if a.Cfg.Mode != ModeServer || a.SignIn == nil {
		return huma.Error409Conflict("Invites need server mode. A local Fettle has one owner.")
	}
	return nil
}

func (a *App) registerMembers(api huma.API) {
	huma.Register(api, op("list-members", http.MethodGet, "/spaces/{space}/members", "The people of a space with their roles", "members"), func(ctx context.Context, in *SpacePath) (*struct {
		Body struct {
			Members []MemberView `json:"members"`
		}
	}, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, false)
		if err != nil {
			return nil, err
		}
		list, err := a.Spaces.Members(ctx, in.Space)
		if err != nil {
			return nil, err
		}
		out := &struct {
			Body struct {
				Members []MemberView `json:"members"`
			}
		}{}
		out.Body.Members = []MemberView{}
		for _, m := range list {
			out.Body.Members = append(out.Body.Members, MemberView{UserID: m.UserID.String(), Email: m.Email, Name: m.Name, Role: m.Role, Since: m.Since, You: m.UserID == p.UserID})
		}
		return out, nil
	})

	huma.Register(api, op("set-member-role", http.MethodPut, "/spaces/{space}/members/{user}", "Change the role of a member. A space keeps at least one owner.", "members"), func(ctx context.Context, in *struct {
		SpacePath
		User uuid.UUID `path:"user"`
		Body struct {
			Role string `json:"role" enum:"owner,member"`
		}
	}) (*struct{}, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		return nil, memberError(a.Spaces.SetRole(ctx, actorOf(p), in.Space, in.User, in.Body.Role))
	})

	huma.Register(api, op("remove-member", http.MethodDelete, "/spaces/{space}/members/{user}", "Remove a person from a space; a member can remove themself. A space keeps at least one owner.", "members"), func(ctx context.Context, in *struct {
		SpacePath
		User uuid.UUID `path:"user"`
	}) (*struct{}, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, false)
		if err != nil {
			return nil, err
		}
		if in.User != p.UserID {
			if _, _, err := a.ctxSpace(ctx, in.Space, true); err != nil {
				return nil, err
			}
		}
		return nil, memberError(a.Spaces.RemoveMember(ctx, actorOf(p), in.Space, in.User))
	})

	huma.Register(api, op("create-invite", http.MethodPost, "/spaces/{space}/invites", "Make a single-use invite link with a role (server mode)", "members"), func(ctx context.Context, in *struct {
		SpacePath
		Body struct {
			Role string `json:"role" enum:"owner,member" default:"member"`
			Days int    `json:"days,omitempty" minimum:"1" maximum:"30" doc:"Days the link works. Default 7."`
		}
	}) (*struct{ Body InviteView }, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		if err := a.serverOnly(); err != nil {
			return nil, err
		}
		token, inv, err := a.Spaces.CreateInvite(ctx, actorOf(p), in.Space, p.UserID, in.Body.Role, time.Duration(in.Body.Days)*24*time.Hour)
		if err != nil {
			return nil, memberError(err)
		}
		v := InviteView{ID: inv.ID().String(), Role: inv.Role, ExpiresAt: inv.ExpiresAt, CreatedAt: inv.CreatedAt(), URL: strings.TrimRight(a.Cfg.BaseURL, "/") + "/invite/" + token}
		return &struct{ Body InviteView }{Body: v}, nil
	})

	huma.Register(api, op("list-invites", http.MethodGet, "/spaces/{space}/invites", "The invite links of a space that still work", "members"), func(ctx context.Context, in *SpacePath) (*struct {
		Body struct {
			Invites []InviteView `json:"invites"`
		}
	}, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, true); err != nil {
			return nil, err
		}
		list, err := a.Spaces.Invites(ctx, in.Space)
		if err != nil {
			return nil, err
		}
		out := &struct {
			Body struct {
				Invites []InviteView `json:"invites"`
			}
		}{}
		out.Body.Invites = []InviteView{}
		for _, inv := range list {
			out.Body.Invites = append(out.Body.Invites, InviteView{ID: inv.ID().String(), Role: inv.Role, ExpiresAt: inv.ExpiresAt, CreatedAt: inv.CreatedAt()})
		}
		return out, nil
	})

	huma.Register(api, op("revoke-invite", http.MethodDelete, "/spaces/{space}/invites/{invite}", "Stop an invite link", "members"), func(ctx context.Context, in *struct {
		SpacePath
		Invite uuid.UUID `path:"invite"`
	}) (*struct{}, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		return nil, memberError(a.Spaces.RevokeInvite(ctx, actorOf(p), in.Space, in.Invite))
	})

	huma.Register(api, op("get-invite", http.MethodGet, "/invites/{token}", "What an invite link gives, before it is accepted (no sign-in)", "members"), func(ctx context.Context, in *struct {
		Token string `path:"token" maxLength:"100"`
	}) (*struct{ Body InviteInfo }, error) {
		inv, sp, err := a.Spaces.FindInvite(ctx, in.Token)
		if err != nil {
			return nil, memberError(err)
		}
		return &struct{ Body InviteInfo }{Body: InviteInfo{Space: sp.Name, Role: inv.Role, ExpiresAt: inv.ExpiresAt}}, nil
	})

	huma.Register(api, op("accept-invite", http.MethodPost, "/invites/{token}/accept", "Join the space of an invite link with the signed-in account", "members"), func(ctx context.Context, in *struct {
		Token string `path:"token" maxLength:"100"`
	}) (*struct{ Body SpaceView }, error) {
		p := auth.From(ctx)
		if p == nil {
			return nil, huma.Error401Unauthorized("Sign in first.")
		}
		sp, role, err := a.Spaces.AcceptInvite(ctx, in.Token, p.UserID)
		if err != nil {
			return nil, memberError(err)
		}
		return &struct{ Body SpaceView }{Body: SpaceView{ID: sp.ID().String(), Name: sp.Name, TimeZone: sp.TimeZone, Role: role}}, nil
	})
}

// inviteSignup creates an account from an invite link, signs it in and adds it to the
// space (§16). It is a plain route because it writes the session cookie.
func (a *App) inviteSignup(w http.ResponseWriter, r *http.Request) {
	if err := a.serverOnly(); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"detail": "Invites need server mode."})
		return
	}
	if !auth.SameOrigin(r, a.Cfg.BaseURL) {
		writeJSON(w, http.StatusForbidden, map[string]string{"detail": "This request comes from another origin."})
		return
	}
	token := chi.URLParam(r, "token")
	var in struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"detail": "Send the email, the name and the password as JSON."})
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > 80 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"detail": "Type your name, 80 characters or fewer."})
		return
	}
	ctx := r.Context()
	if _, _, err := a.Spaces.FindInvite(ctx, token); err != nil {
		writeJSON(w, http.StatusGone, map[string]string{"detail": spaces.ErrInviteInvalid.Error()})
		return
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	u, err := a.SignIn.SignUp(ctx, w, in.Email, in.Name, in.Password, ip)
	if err != nil {
		if status, msg, ok := auth.PublicError(err); ok {
			if status == http.StatusConflict {
				msg = "An account with this email exists. Sign in with it, then open the invite link again."
			}
			writeJSON(w, status, map[string]string{"detail": msg})
			return
		}
		a.Log.Error("invite sign-up", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": "Fettle could not create the account."})
		return
	}
	sp, role, err := a.Spaces.AcceptInvite(ctx, token, u.ID())
	if err != nil {
		writeJSON(w, http.StatusGone, map[string]string{"detail": err.Error()})
		return
	}
	_ = a.Audit.Record(ctx, audit.Actor{Kind: "user", ID: u.ID().String()}, nil, "user.signup", "user:"+u.ID().String(), map[string]string{"via": "invite"})
	writeJSON(w, http.StatusCreated, map[string]string{"space_id": sp.ID().String(), "role": role})
}
