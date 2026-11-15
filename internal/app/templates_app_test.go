package app_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/alternayte/drel"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/apptest"
	"github.com/alternayte/fettle/internal/envelopes"
	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
	"github.com/alternayte/fettle/internal/runs"
	"github.com/alternayte/fettle/internal/skills"
	"github.com/alternayte/fettle/templates"
)

func TestSCN_UX_005_TemplatesCreateBotsWhoseEvalsPass(t *testing.T) {
	e := apptest.Start(t, "sqlite")
	e.SetupModel("openai-chat", "", 128000)
	ts, err := templates.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 5 {
		t.Fatalf("%d templates, want 5", len(ts))
	}
	var listed struct {
		Templates []struct {
			Slug string `json:"slug"`
		} `json:"templates"`
	}
	e.MustDo("GET", "/templates", nil, &listed)
	if len(listed.Templates) != 5 {
		t.Fatalf("GET /templates: %+v", listed)
	}

	for _, tp := range ts {
		t.Run(tp.Slug, func(t *testing.T) {
			// The space starts with Chief from the Chief of Staff template; the others are created.
			var bot apptest.Bot
			if tp.Slug == "chief-of-staff" {
				bot = e.Chief()
			} else {
				e.MustDo("POST", e.SP("/templates/%s", tp.Slug), nil, &bot)
			}
			var full struct {
				Name         string `json:"name"`
				Instructions string `json:"instructions"`
				Zone         struct {
					Name string `json:"name"`
				} `json:"zone"`
			}
			e.MustDo("GET", e.SP("/bots/%s", bot.ID), nil, &full)
			if full.Name != tp.Name || full.Instructions != tp.Instructions {
				t.Fatalf("bot %s: name %q, instructions %q", tp.Slug, full.Name, full.Instructions)
			}

			var lib struct {
				Skills []struct {
					ID   string   `json:"id"`
					Name string   `json:"name"`
					Bots []string `json:"bots"`
				} `json:"skills"`
			}
			e.MustDo("GET", e.SP("/skills"), nil, &lib)
			skillID := ""
			for _, s := range lib.Skills {
				if s.Name == tp.Skill && slices.Contains(s.Bots, bot.ID) {
					skillID = s.ID
				}
			}
			if skillID == "" {
				t.Fatalf("%s has no skill %s: %+v", tp.Name, tp.Skill, lib.Skills)
			}

			var rs struct {
				Routines []struct {
					BotID   string `json:"bot_id"`
					Name    string `json:"name"`
					Enabled bool   `json:"enabled"`
				} `json:"routines"`
			}
			e.MustDo("GET", e.SP("/routines"), nil, &rs)
			found := false
			for _, r := range rs.Routines {
				if r.BotID == bot.ID && r.Name == tp.Routine.Name {
					found = true
					if r.Enabled {
						t.Fatalf("the routine %s is not paused", r.Name)
					}
				}
			}
			if !found {
				t.Fatalf("no routine %s for %s: %+v", tp.Routine.Name, tp.Name, rs.Routines)
			}

			// The fake answers each case with its expected output; the verifier grades it.
			ev, err := skills.ParseEvals(tp.SkillFiles["evals.json"])
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range ev.Evals {
				e.Fake.AddScript(fakemodel.Script{Name: fmt.Sprintf("eval-%s-%d", tp.Slug, c.ID), Match: fakemodel.Match{User: c.Prompt}, Steps: []fakemodel.Step{{Text: c.ExpectedOutput}}})
			}
			var rep skills.EvalReport
			e.MustDo("POST", e.SP("/skills/%s/evals", skillID), map[string]any{"bot_id": bot.ID, "trials": 2}, &rep)
			if !rep.Pass || rep.Score != 1 || len(rep.Cases) != len(ev.Evals) {
				t.Fatalf("evals of %s: %+v", tp.Skill, rep)
			}
			for _, c := range rep.Cases {
				if c.Trials != 2 || c.Passed != 2 || c.Output == "" || len(c.Expectations) == 0 {
					t.Fatalf("case %+v", c)
				}
			}
		})
	}
}

func TestBotZoneOnCreateFromTemplateAndMove(t *testing.T) {
	e := apptest.Start(t, "sqlite")
	var zs struct {
		Zones []struct{ ID, Name string } `json:"zones"`
	}
	e.MustDo("GET", e.SP("/zones"), nil, &zs)
	zone := map[string]string{}
	for _, z := range zs.Zones {
		zone[z.Name] = z.ID
	}
	type view struct {
		ID       string `json:"id"`
		ZoneName string `json:"zone_name"`
		ZoneSize string `json:"zone_size"`
	}
	// The template's own zone is browsing; the request puts the bot in personal.
	var research view
	e.MustDo("POST", e.SP("/templates/research"), map[string]string{"zone_id": zone["personal"]}, &research)
	if research.ZoneName != "personal" || research.ZoneSize == "" {
		t.Fatalf("a template bot with a zone: %+v", research)
	}
	// Without a body, the template's zone applies, as for the CLI.
	var again view
	e.MustDo("POST", e.SP("/templates/research"), nil, &again)
	if again.ZoneName != "browsing" {
		t.Fatalf("a template bot without a zone: %+v", again)
	}
	var ledger view
	e.MustDo("POST", e.SP("/bots"), map[string]string{"name": "Ledger", "job": "Checks invoices", "zone_id": zone["browsing"]}, &ledger)
	if ledger.ZoneName != "browsing" {
		t.Fatalf("a new bot with a zone: %+v", ledger)
	}

	var moved view
	e.MustDo("PUT", e.SP("/bots/%s/zone", research.ID), map[string]string{"zone_id": zone["browsing"]}, &moved)
	if moved.ZoneName != "browsing" {
		t.Fatalf("after the move: %+v", moved)
	}
	var log struct {
		Rows []struct {
			Action string `json:"action"`
		} `json:"rows"`
	}
	if code, body := e.Do("GET", e.SP("/audit?action=bot.zone"), nil, &log); code != 200 || !strings.Contains(body, `"bot.zone"`) {
		t.Fatalf("no audit row for the move: %d %s", code, body)
	}

	// A bot with open work stays where it is.
	ctx := context.Background()
	space, _ := uuid.Parse(e.Space)
	botID, _ := uuid.Parse(research.ID)
	env := &envelopes.Envelope{SpaceID: space, Kind: "message", Origin: "user", Requester: envelopes.Requester{Type: "user", ID: e.Owner}, OwnerBotID: &botID, Goal: "Compare desks"}
	if err := e.App.Envs.Create(ctx, env, nil); err != nil {
		t.Fatal(err)
	}
	if err := e.App.DB.Engine.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		drel.Repo(tx, runs.RunMeta).Add(&runs.Run{SpaceID: space, EnvelopeID: env.ID(), BotID: botID, Kind: "owner", Status: runs.StatusWaiting})
		return tx.SaveChanges(ctx)
	}); err != nil {
		t.Fatal(err)
	}
	if code, body := e.Do("PUT", e.SP("/bots/%s/zone", research.ID), map[string]string{"zone_id": zone["personal"]}, nil); code != 409 {
		t.Fatalf("a bot with open work moved: %d %s", code, body)
	}
}
