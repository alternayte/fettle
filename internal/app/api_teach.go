package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alternayte/drel"
	"github.com/coder/websocket"
	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/agentd/client"
	"github.com/alternayte/fettle/internal/agent"
	"github.com/alternayte/fettle/internal/auth"
	"github.com/alternayte/fettle/internal/chats"
	"github.com/alternayte/fettle/internal/computers"
	"github.com/alternayte/fettle/internal/models"
	"github.com/alternayte/fettle/internal/screens"
	"github.com/alternayte/fettle/internal/skills"
	"github.com/alternayte/fettle/internal/spaces"
	"github.com/alternayte/fettle/internal/teach"
	"github.com/alternayte/fettle/internal/tools"
	"github.com/alternayte/fettle/internal/zones"
)

// TeachReady tells whether a bot can be taught now (§13.3).
type TeachReady struct {
	Ready     bool   `json:"ready" doc:"The bot's zone has the desktop size."`
	ZoneID    string `json:"zone_id,omitempty"`
	ZoneName  string `json:"zone_name,omitempty"`
	Size      string `json:"size,omitempty" enum:"small,browser,desktop,"`
	CanChange bool   `json:"can_change" doc:"You may change the zone to the desktop size (owners)."`
	SessionID string `json:"session_id,omitempty" doc:"Your teach session of this bot that records now."`
}

// TeachStepView is one recorded step.
type TeachStepView struct {
	Seq        int      `json:"seq"`
	Kind       string   `json:"kind" enum:"browser,command,note"`
	URL        string   `json:"url,omitempty"`
	Action     string   `json:"action,omitempty"`
	Role       string   `json:"role,omitempty"`
	Name       string   `json:"name,omitempty"`
	Value      string   `json:"value,omitempty"`
	Secret     string   `json:"secret,omitempty" doc:"The kind of a secret field or input. Fettle keeps no value for it."`
	Command    string   `json:"command,omitempty"`
	Cwd        string   `json:"cwd,omitempty"`
	ExitCode   int      `json:"exit_code"`
	Output     string   `json:"output,omitempty"`
	Notes      []string `json:"notes"`
	Screenshot bool     `json:"screenshot"`
	Login      int      `json:"login,omitempty" doc:"The number of the login step that this step belongs to."`
	VaultItem  string   `json:"vault_item,omitempty"`
	Removed    bool     `json:"removed"`
}

// TeachDraftView is the drafted skill.
type TeachDraftView struct {
	Name        string        `json:"name"`
	SkillMD     string        `json:"skill_md"`
	Inputs      []teach.Input `json:"inputs"`
	EvalsJSON   string        `json:"evals_json"`
	BaseSkillID string        `json:"base_skill_id,omitempty"`
	BaseVersion int           `json:"base_version,omitempty"`
	Diff        string        `json:"diff,omitempty"`
}

// TeachView is a teach session.
type TeachView struct {
	ID           string           `json:"id"`
	BotID        string           `json:"bot_id"`
	ChatID       string           `json:"chat_id"`
	UserID       string           `json:"user_id"`
	Title        string           `json:"title"`
	Status       string           `json:"status" enum:"recording,asking,drafting,review,saved,discarded,failed"`
	Error        string           `json:"error,omitempty"`
	Steps        []TeachStepView  `json:"steps"`
	Questions    []teach.Question `json:"questions"`
	Draft        *TeachDraftView  `json:"draft,omitempty"`
	CardID       string           `json:"card_id,omitempty"`
	SkillID      string           `json:"skill_id,omitempty"`
	SkillVersion int              `json:"skill_version,omitempty"`
	Transcribe   bool             `json:"transcribe" doc:"A transcribe model is set, so spoken notes work."`
	CanSave      bool             `json:"can_save" doc:"You are an owner, so you can save the skill."`
	Mine         bool             `json:"mine" doc:"You teach this session."`
	CreatedAt    time.Time        `json:"created_at"`
}

type TeachPath struct {
	Space uuid.UUID `path:"space" doc:"Space ID"`
	Teach uuid.UUID `path:"teach"`
}

// TeachStepEdit changes one step on the review card.
type TeachStepEdit struct {
	Seq       int      `json:"seq"`
	Value     *string  `json:"value,omitempty" maxLength:"2000"`
	Command   *string  `json:"command,omitempty" maxLength:"4000"`
	Notes     []string `json:"notes,omitempty" maxItems:"20"`
	VaultItem *string  `json:"vault_item,omitempty" maxLength:"200"`
	Removed   *bool    `json:"removed,omitempty"`
}

// TeachEdit is the review card's change of a draft.
type TeachEdit struct {
	SkillMD   *string         `json:"skill_md,omitempty" maxLength:"200000"`
	Inputs    []teach.Input   `json:"inputs,omitempty" maxItems:"20"`
	EvalsJSON *string         `json:"evals_json,omitempty" maxLength:"200000"`
	Steps     []TeachStepEdit `json:"steps,omitempty" maxItems:"2000"`
}

func teachError(err error) error {
	var in *errTeachInput
	switch {
	case errors.As(err, &in):
		return huma.Error422UnprocessableEntity(in.msg)
	case errors.Is(err, teach.ErrNotFound):
		return huma.Error404NotFound(err.Error())
	case errors.Is(err, teach.ErrActive), errors.Is(err, teach.ErrState):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, models.ErrNoTranscribe):
		return huma.Error409Conflict("No transcribe model is set. Type the note, or set a model for the transcribe role in Settings, Models.")
	}
	return screenError(err)
}

func (a *App) teachView(ctx context.Context, sess *teach.Session, p *auth.Principal, role string) (TeachView, error) {
	steps, err := a.Teach.Steps(ctx, sess.ID(), true)
	if err != nil {
		return TeachView{}, err
	}
	groups := loginGroups(steps)
	v := TeachView{ID: sess.ID().String(), BotID: sess.BotID.String(), ChatID: sess.ChatID.String(), UserID: sess.UserID.String(), Title: sess.Title,
		Status: sess.Status, Error: sess.Error, Steps: []TeachStepView{}, Questions: sess.Questions, CardID: sess.CardID, SkillVersion: sess.SkillVersion,
		Transcribe: a.Models.HasTranscribe(ctx, sess.SpaceID, &sess.BotID), CanSave: role == spaces.RoleOwner, Mine: sess.UserID == p.UserID, CreatedAt: sess.CreatedAt()}
	if v.Questions == nil {
		v.Questions = []teach.Question{}
	}
	if sess.SkillID != nil {
		v.SkillID = sess.SkillID.String()
	}
	for _, st := range steps {
		sv := TeachStepView{Seq: st.Seq, Kind: st.Kind, URL: st.URL, Action: st.Action, Role: st.Role, Name: st.Name, Value: st.Value, Secret: st.Secret,
			Command: st.Command, Cwd: st.Cwd, ExitCode: st.ExitCode, Output: st.Output, Notes: st.Notes, Screenshot: st.Screenshot != "",
			Login: groups[st.Seq], VaultItem: st.VaultItem, Removed: st.Removed}
		if sv.Notes == nil {
			sv.Notes = []string{}
		}
		v.Steps = append(v.Steps, sv)
	}
	if sess.Draft.SkillMD != "" {
		d := &TeachDraftView{Name: sess.Draft.Name, SkillMD: sess.Draft.SkillMD, Inputs: sess.Draft.Inputs, EvalsJSON: sess.Draft.EvalsJSON,
			BaseSkillID: sess.Draft.BaseSkillID, BaseVersion: sess.Draft.BaseVersion, Diff: skillDiff(sess.Draft.BaseSkillMD, sess.Draft.SkillMD)}
		if d.Inputs == nil {
			d.Inputs = []teach.Input{}
		}
		v.Draft = d
	}
	return v, nil
}

// teachOf loads a session of a space for the caller.
func (a *App) teachOf(ctx context.Context, in *TeachPath) (*teach.Session, *auth.Principal, string, error) {
	p, role, err := a.ctxSpace(ctx, in.Space, false)
	if err != nil {
		return nil, nil, "", err
	}
	sess, err := a.Teach.Get(ctx, in.Space, in.Teach)
	if err != nil {
		return nil, nil, "", teachError(err)
	}
	return sess, p, role, nil
}

// teachReady reads the zone of a bot for teaching.
func (a *App) teachReady(ctx context.Context, spaceID, botID uuid.UUID, p *auth.Principal, role string) (TeachReady, *zones.Zone, error) {
	b, err := a.Bots.Get(ctx, spaceID, botID)
	if err != nil {
		return TeachReady{}, nil, apiError(err)
	}
	r := TeachReady{CanChange: role == spaces.RoleOwner}
	if s, err := a.Teach.Recording(ctx, botID); err == nil && s != nil && s.UserID == p.UserID {
		r.SessionID = s.ID().String()
	}
	if b.ZoneID == nil {
		return r, nil, nil
	}
	z, err := a.Zones.Get(ctx, spaceID, *b.ZoneID)
	if err != nil {
		return r, nil, zoneError(err)
	}
	r.ZoneID, r.ZoneName, r.Size, r.Ready = z.ID().String(), z.Name, z.Size, z.Size == "desktop"
	return r, z, nil
}

// abandonTeach ends a recording session of the bot whose person no longer holds the screen,
// so that a new session can start.
func (a *App) abandonTeach(ctx context.Context, spaceID, botID uuid.UUID, p *auth.Principal) (*teach.Session, error) {
	old, err := a.Teach.Recording(ctx, botID)
	if err != nil || old == nil {
		return nil, err
	}
	if old.UserID == p.UserID {
		return old, nil
	}
	if h, err := a.Screens.Holder(ctx, botID); err == nil && h != nil && h.HolderKind == "person" && h.HolderID == old.UserID.String() {
		return nil, teach.ErrActive
	}
	if l, err := a.teachLease(ctx, old); err == nil {
		_, _ = l.Client.TeachStop(ctx, old.ID().String(), old.Pulled)
		l.Release()
	}
	_, err = a.Teach.Modify(ctx, old.ID(), func(_ context.Context, s *teach.Session) error {
		s.Status, s.Error = teach.StatusDiscarded, "The person who taught left the screen."
		return nil
	})
	return nil, err
}

func (a *App) registerTeach(api huma.API) {
	huma.Register(api, op("teach-ready", http.MethodGet, "/spaces/{space}/bots/{bot}/teach-ready", "Whether a bot can be taught now: teaching needs the desktop size (§13.3)", "teach"), func(ctx context.Context, in *BotPath) (*struct{ Body TeachReady }, error) {
		p, role, err := a.ctxSpace(ctx, in.Space, false)
		if err != nil {
			return nil, err
		}
		r, _, err := a.teachReady(ctx, in.Space, in.Bot, p, role)
		if err != nil {
			return nil, err
		}
		return &struct{ Body TeachReady }{Body: r}, nil
	})

	huma.Register(api, op("start-teach", http.MethodPost, "/spaces/{space}/teach", "Start teaching a bot a task on its screen. It takes the control lease of the screen and starts the recorder.", "teach"), func(ctx context.Context, in *struct {
		SpacePath
		Body struct {
			BotID        uuid.UUID `json:"bot_id"`
			Title        string    `json:"title,omitempty" maxLength:"300" doc:"What the task is, in a few words."`
			TakeoverCard string    `json:"takeover_card,omitempty" doc:"Record the work of a takeover card as a skill."`
			ChangeSize   bool      `json:"change_size,omitempty" doc:"Change the bot's zone to the desktop size first (owners)."`
		}
	}) (*struct {
		Body struct {
			Session TeachView     `json:"session"`
			Control ScreenControl `json:"control"`
		}
	}, error) {
		p, role, err := a.ctxSpace(ctx, in.Space, false)
		if err != nil {
			return nil, err
		}
		botID := in.Body.BotID
		ready, z, err := a.teachReady(ctx, in.Space, botID, p, role)
		if err != nil {
			return nil, err
		}
		if z == nil {
			return nil, huma.Error409Conflict(tools.ErrNoZone.Error())
		}
		if !ready.Ready {
			if !in.Body.ChangeSize {
				return nil, huma.Error409Conflict(fmt.Sprintf("Teaching needs the desktop size, and the zone %s has the %s size.", z.Name, z.Size))
			}
			if role != spaces.RoleOwner {
				return nil, huma.Error403Forbidden("Only an owner can change the size of a zone. Ask an owner to change the zone " + z.Name + " to the desktop size.")
			}
			if _, err := a.Zones.Update(ctx, actorOf(p), in.Space, z.ID(), zones.Input{Name: z.Name, ProviderID: z.ProviderID, Network: z.Network, AllowHosts: z.AllowHosts, Size: "desktop", IdleMinutes: z.IdleMinutes, ShareBlocked: z.ShareBlocked}); err != nil {
				return nil, zoneError(err)
			}
			if err := a.Computers.Replace(ctx, z.ID()); err != nil {
				return nil, huma.Error500InternalServerError("The zone changed, but its computer could not stop: " + err.Error())
			}
		}
		chatID := uuid.Nil
		if in.Body.TakeoverCard != "" {
			msg, err := a.Chats.FindCard(ctx, in.Space, in.Body.TakeoverCard)
			if err != nil {
				return nil, apiError(err)
			}
			if msg.Card.Kind != "takeover" || msg.Card.Data["bot_id"] != botID.String() {
				return nil, huma.Error422UnprocessableEntity("this card is not a takeover request of this bot")
			}
			chatID = msg.ChatID
		}
		if chatID == uuid.Nil {
			chat, err := a.Chats.DirectFor(ctx, in.Space, botID)
			if err != nil {
				return nil, apiError(err)
			}
			chatID = chat.ID()
		}
		sess, err := a.abandonTeach(ctx, in.Space, botID, p)
		if err != nil {
			return nil, teachError(err)
		}
		tok, exp, err := a.Screens.Control(ctx, in.Space, botID, p.UserID)
		if err != nil {
			return nil, screenError(err)
		}
		fresh := sess == nil
		if sess == nil {
			title := strings.TrimSpace(in.Body.Title)
			if title == "" {
				title = "Work that I showed you"
			}
			sess = &teach.Session{SpaceID: in.Space, BotID: botID, UserID: p.UserID, ChatID: chatID, Title: a.Vault.Redactor.String(ctx, in.Space, title), TakeoverCard: in.Body.TakeoverCard}
			if err := a.Teach.Create(ctx, sess); err != nil {
				_, _ = a.Screens.Release(ctx, botID, p.UserID)
				return nil, teachError(err)
			}
			if err := a.Audit.Record(ctx, actorOf(p), &in.Space, "teach.start", "teach:"+sess.ID().String(), map[string]string{"bot": botID.String()}); err != nil {
				return nil, err
			}
		}
		l, err := a.botScreen(ctx, in.Space, botID)
		created := false
		if err == nil {
			created, err = l.Client.TeachStart(ctx, sess.ID().String(), client.TeachStart{Browser: tools.BrowserRequest(l, botID).ID, Cwd: computers.BotHome(botID)})
			l.Release()
		}
		if err == nil && created && sess.Pulled > 0 {
			// The computer started again, so its recorder counts its steps from 1.
			sess, err = a.Teach.Modify(ctx, sess.ID(), func(_ context.Context, s *teach.Session) error {
				s.Pulled = 0
				return nil
			})
		}
		if err != nil {
			_, _ = a.Screens.Release(ctx, botID, p.UserID)
			if fresh {
				// A new session without a recorder holds nothing to keep.
				_, _ = a.Teach.Modify(ctx, sess.ID(), func(_ context.Context, s *teach.Session) error {
					s.Status, s.Error = teach.StatusDiscarded, "The recorder did not start: "+err.Error()
					return nil
				})
			}
			// A resumed session keeps its steps and records again when the person retries.
			return nil, teachError(err)
		}
		v, err := a.teachView(ctx, sess, p, role)
		if err != nil {
			return nil, err
		}
		out := &struct {
			Body struct {
				Session TeachView     `json:"session"`
				Control ScreenControl `json:"control"`
			}
		}{}
		out.Body.Session, out.Body.Control = v, ScreenControl{Token: tok, ExpiresAt: exp}
		return out, nil
	})

	huma.Register(api, op("get-teach", http.MethodGet, "/spaces/{space}/teach/{teach}", "A teach session with its steps, questions and draft. While it records, it reads the new steps first.", "teach"), func(ctx context.Context, in *TeachPath) (*struct{ Body TeachView }, error) {
		sess, p, role, err := a.teachOf(ctx, in)
		if err != nil {
			return nil, err
		}
		if sess.Status == teach.StatusRecording {
			if err := a.teachPull(ctx, sess, false); err != nil {
				a.Log.Warn("teach pull", "session", sess.ID(), "error", err)
			}
		}
		v, err := a.teachView(ctx, sess, p, role)
		if err != nil {
			return nil, err
		}
		return &struct{ Body TeachView }{Body: v}, nil
	})

	recording := func(ctx context.Context, in *TeachPath) (*teach.Session, *auth.Principal, string, error) {
		sess, p, role, err := a.teachOf(ctx, in)
		if err != nil {
			return nil, nil, "", err
		}
		if sess.UserID != p.UserID {
			return nil, nil, "", huma.Error403Forbidden("Only the person who teaches can do this.")
		}
		if sess.Status != teach.StatusRecording {
			return nil, nil, "", huma.Error409Conflict("The teach session does not record now.")
		}
		return sess, p, role, nil
	}

	huma.Register(api, op("add-teach-note", http.MethodPost, "/spaces/{space}/teach/{teach}/notes", "Add a typed note to the current step, or as a step of its own", "teach"), func(ctx context.Context, in *struct {
		TeachPath
		Body struct {
			Text    string `json:"text" minLength:"1" maxLength:"2000"`
			NewStep bool   `json:"new_step,omitempty" doc:"The note is a step of its own, for example a step in another app."`
		}
	}) (*struct{ Body TeachStepView }, error) {
		sess, _, _, err := recording(ctx, &in.TeachPath)
		if err != nil {
			return nil, err
		}
		st, err := a.teachNote(ctx, sess, in.Body.Text, in.Body.NewStep)
		if err != nil {
			return nil, teachError(err)
		}
		return &struct{ Body TeachStepView }{Body: noteView(st)}, nil
	})

	audio := op("add-teach-voice-note", http.MethodPost, "/spaces/{space}/teach/{teach}/notes/audio", "Add a spoken note to the current step. The transcribe role turns it into text; Fettle keeps the text, not the audio.", "teach")
	audio.MaxBodyBytes = models.MaxAudioBytes
	huma.Register(api, audio, func(ctx context.Context, in *struct {
		TeachPath
		ContentType string `header:"Content-Type"`
		RawBody     []byte `contentType:"application/octet-stream"`
	}) (*struct{ Body TeachStepView }, error) {
		sess, _, _, err := recording(ctx, &in.TeachPath)
		if err != nil {
			return nil, err
		}
		text, err := a.Models.Transcribe(ctx, sess.SpaceID, &sess.BotID, in.RawBody, in.ContentType)
		in.RawBody = nil
		if err != nil {
			return nil, teachError(err)
		}
		if strings.TrimSpace(text) == "" {
			return nil, huma.Error422UnprocessableEntity("The note has no words that the model could hear.")
		}
		st, err := a.teachNote(ctx, sess, text, false)
		if err != nil {
			return nil, teachError(err)
		}
		return &struct{ Body TeachStepView }{Body: noteView(st)}, nil
	})

	huma.Register(api, op("finish-teach", http.MethodPost, "/spaces/{space}/teach/{teach}/done", "End the demo: stop the recorder, return the screen, and let the bot ask its questions", "teach"), func(ctx context.Context, in *TeachPath) (*struct{ Body TeachView }, error) {
		sess, p, role, err := recording(ctx, in)
		if err != nil {
			return nil, err
		}
		if err := a.teachPull(ctx, sess, true); err != nil {
			return nil, teachError(err)
		}
		if _, err := a.Screens.Release(ctx, sess.BotID, p.UserID); err != nil {
			return nil, err
		}
		steps, err := a.Teach.Steps(ctx, sess.ID(), false)
		if err != nil {
			return nil, err
		}
		err = a.DB.Engine.WithTx(ctx, func(ctx context.Context) error {
			cur, err := a.Teach.Modify(ctx, sess.ID(), func(_ context.Context, s *teach.Session) error {
				if s.Status != teach.StatusRecording {
					return teach.ErrState
				}
				if len(steps) == 0 {
					s.Status, s.Error = teach.StatusDiscarded, "Nothing was recorded."
					return nil
				}
				s.Status = teach.StatusAsking
				return nil
			})
			if err != nil {
				return err
			}
			if err := a.Audit.Record(ctx, actorOf(p), &cur.SpaceID, "teach.done", "teach:"+cur.ID().String(), map[string]string{"steps": strconv.Itoa(len(steps))}); err != nil {
				return err
			}
			if cur.Status != teach.StatusAsking {
				return nil
			}
			_, err = a.Queue.InsertTx(ctx, drel.MustFromContext(ctx), TeachArgs{SessionID: cur.ID(), Phase: "questions"}, teachJobOpts())
			return err
		})
		if err != nil {
			return nil, teachError(err)
		}
		if sess.TakeoverCard != "" {
			if msg, err := a.Chats.FindCard(ctx, sess.SpaceID, sess.TakeoverCard); err == nil && msg.Card.Status == "pending" {
				if _, err := a.Agent.AnswerCard(ctx, actorOf(p), sess.SpaceID, sess.TakeoverCard, "done", "I recorded what I did as a skill."); err != nil && !errors.Is(err, chats.ErrCardAnswered) {
					a.Log.Warn("teach takeover card", "error", err)
				}
			}
		}
		if err := a.Agent.WakeScreenWaiters(ctx, sess.BotID); err != nil {
			a.Log.Warn("teach wake", "error", err)
		}
		sess, err = a.Teach.Find(ctx, sess.ID())
		if err != nil {
			return nil, err
		}
		v, err := a.teachView(ctx, sess, p, role)
		if err != nil {
			return nil, err
		}
		return &struct{ Body TeachView }{Body: v}, nil
	})

	huma.Register(api, op("edit-teach-draft", http.MethodPut, "/spaces/{space}/teach/{teach}/draft", "Change the draft on the review card: SKILL.md, the inputs, the steps and the evals", "teach"), func(ctx context.Context, in *struct {
		TeachPath
		Body TeachEdit
	}) (*struct{ Body TeachView }, error) {
		sess, p, role, err := a.teachOf(ctx, &in.TeachPath)
		if err != nil {
			return nil, err
		}
		if role != spaces.RoleOwner && sess.UserID != p.UserID {
			return nil, huma.Error403Forbidden("Only an owner or the person who taught can change the draft.")
		}
		if sess, err = a.teachEdit(ctx, sess, in.Body); err != nil {
			return nil, teachError(err)
		}
		v, err := a.teachView(ctx, sess, p, role)
		if err != nil {
			return nil, err
		}
		return &struct{ Body TeachView }{Body: v}, nil
	})

	huma.Register(api, op("save-teach", http.MethodPost, "/spaces/{space}/teach/{teach}/save", "Save the reviewed draft as a skill version and give it to the bot (owners)", "teach"), func(ctx context.Context, in *struct {
		TeachPath
		Body TeachEdit
	}) (*struct{ Body TeachView }, error) {
		sess, p, role, err := a.teachOf(ctx, &in.TeachPath)
		if err != nil {
			return nil, err
		}
		if role != spaces.RoleOwner {
			return nil, huma.Error403Forbidden("Only an owner of this space can save a skill.")
		}
		if sess, err = a.teachEdit(ctx, sess, in.Body); err != nil {
			return nil, teachError(err)
		}
		steps, err := a.Teach.Steps(ctx, sess.ID(), false)
		if err != nil {
			return nil, err
		}
		files, _, err := a.teachFiles(ctx, sess, steps)
		if err != nil {
			return nil, teachError(err)
		}
		sk, v, err := a.Skills.Store(ctx, actorOf(p), in.Space, files, "local", 0)
		if err != nil {
			return nil, skillError(err)
		}
		if err := a.Skills.Assign(ctx, actorOf(p), in.Space, sess.BotID, sk.ID(), true); err != nil {
			return nil, err
		}
		id := sk.ID()
		sess, err = a.Teach.Modify(ctx, sess.ID(), func(_ context.Context, s *teach.Session) error {
			if s.Status != teach.StatusReview {
				return teach.ErrState
			}
			s.Status, s.SkillID, s.SkillVersion = teach.StatusSaved, &id, v.Version
			return nil
		})
		if err != nil {
			return nil, teachError(err)
		}
		if err := a.Audit.Record(ctx, actorOf(p), &in.Space, "teach.save", "teach:"+sess.ID().String(), map[string]string{"skill": sk.Name, "version": strconv.Itoa(v.Version)}); err != nil {
			return nil, err
		}
		a.resolveTeachCard(ctx, sess, "answered", fmt.Sprintf("Saved as %s, version %d", sk.Name, v.Version))
		out, err := a.teachView(ctx, sess, p, role)
		if err != nil {
			return nil, err
		}
		return &struct{ Body TeachView }{Body: out}, nil
	})

	huma.Register(api, op("discard-teach", http.MethodPost, "/spaces/{space}/teach/{teach}/discard", "Discard a teach session and its screenshots", "teach"), func(ctx context.Context, in *TeachPath) (*struct{ Body TeachView }, error) {
		sess, p, role, err := a.teachOf(ctx, in)
		if err != nil {
			return nil, err
		}
		if role != spaces.RoleOwner && sess.UserID != p.UserID {
			return nil, huma.Error403Forbidden("Only an owner or the person who taught can discard this session.")
		}
		if sess.Status == teach.StatusSaved {
			return nil, huma.Error409Conflict("The skill is saved already.")
		}
		if sess.Status == teach.StatusRecording {
			if l, err := a.teachLease(ctx, sess); err == nil {
				_, _ = l.Client.TeachStop(ctx, sess.ID().String(), sess.Pulled)
				l.Release()
			}
			_, _ = a.Screens.Release(ctx, sess.BotID, sess.UserID)
			_ = a.Agent.WakeScreenWaiters(ctx, sess.BotID)
		}
		sess, err = a.Teach.Modify(ctx, sess.ID(), func(_ context.Context, s *teach.Session) error {
			s.Status = teach.StatusDiscarded
			return nil
		})
		if err != nil {
			return nil, teachError(err)
		}
		a.dropTeachScreenshots(ctx, sess)
		for _, q := range sess.Questions {
			if msg, err := a.Chats.FindCard(ctx, sess.SpaceID, q.CardID); err == nil && msg.Card.Status == "pending" {
				card := *msg.Card
				card.Status, card.Answer = "expired", "The teach session was discarded"
				if m, err := a.Chats.SetCard(ctx, msg.ID(), &card); err == nil {
					a.Chats.Publish(m.ChatID, "message", a.Agent.MessageView(ctx, m))
				}
			}
		}
		a.resolveTeachCard(ctx, sess, "denied", "Discarded")
		if err := a.Audit.Record(ctx, actorOf(p), &in.Space, "teach.discard", "teach:"+sess.ID().String(), nil); err != nil {
			return nil, err
		}
		v, err := a.teachView(ctx, sess, p, role)
		if err != nil {
			return nil, err
		}
		return &struct{ Body TeachView }{Body: v}, nil
	})

	huma.Register(api, op("retry-teach", http.MethodPost, "/spaces/{space}/teach/{teach}/retry", "Try the questions or the draft of a failed teach session again", "teach"), func(ctx context.Context, in *TeachPath) (*struct{ Body TeachView }, error) {
		sess, p, role, err := a.teachOf(ctx, in)
		if err != nil {
			return nil, err
		}
		if role != spaces.RoleOwner && sess.UserID != p.UserID {
			return nil, huma.Error403Forbidden("Only an owner or the person who taught can do this.")
		}
		err = a.DB.Engine.WithTx(ctx, func(ctx context.Context) error {
			cur, err := a.Teach.Modify(ctx, sess.ID(), func(_ context.Context, s *teach.Session) error {
				if s.Status != teach.StatusFailed && s.Status != teach.StatusReview {
					return teach.ErrState
				}
				s.Error = ""
				answered := len(s.Questions) > 0
				for _, q := range s.Questions {
					if strings.TrimSpace(q.Answer) == "" {
						answered = false
					}
				}
				switch {
				case answered:
					s.Status = teach.StatusDrafting
				case len(s.Questions) == 0:
					s.Status = teach.StatusAsking
				default:
					s.Status = teach.StatusAsking
				}
				return nil
			})
			if err != nil {
				return err
			}
			phase := "draft"
			if cur.Status == teach.StatusAsking {
				if len(cur.Questions) > 0 {
					return nil // the questions wait for their answers
				}
				phase = "questions"
			}
			_, err = a.Queue.InsertTx(ctx, drel.MustFromContext(ctx), TeachArgs{SessionID: cur.ID(), Phase: phase}, teachJobOpts())
			return err
		})
		if err != nil {
			return nil, teachError(err)
		}
		sess, err = a.Teach.Find(ctx, sess.ID())
		if err != nil {
			return nil, err
		}
		v, err := a.teachView(ctx, sess, p, role)
		if err != nil {
			return nil, err
		}
		return &struct{ Body TeachView }{Body: v}, nil
	})

	huma.Register(api, op("try-teach", http.MethodPost, "/spaces/{space}/teach/{teach}/try", "Try the saved skill: a normal run of the bot with new inputs, under the normal approval mode. Fettle never replays the demo.", "teach"), func(ctx context.Context, in *struct {
		TeachPath
		Body struct {
			Inputs map[string]string `json:"inputs,omitempty" maxProperties:"20"`
		}
	}) (*struct {
		Body struct {
			ChatID string `json:"chat_id"`
			RunID  string `json:"run_id,omitempty"`
		}
	}, error) {
		sess, p, _, err := a.teachOf(ctx, &in.TeachPath)
		if err != nil {
			return nil, err
		}
		if sess.Status != teach.StatusSaved || sess.SkillID == nil {
			return nil, huma.Error409Conflict("Save the skill first, then try it.")
		}
		sk, err := a.Skills.Get(ctx, sess.SpaceID, *sess.SkillID)
		if err != nil {
			return nil, skillError(err)
		}
		chat, err := a.Chats.DirectFor(ctx, sess.SpaceID, sess.BotID)
		if err != nil {
			return nil, apiError(err)
		}
		body := tryBody(sk.Name, sess.Draft.Inputs, in.Body.Inputs)
		res, err := a.Agent.PostMessage(ctx, actorOf(p), sess.SpaceID, chat.ID(), agent.PostInput{Body: body})
		if err != nil {
			return nil, apiError(err)
		}
		out := &struct {
			Body struct {
				ChatID string `json:"chat_id"`
				RunID  string `json:"run_id,omitempty"`
			}
		}{}
		out.Body.ChatID = chat.ID().String()
		if res.RunID != nil {
			out.Body.RunID = res.RunID.String()
		}
		return out, nil
	})

	huma.Register(api, op("teach-screenshot", http.MethodGet, "/spaces/{space}/teach/{teach}/steps/{seq}/screenshot", "The screenshot of a step. It stays with the teach session and is not in the skill.", "teach"), func(ctx context.Context, in *struct {
		TeachPath
		Seq int `path:"seq"`
	}) (*struct {
		ContentType  string `header:"Content-Type"`
		CacheControl string `header:"Cache-Control"`
		Body         []byte
	}, error) {
		sess, _, _, err := a.teachOf(ctx, &in.TeachPath)
		if err != nil {
			return nil, err
		}
		st, err := a.Teach.Step(ctx, sess.ID(), in.Seq)
		if err != nil {
			return nil, teachError(err)
		}
		img, err := a.teachScreenshot(ctx, sess, st)
		if err != nil {
			return nil, teachError(err)
		}
		return &struct {
			ContentType  string `header:"Content-Type"`
			CacheControl string `header:"Cache-Control"`
			Body         []byte
		}{ContentType: "image/jpeg", CacheControl: "private, max-age=3600", Body: img}, nil
	})
}

// tryBody is the chat message of "Try it": /name with the inputs, one per line.
func tryBody(name string, defs []teach.Input, values map[string]string) string {
	var b strings.Builder
	b.WriteString("/" + name)
	seen := map[string]bool{}
	for _, d := range defs {
		if v := strings.TrimSpace(values[d.Name]); v != "" {
			fmt.Fprintf(&b, "\n%s: %s", d.Name, v)
			seen[d.Name] = true
		}
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		if !seen[k] && strings.TrimSpace(values[k]) != "" {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "\n%s: %s", k, strings.TrimSpace(values[k]))
	}
	return b.String()
}

func noteView(st *teach.Step) TeachStepView {
	v := TeachStepView{Seq: st.Seq, Kind: st.Kind, URL: st.URL, Action: st.Action, Role: st.Role, Name: st.Name, Value: st.Value, Secret: st.Secret, Command: st.Command, Cwd: st.Cwd, ExitCode: st.ExitCode, Output: st.Output, Notes: st.Notes, Screenshot: st.Screenshot != "", Removed: st.Removed}
	if v.Notes == nil {
		v.Notes = []string{}
	}
	return v
}

// teachNote reads the new steps, so the note attaches to the step that the person just
// did, then attaches the redacted note.
func (a *App) teachNote(ctx context.Context, sess *teach.Session, text string, newStep bool) (*teach.Step, error) {
	if err := a.teachPull(ctx, sess, false); err != nil {
		a.Log.Warn("teach pull", "session", sess.ID(), "error", err)
	}
	text = a.Vault.Redactor.String(ctx, sess.SpaceID, text)
	if newStep {
		return a.Teach.NewNoteStep(ctx, sess.ID(), text)
	}
	return a.Teach.AddNote(ctx, sess.ID(), text)
}

// teachEdit applies review edits while the draft waits for review.
func (a *App) teachEdit(ctx context.Context, sess *teach.Session, ed TeachEdit) (*teach.Session, error) {
	if sess.Status != teach.StatusReview {
		return nil, teach.ErrState
	}
	if len(ed.Steps) > 0 {
		edits := make([]teach.StepEdit, len(ed.Steps))
		for i, s := range ed.Steps {
			e := teach.StepEdit{Seq: s.Seq, VaultItem: s.VaultItem, Removed: s.Removed}
			if s.Value != nil {
				v := a.Vault.Redactor.String(ctx, sess.SpaceID, *s.Value)
				e.Value = &v
			}
			if s.Command != nil {
				v := a.Vault.Redactor.String(ctx, sess.SpaceID, *s.Command)
				e.Command = &v
			}
			if s.Notes != nil {
				e.Notes = make([]string, 0, len(s.Notes))
				for _, n := range s.Notes {
					if n = strings.TrimSpace(n); n != "" {
						e.Notes = append(e.Notes, a.Vault.Redactor.String(ctx, sess.SpaceID, n))
					}
				}
			}
			edits[i] = e
		}
		if err := a.Teach.EditSteps(ctx, sess.ID(), edits); err != nil {
			return nil, err
		}
	}
	if ed.SkillMD == nil && ed.Inputs == nil && ed.EvalsJSON == nil {
		return a.Teach.Find(ctx, sess.ID())
	}
	return a.Teach.Modify(ctx, sess.ID(), func(_ context.Context, s *teach.Session) error {
		if s.Status != teach.StatusReview {
			return teach.ErrState
		}
		if ed.SkillMD != nil {
			s.Draft.SkillMD = a.Vault.Redactor.String(ctx, s.SpaceID, *ed.SkillMD)
			if f, _, err := skills.ParseMD(s.Draft.SkillMD); err == nil {
				s.Draft.Name = f.Name
			}
		}
		if ed.Inputs != nil {
			ins := make([]teach.Input, 0, len(ed.Inputs))
			for _, in := range ed.Inputs {
				if strings.TrimSpace(in.Name) == "" {
					continue
				}
				ins = append(ins, teach.Input{Name: strings.TrimSpace(in.Name), Description: a.Vault.Redactor.String(ctx, s.SpaceID, strings.TrimSpace(in.Description)), Example: a.Vault.Redactor.String(ctx, s.SpaceID, strings.TrimSpace(in.Example))})
			}
			s.Draft.Inputs = ins
		}
		if ed.EvalsJSON != nil {
			s.Draft.EvalsJSON = a.Vault.Redactor.String(ctx, s.SpaceID, *ed.EvalsJSON)
		}
		return nil
	})
}

// dropTeachScreenshots deletes the screenshots of a discarded session.
func (a *App) dropTeachScreenshots(ctx context.Context, sess *teach.Session) {
	steps, err := a.Teach.Steps(ctx, sess.ID(), true)
	if err != nil {
		return
	}
	for _, st := range steps {
		if st.Screenshot != "" {
			if err := a.Objects.Delete(ctx, st.Screenshot); err != nil {
				a.Log.Warn("teach screenshot delete", "error", err)
			}
		}
	}
}

// teachPTYStream bridges the terminal pane of the teach view to the recorded terminal in
// the bot's computer. The control token of the bot's screen is the authorization.
func (a *App) teachPTYStream(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, err := a.Screens.Check(ctx, r.URL.Query().Get("t"))
	if err != nil || !c.Control {
		http.Error(w, screens.ErrToken.Error(), http.StatusUnauthorized)
		return
	}
	sid, err := uuid.Parse(r.URL.Query().Get("s"))
	if err != nil {
		http.Error(w, "no teach session", http.StatusBadRequest)
		return
	}
	sess, err := a.Teach.Get(ctx, c.Space, sid)
	if err != nil || sess.BotID != c.Bot || sess.UserID != c.User || sess.Status != teach.StatusRecording {
		http.Error(w, "the teach session does not record now", http.StatusConflict)
		return
	}
	l, err := a.teachLease(ctx, sess)
	if err != nil {
		http.Error(w, "the computer is not available: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer l.Release()
	cols, _ := strconv.Atoi(r.URL.Query().Get("cols"))
	rows, _ := strconv.Atoi(r.URL.Query().Get("rows"))
	up, err := l.Client.TeachPTY(ctx, sess.ID().String(), cols, rows)
	if err != nil {
		http.Error(w, "the terminal did not start: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer func() { _ = up.CloseNow() }()
	down, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = down.CloseNow() }()
	down.SetReadLimit(1 << 20)
	sctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	pump := func(from, to *websocket.Conn) {
		defer cancel()
		for {
			typ, data, err := from.Read(sctx)
			if err != nil {
				return
			}
			if err := to.Write(sctx, typ, data); err != nil {
				return
			}
		}
	}
	go pump(down, up)
	go pump(up, down)
	// The terminal stops when the person returns control or the lease expires.
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-sctx.Done():
			_ = down.Close(websocket.StatusNormalClosure, "the terminal ended")
			return
		case <-t.C:
			h, err := a.Screens.Holder(sctx, c.Bot)
			if err == nil && (h == nil || h.HolderKind != "person" || h.HolderID != c.User.String()) {
				_ = down.Close(websocket.StatusPolicyViolation, "control returned")
				return
			}
		}
	}
}
