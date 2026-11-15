package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/bots"
	"github.com/alternayte/fettle/internal/routines"
	"github.com/alternayte/fettle/internal/skills"
	"github.com/alternayte/fettle/templates"
)

// TemplateView is a bot template (§15.5).
type TemplateView struct {
	Slug       string `json:"slug"`
	Name       string `json:"name"`
	Job        string `json:"job"`
	PublicDesc string `json:"public_desc"`
	Zone       string `json:"zone"`
	Skill      string `json:"skill"`
	Routine    string `json:"routine"`
}

// fromTemplate creates a bot from a template in the transaction in ctx: the bot with its
// instructions in the template's zone, its direct chat, its skill in the library, and its
// routine paused. primary marks the space's first bot.
func (a *App) fromTemplate(ctx context.Context, actor audit.Actor, spaceID uuid.UUID, tz string, t templates.Template, primary bool) (*bots.Bot, error) {
	z, err := a.Zones.ByName(ctx, spaceID, t.Zone)
	if err != nil {
		return nil, fmt.Errorf("the template needs the zone %s: %w", t.Zone, err)
	}
	zid := z.ID()
	name := t.Name
	var b *bots.Bot
	for n := 1; n < 100; n++ {
		if n > 1 {
			name = fmt.Sprintf("%s %d", t.Name, n)
		}
		b, err = a.Bots.Create(ctx, actor, spaceID, bots.Input{Name: name, Job: t.Job, PublicDesc: t.PublicDesc, Instructions: t.Instructions, ZoneID: &zid, IsPrimary: primary})
		if !errors.Is(err, bots.ErrNameTaken) {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	if _, err := a.Chats.CreateDirect(ctx, spaceID, b.ID(), b.Name); err != nil {
		return nil, err
	}
	if a.Skills != nil {
		sk, _, err := a.Skills.Store(ctx, actor, spaceID, skills.Files(t.SkillFiles), "local", 0)
		if err != nil {
			return nil, err
		}
		if err := a.Skills.Assign(ctx, actor, spaceID, b.ID(), sk.ID(), true); err != nil {
			return nil, err
		}
	}
	if a.Routines != nil {
		if tz == "" {
			tz = "UTC"
		}
		if _, _, err := a.Routines.Create(ctx, actor, spaceID, routines.Input{BotID: b.ID(), Name: t.Routine.Name, Prompt: t.Routine.Prompt, Trigger: "cron", Cron: t.Routine.Cron, TZ: tz, Notify: "on_failure", Enabled: false}); err != nil {
			return nil, err
		}
	}
	return b, nil
}

func (a *App) registerTemplates(api huma.API) {
	huma.Register(api, op("list-templates", http.MethodGet, "/templates", "The bot templates that ship with Fettle", "bots"), func(ctx context.Context, _ *struct{}) (*struct {
		Body struct {
			Templates []TemplateView `json:"templates"`
		}
	}, error) {
		ts, err := templates.All()
		if err != nil {
			return nil, err
		}
		out := &struct {
			Body struct {
				Templates []TemplateView `json:"templates"`
			}
		}{}
		out.Body.Templates = []TemplateView{}
		for _, t := range ts {
			out.Body.Templates = append(out.Body.Templates, TemplateView{Slug: t.Slug, Name: t.Name, Job: t.Job, PublicDesc: t.PublicDesc, Zone: t.Zone, Skill: t.Skill, Routine: t.Routine.Name})
		}
		return out, nil
	})
	huma.Register(api, op("create-from-template", http.MethodPost, "/spaces/{space}/templates/{template}", "Create a bot from a template, with its skill and its routine paused", "bots"), func(ctx context.Context, in *struct {
		SpacePath
		Template string `path:"template"`
	}) (*botOut, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, true)
		if err != nil {
			return nil, err
		}
		t, err := templates.Get(in.Template)
		if err != nil {
			return nil, huma.Error404NotFound("template not found")
		}
		sp, err := a.Spaces.Get(ctx, in.Space)
		if err != nil {
			return nil, apiError(err)
		}
		var b *bots.Bot
		err = a.DB.Engine.WithTx(ctx, func(ctx context.Context) error {
			var err error
			b, err = a.fromTemplate(ctx, actorOf(p), in.Space, sp.TimeZone, t, false)
			return err
		})
		if err != nil {
			return nil, apiError(err)
		}
		return &botOut{Body: a.botView(ctx, b)}, nil
	})
}
