package app_test

import (
	"fmt"
	"testing"

	"github.com/alternayte/fettle/internal/apptest"
	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
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
					Bots []string `json:"bot_ids"`
				} `json:"skills"`
			}
			e.MustDo("GET", e.SP("/skills"), nil, &lib)
			skillID := ""
			for _, s := range lib.Skills {
				if s.Name == tp.Skill {
					skillID = s.ID
				}
			}
			if skillID == "" {
				t.Fatalf("no skill %s: %+v", tp.Skill, lib.Skills)
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
