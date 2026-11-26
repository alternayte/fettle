package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/user"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/docs"
	"github.com/alternayte/fettle/internal/agent"
	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/auth"
	"github.com/alternayte/fettle/internal/chats"
	"github.com/alternayte/fettle/internal/envelopes"
	"github.com/alternayte/fettle/internal/models"
	"github.com/alternayte/fettle/internal/selfhelp"
	"github.com/alternayte/fettle/internal/settings"
	"github.com/alternayte/fettle/internal/web"
)

func init() {
	settings.Register(settings.Def{Key: "fettle.issue_repo", Scope: "instance", Default: selfhelp.DefaultRepo, Admin: true,
		Doc: "The GitHub repository, as owner/name, that gets the issues that bots and people draft."})
}

// Time limits of the doctor checks in fettle_status and in issue drafts.
const (
	selfDoctorLimit = 10 * time.Second
	selfDoctorTTL   = 60 * time.Second
)

// doctorCache keeps the last doctor result, so that status questions do not start many
// network checks.
type doctorCache struct {
	mu     sync.Mutex
	at     time.Time
	checks []Check
	// done is open while one run of the checks is in progress.
	done chan struct{}
}

// cachedDoctor returns the doctor checks of the last 60 s, or runs them with a total time
// limit of 10 s. When the limit passes, it returns one failed check that says so, and the
// checks finish in the background for the next call.
func (a *App) cachedDoctor(ctx context.Context) []Check {
	c := &a.selfDoctor
	c.mu.Lock()
	if !c.at.IsZero() && time.Since(c.at) < selfDoctorTTL {
		out := c.checks
		c.mu.Unlock()
		return out
	}
	done := c.done
	if done == nil {
		done = make(chan struct{})
		c.done = done
		go func() {
			cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), selfDoctorLimit)
			defer cancel()
			checks := a.Doctor(cctx)
			c.mu.Lock()
			c.checks, c.at, c.done = checks, time.Now(), nil
			c.mu.Unlock()
			close(done)
		}()
	}
	c.mu.Unlock()
	t := time.NewTimer(selfDoctorLimit)
	defer t.Stop()
	select {
	case <-done:
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.checks
	case <-t.C:
		return []Check{{Name: "doctor", Reason: "the checks took more than 10 s; ask again in a minute"}}
	case <-ctx.Done():
		return []Check{{Name: "doctor", Reason: ctx.Err().Error()}}
	}
}

// requesterIsAdmin reports whether the person who started the work of an envelope is an
// instance admin. Work that no person started (a routine or a webhook) is not.
func (a *App) requesterIsAdmin(ctx context.Context, spaceID, envelopeID uuid.UUID) (bool, *auth.User) {
	env, err := a.Envs.Get(ctx, spaceID, envelopeID)
	if err != nil {
		return false, nil
	}
	if env.RootID != uuid.Nil && env.RootID != env.ID() {
		if root, err := a.Envs.Get(ctx, spaceID, env.RootID); err == nil {
			env = root
		}
	}
	if env.Requester.Type != "user" {
		return false, nil
	}
	id, err := uuid.Parse(env.Requester.ID)
	if err != nil {
		return false, nil
	}
	u, err := a.Users.Get(ctx, id)
	if err != nil {
		return false, nil
	}
	return u.IsAdmin, u
}

// hideNames are the words that an issue draft must not carry: the name and the email of
// the person and the user name of the OS account that runs Fettle.
func hideNames(u *auth.User) []string {
	var out []string
	if u != nil {
		out = append(out, u.Name, u.Email)
		if i := strings.IndexByte(u.Email, '@'); i > 0 {
			out = append(out, u.Email[:i])
		}
		out = append(out, strings.Fields(u.Name)...)
	}
	if cur, err := user.Current(); err == nil {
		out = append(out, cur.Username, cur.Name)
	}
	return out
}

// mode is local, desktop or server.
func (a *App) modeName() string {
	if a.Desktop != nil {
		return "desktop"
	}
	return string(a.Cfg.Mode)
}

// BotSetup is the setup of one bot, as fettle_status shows it.
type BotSetup struct {
	Name          string              `json:"name"`
	Models        map[string][]string `json:"models"`
	Zone          string              `json:"zone,omitempty"`
	Network       string              `json:"network,omitempty"`
	AllowedHosts  []string            `json:"allowed_hosts,omitempty"`
	Provider      string              `json:"computer_provider,omitempty"`
	ProviderError string              `json:"computer_provider_error,omitempty"`
	Computer      string              `json:"computer_state,omitempty"`
	ComputerError string              `json:"computer_error,omitempty"`
	Skills        []string            `json:"skills"`
	Connectors    []string            `json:"space_connectors"`
	WebSearch     string              `json:"web_search"`
	FailedRuns    []FailedRun         `json:"last_failed_runs"`
}

// FailedRun is one failed run of a bot with its error.
type FailedRun struct {
	Run   string `json:"run"`
	Error string `json:"error"`
}

// Status is the result of fettle_status.
type Status struct {
	Version      string   `json:"version"`
	OS           string   `json:"os"`
	Arch         string   `json:"arch"`
	Mode         string   `json:"mode"`
	Bot          BotSetup `json:"bot"`
	DoctorChecks []Check  `json:"doctor_checks,omitempty"`
	Note         string   `json:"note,omitempty"`
}

func (a *App) botSetup(ctx context.Context, spaceID, botID uuid.UUID) (BotSetup, error) {
	b, err := a.Bots.Get(ctx, spaceID, botID)
	if err != nil {
		return BotSetup{}, err
	}
	s := BotSetup{Name: b.Name, Models: map[string][]string{}, Skills: []string{}, Connectors: []string{}, FailedRuns: []FailedRun{}}
	for _, role := range []string{models.RoleOwner, models.RoleRouter, models.RoleVerifier, models.RoleComputerUse, models.RoleSummarizer} {
		res, err := a.Models.Resolve(ctx, spaceID, &botID, role)
		if err != nil {
			s.Models[role] = []string{"none: " + err.Error()}
			continue
		}
		for _, r := range res {
			s.Models[role] = append(s.Models[role], r.Name+" / "+r.Target.Model)
		}
	}
	if b.ZoneID == nil {
		s.Zone = "none: the bot has no zone, so it has no computer"
	} else if z, err := a.Zones.Get(ctx, spaceID, *b.ZoneID); err == nil {
		s.Zone, s.Network, s.AllowedHosts = z.Name, z.Network, z.AllowHosts
		if a.Computers != nil {
			kind, perr := a.Computers.ProviderKind(ctx, z)
			s.Provider = kind
			if perr != nil {
				s.ProviderError = perr.Error()
			}
			if c, err := a.Computers.Current(ctx, z.ID()); err == nil && c != nil {
				s.Computer, s.ComputerError = c.State, c.Error
			} else {
				s.Computer = "none yet; it starts at the first tool that needs it"
			}
		}
	}
	if sk, err := a.Skills.ForBot(ctx, spaceID, botID); err == nil {
		for _, k := range sk {
			s.Skills = append(s.Skills, k.Name)
		}
	}
	if cs, err := a.Connectors.List(ctx, spaceID); err == nil {
		for _, c := range cs {
			s.Connectors = append(s.Connectors, c.Name)
		}
	}
	if p, err := a.Web.Get(ctx, spaceID); err == nil {
		s.WebSearch = p.Kind
	} else if errors.Is(err, web.ErrNoProvider) {
		s.WebSearch = "off: no search provider in Settings, Web (/settings/web)"
	}
	rows, err := a.DB.Engine.Query(ctx, `SELECT e.data FROM runs r JOIN run_events e ON e.run_id = r.id AND e.type = 'run_finished'
		WHERE r.space_id = $1 AND r.bot_id = $2 AND r.status = 'failed' ORDER BY e.at DESC, e.id DESC LIMIT 5`, spaceID, botID)
	if err != nil {
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return s, err
		}
		var ev struct {
			Summary string `json:"summary"`
			Error   string `json:"error"`
		}
		_ = json.Unmarshal([]byte(data), &ev)
		msg := ev.Error
		if msg == "" {
			msg = ev.Summary
		}
		if r := []rune(msg); len(r) > 500 {
			msg = string(r[:500]) + "…"
		}
		s.FailedRuns = append(s.FailedRuns, FailedRun{Error: msg})
	}
	return s, rows.Err()
}

// status builds the result of fettle_status. Only an instance admin gets the doctor
// checks, which cover the whole instance.
func (a *App) status(ctx context.Context, spaceID, botID uuid.UUID, admin bool) (Status, error) {
	st := Status{Version: a.Cfg.Version, OS: runtime.GOOS, Arch: runtime.GOARCH, Mode: a.modeName()}
	bs, err := a.botSetup(ctx, spaceID, botID)
	if err != nil {
		return st, err
	}
	st.Bot = bs
	if admin {
		st.DoctorChecks = a.cachedDoctor(ctx)
	} else {
		st.Note = "The instance checks are only for an instance admin. An admin sees them in /admin, Diagnostics."
	}
	return st, nil
}

// issueDiagnostics returns the facts for an issue. The failed doctor checks are only for an
// instance admin.
func (a *App) issueDiagnostics(ctx context.Context, spaceID uuid.UUID, botID *uuid.UUID, admin bool) selfhelp.Diagnostics {
	d := selfhelp.Diagnostics{Version: a.Cfg.Version, OS: runtime.GOOS, Arch: runtime.GOARCH, Mode: a.modeName(), ModelProviders: []string{}, FailedChecks: []string{}}
	if a.Computers != nil {
		d.ComputerProvider = a.Computers.Default
	}
	if botID != nil {
		if b, err := a.Bots.Get(ctx, spaceID, *botID); err == nil && b.ZoneID != nil {
			if z, err := a.Zones.Get(ctx, spaceID, *b.ZoneID); err == nil && a.Computers != nil {
				if k, _ := a.Computers.ProviderKind(ctx, z); k != "" {
					d.ComputerProvider = k
				}
			}
		}
	}
	if ps, err := a.Models.List(ctx, spaceID); err == nil {
		seen := map[string]bool{}
		for _, p := range ps {
			k := p.Preset
			if k == "" {
				k = p.APIType
			}
			if !seen[k] {
				seen[k] = true
				d.ModelProviders = append(d.ModelProviders, k)
			}
		}
		sort.Strings(d.ModelProviders)
	}
	if admin {
		for _, c := range a.cachedDoctor(ctx) {
			if !c.Pass {
				d.FailedChecks = append(d.FailedChecks, c.Name+": "+c.Reason)
			}
		}
	}
	return d
}

// redactFor removes the vault values of a space from s.
func (a *App) redactFor(ctx context.Context, spaceID uuid.UUID, s string) string {
	return a.Vault.Redactor.String(ctx, spaceID, s)
}

// prepareIssue redacts a draft with the vault values of the space and the issue redactor.
func (a *App) prepareIssue(ctx context.Context, spaceID uuid.UUID, dr selfhelp.Draft, d selfhelp.Diagnostics, u *auth.User) selfhelp.Issue {
	for _, p := range []*string{&dr.Title, &dr.What, &dr.Expected, &dr.Steps} {
		*p = a.redactFor(ctx, spaceID, *p)
	}
	for i, c := range d.FailedChecks {
		d.FailedChecks[i] = a.redactFor(ctx, spaceID, c)
	}
	repo := a.Settings.String(ctx, nil, "fettle.issue_repo")
	return selfhelp.Prepare(repo, dr, d, hideNames(u)...)
}

// registerSelfHelp adds fettle_docs, fettle_status and draft_issue.
func (a *App) registerSelfHelp() {
	d := selfhelp.NewDocs(docs.FS)
	r := a.Agent.Tools
	r.Add(agent.Tool{
		Name:        "fettle_docs",
		Description: "Read the Fettle docs of the version that runs. With no arguments, list the pages. With query, search the pages for words. With page (and optional section), read one page or one section.",
		Class:       envelopes.ClassRead,
		Schema:      json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","maxLength":200},"page":{"type":"string","maxLength":100,"description":"A page name from the list, for example models.md."},"section":{"type":"string","maxLength":120,"description":"A heading of the page, or a part of it."}},"additionalProperties":false}`),
		Run: func(_ context.Context, _ *agent.ToolEnv, args json.RawMessage) (agent.ToolOutput, error) {
			var in struct{ Query, Page, Section string }
			if len(args) > 0 {
				if err := json.Unmarshal(args, &in); err != nil {
					return agent.ToolOutput{}, err
				}
			}
			var b strings.Builder
			switch {
			case strings.TrimSpace(in.Page) != "":
				text, err := d.ReadDoc(in.Page, in.Section)
				if err != nil {
					return agent.ToolOutput{}, err
				}
				b.WriteString(text)
			case strings.TrimSpace(in.Query) != "":
				hits := d.Search(in.Query, 6)
				if len(hits) == 0 {
					b.WriteString("No section has these words. Call fettle_docs with no arguments to list the pages.")
				}
				for _, h := range hits {
					fmt.Fprintf(&b, "## %s — %s\n%s\n\n", h.Page, h.Section, h.Snippet)
				}
				if len(hits) > 0 {
					b.WriteString("Read a section with page and section.")
				}
			default:
				b.WriteString("Fettle docs pages:\n")
				for _, p := range d.Pages() {
					fmt.Fprintf(&b, "- %s: %s\n", p.Name, p.Title)
				}
			}
			return agent.ToolOutput{Text: b.String(), Origin: agent.OriginSystem}, nil
		},
	})
	r.Add(agent.Tool{
		Name:        "fettle_status",
		Description: "Check Fettle and your own setup: the version, the mode, your models per role, your zone, network and computer, your skills, the connectors and web search of the space, your last failed runs, and (for an instance admin) the doctor checks. It never shows keys or vault values.",
		Class:       envelopes.ClassRead,
		Schema:      json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		Run: func(ctx context.Context, env *agent.ToolEnv, _ json.RawMessage) (agent.ToolOutput, error) {
			admin, _ := a.requesterIsAdmin(ctx, env.SpaceID, env.EnvelopeID)
			st, err := a.status(ctx, env.SpaceID, env.BotID, admin)
			if err != nil {
				return agent.ToolOutput{}, err
			}
			out, err := json.MarshalIndent(st, "", "  ")
			if err != nil {
				return agent.ToolOutput{}, err
			}
			return agent.ToolOutput{Text: a.redactFor(ctx, env.SpaceID, string(out)), Origin: agent.OriginSystem}, nil
		},
	})
	r.Add(agent.Tool{
		Name:        "draft_issue",
		Description: "Draft a GitHub issue about a bug in Fettle. A card in the chat shows the draft with diagnostics, without private details. The person reads it and opens it on GitHub. Nothing goes to GitHub before the person submits it there. The run does not wait.",
		Class:       envelopes.ClassRead,
		NoSubagent:  true,
		Schema:      json.RawMessage(`{"type":"object","properties":{"title":{"type":"string","minLength":5,"maxLength":200},"what":{"type":"string","maxLength":4000,"description":"What happened, with the error text."},"expected":{"type":"string","maxLength":2000},"steps":{"type":"string","maxLength":4000,"description":"The steps that cause the problem."}},"required":["title","what"],"additionalProperties":false}`),
		Run: func(ctx context.Context, env *agent.ToolEnv, args json.RawMessage) (agent.ToolOutput, error) {
			var in selfhelp.Draft
			if err := json.Unmarshal(args, &in); err != nil {
				return agent.ToolOutput{}, err
			}
			admin, u := a.requesterIsAdmin(ctx, env.SpaceID, env.EnvelopeID)
			is := a.prepareIssue(ctx, env.SpaceID, in, a.issueDiagnostics(ctx, env.SpaceID, &env.BotID, admin), u)
			chatID := env.ChatID
			if chatID == nil {
				c, err := a.Chats.DirectFor(ctx, env.SpaceID, env.BotID)
				if err != nil {
					return agent.ToolOutput{}, err
				}
				id := c.ID()
				chatID = &id
			}
			data := map[string]string{"url": is.URL, "repo": is.Repo}
			if is.Cut {
				data["cut"] = "true"
			}
			card := chats.Card{ID: uuid.NewString(), Kind: "issue", Status: "pending", Title: is.Title, Detail: is.Body, Data: data}
			msg := &chats.Message{SpaceID: env.SpaceID, ChatID: *chatID, AuthorKind: "bot", AuthorID: env.BotID.String(), Card: &card, EnvelopeID: &env.EnvelopeID}
			err := a.DB.Engine.WithTx(ctx, func(ctx context.Context) error {
				if err := a.Chats.Insert(ctx, msg, nil); err != nil {
					return err
				}
				return a.Audit.Record(ctx, audit.Actor{Kind: "bot", ID: env.BotID.String()}, &env.SpaceID, "card.post", "card:"+card.ID, map[string]string{"kind": "issue", "repo": is.Repo})
			})
			if err != nil {
				return agent.ToolOutput{}, err
			}
			a.Chats.Publish(*chatID, "message", a.Agent.MessageView(ctx, msg))
			text := "The issue card for " + is.Repo + " is in the chat. Tell the person to read it and select Open on GitHub. Nothing goes to GitHub before they submit it there."
			if is.Cut {
				text += " The text is long, so the link holds only its start; the card has Copy for the full text."
			}
			return agent.ToolOutput{Text: text, Origin: agent.OriginTool}, nil
		},
	})
}

// IssueDraftView is a redacted issue draft for the Report a problem form.
type IssueDraftView struct {
	Repo        string               `json:"repo"`
	Diagnostics selfhelp.Diagnostics `json:"diagnostics"`
	// Markdown is the redacted diagnostics block that the form adds to the body.
	Markdown string `json:"markdown"`
	// MaxURL is the longest new-issue URL that the form builds.
	MaxURL int `json:"max_url"`
}

func (a *App) registerIssueDraft(api huma.API) {
	huma.Register(api, op("issue-draft", http.MethodGet, "/spaces/{space}/issue-draft", "The redacted diagnostics and the repository for a GitHub issue about Fettle", "help"), func(ctx context.Context, in *SpacePath) (*struct{ Body IssueDraftView }, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, false)
		if err != nil {
			return nil, err
		}
		u, _ := a.Users.Get(ctx, p.UserID)
		d := a.issueDiagnostics(ctx, in.Space, nil, p.IsAdmin)
		is := a.prepareIssue(ctx, in.Space, selfhelp.Draft{}, d, u)
		names := hideNames(u)
		for i, c := range d.FailedChecks {
			d.FailedChecks[i] = selfhelp.Redact(a.redactFor(ctx, in.Space, c), names...)
		}
		out := &struct{ Body IssueDraftView }{}
		out.Body = IssueDraftView{Repo: is.Repo, Diagnostics: d, Markdown: strings.TrimSpace(is.Body), MaxURL: selfhelp.MaxURL}
		return out, nil
	})
}
