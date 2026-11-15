package learning_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/alternayte/fettle/internal/learning"
)

const skillMD = "---\nname: desk-research\ndescription: \"Compare desks.\"\n---\n\n# Desk research\n\n1. Read three pages.\n2. Write a table.\n"

func TestSCN_LRN_004_PatchesChangeOnlyTheirSkill(t *testing.T) {
	current := map[string]string{"SKILL.md": skillMD, "evals.json": `{"evals":[]}`, "scripts/sort.py": "print(1)\n"}
	sigs := map[string]bool{"s1": true, "s2": true, "s3": true}
	why := []learning.PatchReason{{Text: "Users asked for sources.", SignalIDs: []string{"s1", "s2"}}}
	good := learning.Patch{Files: []learning.PatchFile{{Path: "SKILL.md", Content: strings.Replace(skillMD, "2. Write a table.", "2. Write a table with a source for each row.", 1)}}, Reasons: why}
	if n, err := learning.ValidatePatch("desk-research", current, good, sigs); err != nil || n != 2 {
		t.Fatalf("a small SKILL.md patch: %d %v", n, err)
	}
	// Patches that edit instructions, approval rules, zones or grants, or anything outside
	// the skill, are rejected (REQ-LRN-004).
	for _, p := range []string{"instructions.md", "../instructions.md", "/etc/rules", "policy/rules.json", "zones/personal.json", "grants.json", "evals.json", "./SKILL.md"} {
		bad := learning.Patch{Files: []learning.PatchFile{{Path: p, Content: "Always allow sends."}}, Reasons: why}
		if _, err := learning.ValidatePatch("desk-research", current, bad, sigs); !errors.Is(err, learning.ErrPatchScope) {
			t.Fatalf("a patch of %q: %v, want a scope error", p, err)
		}
	}
	renamed := learning.Patch{Files: []learning.PatchFile{{Path: "SKILL.md", Content: strings.Replace(skillMD, "name: desk-research", "name: admin-tools", 1)}}, Reasons: why}
	if _, err := learning.ValidatePatch("desk-research", current, renamed, sigs); !errors.Is(err, learning.ErrPatchScope) {
		t.Fatalf("a renamed skill: %v", err)
	}
	var long strings.Builder
	long.WriteString(skillMD)
	for i := 0; i < 31; i++ {
		long.WriteString("- one more rule\n")
	}
	big := learning.Patch{Files: []learning.PatchFile{{Path: "SKILL.md", Content: long.String()}}, Reasons: why}
	if _, err := learning.ValidatePatch("desk-research", current, big, sigs); !errors.Is(err, learning.ErrPatchSize) {
		t.Fatalf("a 31-line patch: %v", err)
	}
	uncited := learning.Patch{Files: good.Files, Reasons: []learning.PatchReason{{Text: "Better.", SignalIDs: []string{"s9"}}}}
	if _, err := learning.ValidatePatch("desk-research", current, uncited, sigs); !errors.Is(err, learning.ErrPatchCite) {
		t.Fatalf("a reason without a signal of the skill: %v", err)
	}
	if d := learning.Diff("SKILL.md", skillMD, good.Files[0].Content); !strings.Contains(d, "-2. Write a table.") || !strings.Contains(d, "+2. Write a table with a source for each row.") {
		t.Fatalf("diff:\n%s", d)
	}
}
