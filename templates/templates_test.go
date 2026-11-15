package templates

import (
	"testing"

	"github.com/alternayte/fettle/internal/skills"
)

func TestAllTemplatesLoad(t *testing.T) {
	ts, err := All()
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 5 || ts[0].Slug != "chief-of-staff" {
		t.Fatalf("templates %d, first %s", len(ts), ts[0].Slug)
	}
	for _, tp := range ts {
		if tp.Name == "" || tp.Instructions == "" || tp.Skill == "" || tp.Routine.Cron == "" || tp.SkillFiles["SKILL.md"] == nil || tp.SkillFiles["evals.json"] == nil {
			t.Fatalf("template %s is not complete: %+v", tp.Slug, tp)
		}
		front, _, err := skills.ParseMD(string(tp.SkillFiles["SKILL.md"]))
		if err != nil || front.Name != tp.Skill {
			t.Fatalf("template %s: SKILL.md: %v (name %q)", tp.Slug, err, front.Name)
		}
	}
}
