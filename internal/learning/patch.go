package learning

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Patch limits of §14.3.
const (
	PatchSignals    = 3
	PatchWindow     = 14 * 24 * time.Hour
	PatchMaxChanged = 30
)

// PatchFile is one file of a skill patch: its path inside the skill folder and its new text.
type PatchFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// PatchReason is one reason of a patch, with the signals it answers.
type PatchReason struct {
	Text      string   `json:"text"`
	SignalIDs []string `json:"signal_ids"`
}

// Patch is a proposed change to one skill.
type Patch struct {
	Files   []PatchFile   `json:"files"`
	Reasons []PatchReason `json:"reasons"`
}

// Errors of patch validation.
var (
	ErrPatchScope = errors.New("a patch changes only the files of its skill")
	ErrPatchSize  = errors.New("a patch changes at most 30 lines")
	ErrPatchEmpty = errors.New("the patch changes nothing")
	ErrPatchCite  = errors.New("each reason of a patch cites a signal")
)

// ValidatePatch checks a patch against the current files of its skill (REQ-LRN-004): it
// may change only existing text files of the skill folder, never the bot's instructions,
// approval rules, zones or grants, and at most 30 lines in all. The skill's name stays.
// signalIDs are the signals that the patch may cite.
func ValidatePatch(name string, current map[string]string, p Patch, signalIDs map[string]bool) (int, error) {
	if len(p.Files) == 0 {
		return 0, ErrPatchEmpty
	}
	changed := 0
	seen := map[string]bool{}
	for _, f := range p.Files {
		clean := path.Clean(strings.TrimPrefix(f.Path, "./"))
		if clean != f.Path || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") || seen[clean] {
			return 0, fmt.Errorf("%w: %q", ErrPatchScope, f.Path)
		}
		seen[clean] = true
		old, ok := current[clean]
		if !ok || clean == "evals.json" {
			// A new file, or the gate's own cases: outside what a patch may change.
			return 0, fmt.Errorf("%w: %q", ErrPatchScope, f.Path)
		}
		if clean == "SKILL.md" && frontName(f.Content) != name {
			return 0, fmt.Errorf("%w: the skill keeps its name", ErrPatchScope)
		}
		changed += ChangedLines(old, f.Content)
	}
	if changed == 0 {
		return 0, ErrPatchEmpty
	}
	if changed > PatchMaxChanged {
		return changed, fmt.Errorf("%w: it changes %d", ErrPatchSize, changed)
	}
	if len(p.Reasons) == 0 {
		return changed, ErrPatchCite
	}
	for _, r := range p.Reasons {
		if len(r.SignalIDs) == 0 {
			return changed, ErrPatchCite
		}
		for _, id := range r.SignalIDs {
			if !signalIDs[id] {
				return changed, fmt.Errorf("%w: %s is not a signal of this skill", ErrPatchCite, id)
			}
		}
	}
	return changed, nil
}

// frontName returns the name of SKILL.md's frontmatter.
func frontName(md string) string {
	if !strings.HasPrefix(md, "---") {
		return ""
	}
	end := strings.Index(md[3:], "\n---")
	if end < 0 {
		return ""
	}
	for _, line := range strings.Split(md[3:3+end], "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) == "name" {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

// diffOps is a line diff: ' ' kept, '-' removed, '+' added.
func diffOps(a, b []string) []string {
	n, m := len(a), len(b)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var out []string
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			out = append(out, " "+a[i])
			i, j = i+1, j+1
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, "-"+a[i])
			i++
		default:
			out = append(out, "+"+b[j])
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, "-"+a[i])
	}
	for ; j < m; j++ {
		out = append(out, "+"+b[j])
	}
	return out
}

func lines(s string) []string {
	s = strings.TrimSuffix(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// ChangedLines counts the removed and added lines between two texts.
func ChangedLines(a, b string) int {
	n := 0
	for _, op := range diffOps(lines(a), lines(b)) {
		if op[0] != ' ' {
			n++
		}
	}
	return n
}

// Diff renders a unified-style diff of one file with 2 lines of context.
func Diff(file, a, b string) string {
	ops := diffOps(lines(a), lines(b))
	keep := make([]bool, len(ops))
	for i, op := range ops {
		if op[0] != ' ' {
			for k := max(0, i-2); k <= min(len(ops)-1, i+2); k++ {
				keep[k] = true
			}
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n+++ %s\n", file, file)
	gap := false
	for i, op := range ops {
		if !keep[i] {
			gap = true
			continue
		}
		if gap {
			sb.WriteString("@@\n")
			gap = false
		}
		sb.WriteString(op + "\n")
	}
	return sb.String()
}

// Candidate is a skill with enough recent signals for a patch proposal.
type Candidate struct {
	Skill   string
	Signals []*Signal
	// BotID is the bot with the most of these signals.
	BotID *uuid.UUID
}

// Candidates returns the skills with 3 or more signals in 14 days on runs that loaded
// them (§14.3, step 1).
func (s *Service) Candidates(ctx context.Context, spaceID uuid.UUID) ([]Candidate, error) {
	list, err := s.Signals(ctx, spaceID, s.now().Add(-PatchWindow), 0)
	if err != nil {
		return nil, err
	}
	by := map[string][]*Signal{}
	var order []string
	for _, sig := range list {
		for _, k := range sig.Skills {
			if _, ok := by[k]; !ok {
				order = append(order, k)
			}
			by[k] = append(by[k], sig)
		}
	}
	var out []Candidate
	for _, k := range order {
		sigs := by[k]
		if len(sigs) < PatchSignals {
			continue
		}
		count := map[uuid.UUID]int{}
		var top *uuid.UUID
		for _, sig := range sigs {
			if sig.BotID == nil {
				continue
			}
			count[*sig.BotID]++
			if top == nil || count[*sig.BotID] > count[*top] {
				id := *sig.BotID
				top = &id
			}
		}
		out = append(out, Candidate{Skill: k, Signals: sigs, BotID: top})
	}
	return out, nil
}
