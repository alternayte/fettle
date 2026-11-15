package app_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/apptest"
	"github.com/alternayte/fettle/internal/audit"
	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
	"github.com/alternayte/fettle/internal/skills"
)

const researchMD = "---\nname: desk-research\ndescription: \"Compare desks.\"\n---\n\n# Desk research\n\n1. Read three pages.\n2. Write a table.\n"
const summaryMD = "---\nname: desk-summary\ndescription: \"Summarize the desk market.\"\n---\n\n# Desk summary\n\n1. Read the notes.\n2. Write five lines.\n"

func TestSCN_LRN_003_GatedSkillPatchProposals(t *testing.T) {
	e := apptest.Start(t, "sqlite")
	e.SetupModel("openai-chat", "", 128000)
	chief := e.Chief()
	ctx := context.Background()
	sp, bot := uuid.MustParse(e.Space), uuid.MustParse(chief.ID)
	owner := audit.Actor{Kind: "user", ID: e.Owner}

	// Two skills with evals.json, both on Chief.
	for _, s := range []struct{ md, evals string }{
		{researchMD, `{"skill_name":"desk-research","evals":[{"id":1,"prompt":"Compare three desks.","expected_output":"A table with a source per desk.","expectations":["Each desk has a source."]}]}`},
		{summaryMD, `{"skill_name":"desk-summary","evals":[{"id":1,"prompt":"Summarize the desk market.","expected_output":"Five lines.","expectations":["The summary is readable."]},{"id":2,"prompt":"List desk brands.","expected_output":"Brands with sources.","expectations":["Each brand has a source."]}]}`},
	} {
		sk, _, err := e.App.Skills.Store(ctx, owner, sp, skills.Files{"SKILL.md": []byte(s.md), "evals.json": []byte(s.evals)}, "local", 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.App.Skills.Assign(ctx, owner, sp, bot, sk.ID(), true); err != nil {
			t.Fatal(err)
		}
	}

	// Three thumbs down on runs that loaded each skill: 3 signals in 14 days.
	e.Fake.AddScript(fakemodel.Script{Name: "runs", Match: fakemodel.Match{User: "[[desk-run]]"}, Steps: []fakemodel.Step{{Text: "Desks: A, B, C."}}})
	for _, name := range []string{"desk-research", "desk-summary"} {
		for i := 0; i < 3; i++ {
			run, _ := e.Post(chief.ChatID, "/"+name+" [[desk-run]] Look at desks.")
			e.WaitRun(run, "done")
			reply := lastBotMessage(e, chief.ChatID)
			e.MustDo("PUT", e.SP("/chats/%s/messages/%s/reaction", chief.ChatID, reply["id"]), map[string]any{"emoji": "👎"}, nil)
		}
	}
	var list struct {
		Signals []struct {
			ID     string   `json:"id"`
			Skills []string `json:"skills"`
		} `json:"signals"`
	}
	e.MustDo("GET", e.SP("/learning/signals"), nil, &list)
	ids := map[string][]string{}
	for _, s := range list.Signals {
		for _, k := range s.Skills {
			ids[k] = append(ids[k], s.ID)
		}
	}
	if len(ids["desk-research"]) != 3 || len(ids["desk-summary"]) != 3 {
		t.Fatalf("signals per skill %v", ids)
	}

	// The scripted patches: one adds sources, one breaks the summary.
	better := strings.Replace(researchMD, "2. Write a table.", "2. Write a table.\n3. Cite a source for each desk.", 1)
	broken := strings.Replace(summaryMD, "2. Write five lines.", "2. Write five lines.\n3. BREAK the summary.", 1)
	e.Fake.AddScript(fakemodel.Script{Name: "patch-research", Match: fakemodel.Match{System: "You improve a skill", User: "Skill desk-research"}, Steps: []fakemodel.Step{{JSON: map[string]any{
		"files": []map[string]any{{"path": "SKILL.md", "content": better}}, "reasons": []map[string]any{{"text": "People want a source for each desk.", "signal_ids": ids["desk-research"]}},
	}}}})
	e.Fake.AddScript(fakemodel.Script{Name: "patch-summary", Match: fakemodel.Match{System: "You improve a skill", User: "Skill desk-summary"}, Steps: []fakemodel.Step{{JSON: map[string]any{
		"files": []map[string]any{{"path": "SKILL.md", "content": broken}}, "reasons": []map[string]any{{"text": "Shorter summaries.", "signal_ids": ids["desk-summary"]}},
	}}}})
	// The evals: the current research skill gives no sources and fails; the patched one
	// passes. The current summary passes; the broken one fails.
	e.Fake.AddScript(fakemodel.Script{Name: "eval-research-old", Match: fakemodel.Match{User: "Compare three desks."}, Steps: []fakemodel.Step{{Text: "Desk A, desk B, desk C."}}})
	e.Fake.AddScript(fakemodel.Script{Name: "eval-research-new", Match: fakemodel.Match{System: "Cite a source for each desk", User: "Compare three desks."}, Steps: []fakemodel.Step{{Text: "Desk A [shop.test/a], desk B [shop.test/b], desk C [shop.test/c]."}}})
	e.Fake.AddScript(fakemodel.Script{Name: "eval-summary-old", Match: fakemodel.Match{User: "Summarize the desk market."}, Steps: []fakemodel.Step{{Text: "Five clear lines about desks."}}})
	e.Fake.AddScript(fakemodel.Script{Name: "eval-summary-new", Match: fakemodel.Match{System: "BREAK the summary", User: "Summarize the desk market."}, Steps: []fakemodel.Step{{Text: "Broken text."}}})
	e.Fake.AddScript(fakemodel.Script{Name: "verify-nosource", Match: fakemodel.Match{System: "You verify", User: "Desk A, desk B, desk C."}, Steps: []fakemodel.Step{{JSON: map[string]any{"pass": false, "reason": "No sources."}}}})
	// The broken patch also fixes case 2, so the mean stays and only the rule that a
	// passing case may not fail rejects it.
	e.Fake.AddScript(fakemodel.Script{Name: "eval-brands-old", Match: fakemodel.Match{User: "List desk brands."}, Steps: []fakemodel.Step{{Text: "Brands without sources."}}})
	e.Fake.AddScript(fakemodel.Script{Name: "eval-brands-new", Match: fakemodel.Match{System: "BREAK the summary", User: "List desk brands."}, Steps: []fakemodel.Step{{Text: "Brand X [shop.test/x]."}}})
	e.Fake.AddScript(fakemodel.Script{Name: "verify-brands", Match: fakemodel.Match{System: "You verify", User: "Brands without sources."}, Steps: []fakemodel.Step{{JSON: map[string]any{"pass": false, "reason": "No sources."}}}})
	e.Fake.AddScript(fakemodel.Script{Name: "verify-broken", Match: fakemodel.Match{System: "You verify", User: "Broken text."}, Steps: []fakemodel.Step{{JSON: map[string]any{"pass": false, "reason": "Not readable."}}}})

	var out struct {
		Skills []struct {
			Skill    string  `json:"skill"`
			Outcome  string  `json:"outcome"`
			Reason   string  `json:"reason"`
			CardID   string  `json:"card_id"`
			OldScore float64 `json:"old_score"`
			NewScore float64 `json:"new_score"`
		} `json:"skills"`
	}
	e.MustDo("POST", e.SP("/learning/patches/run"), nil, &out)
	got := map[string]string{}
	cardID := ""
	for _, s := range out.Skills {
		got[s.Skill] = s.Outcome
		if s.Skill == "desk-research" {
			cardID = s.CardID
			if s.OldScore != 0 || s.NewScore != 1 {
				t.Fatalf("eval delta %+v", s)
			}
		}
		if s.Skill == "desk-summary" && !strings.Contains(s.Reason, "passed before") {
			t.Fatalf("the broken patch: %+v", s)
		}
	}
	if got["desk-research"] != "proposed" || got["desk-summary"] != "rejected" {
		t.Fatalf("outcomes %+v", out.Skills)
	}

	// One card, for the research patch, with the diff and the eval delta.
	var patches []map[string]any
	for _, m := range e.Messages(chief.ChatID) {
		if c, ok := m["card"].(map[string]any); ok && c["kind"] == "patch" {
			patches = append(patches, c)
		}
	}
	if len(patches) != 1 || patches[0]["id"] != cardID {
		t.Fatalf("patch cards %v", patches)
	}
	data, _ := patches[0]["data"].(map[string]any)
	preview, _ := patches[0]["preview"].(map[string]any)
	if d, _ := data["diff"].(string); !strings.Contains(d, "+3. Cite a source for each desk.") {
		t.Fatalf("diff %q", data["diff"])
	}
	if p, _ := preview["Eval score"].(string); !strings.HasPrefix(p, "0.00 → 1.00") {
		t.Fatalf("preview %v", preview)
	}

	// Apply makes a new version of the skill.
	e.MustDo("POST", e.SP("/cards/%s/answer", cardID), map[string]any{"action": "apply"}, nil)
	sk, err := e.App.Skills.ByName(ctx, sp, "desk-research")
	if err != nil {
		t.Fatal(err)
	}
	if sk.CurrentVersion != 2 {
		t.Fatalf("version %d after Apply, want 2", sk.CurrentVersion)
	}
	v, _ := e.App.Skills.Version(ctx, sk, 2)
	if !strings.Contains(v.SkillMD, "Cite a source for each desk.") {
		t.Fatalf("version 2:\n%s", v.SkillMD)
	}

	// Without new signals the job proposes nothing again.
	e.MustDo("POST", e.SP("/learning/patches/run"), nil, &out)
	for _, s := range out.Skills {
		if s.Outcome != "skipped" {
			t.Fatalf("a second run without new signals: %+v", out.Skills)
		}
	}
}
