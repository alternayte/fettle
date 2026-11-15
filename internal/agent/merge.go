package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/alternayte/fettle/internal/runs"
)

type (
	evMerge struct {
		Applied   []string        `json:"applied"`
		Conflicts []mergeConflict `json:"conflicts"`
	}
	mergeConflict struct {
		Path     string         `json:"path"`
		Reason   string         `json:"reason"`
		Versions []mergeVersion `json:"versions"`
	}
	mergeVersion struct {
		Helper  string `json:"helper"`
		Status  string `json:"status"` // added, changed or deleted
		Content string `json:"content,omitempty"`
	}
)

// forkFinish computes the diff of a forked sub-agent and ends its fork, before its
// run_finished commits.
func (x *exec) forkFinish(ctx context.Context) {
	a := x.a
	if !x.forkRun() || a.Forker == nil || x.forkDiff != nil {
		return
	}
	d, err := a.Forker.Finish(ctx, x.run.BotID, x.run.ID(), x.env.Decision.ForkBase)
	if err != nil {
		a.Log.Warn("fork diff", "run", x.run.ID(), "error", a.Vault.Redactor.Global(err.Error()))
		return
	}
	x.forkDiff = d
	if x.forkDiff == nil {
		x.forkDiff = []FileDiff{}
	}
}

// merge applies the diffs of forked sub-agents that touch different files and whose
// base did not change. Other files go to the owner with all versions (§9.4).
func (x *exec) merge(ctx context.Context, st *state) error {
	a := x.a
	type touch struct {
		helper string
		d      FileDiff
	}
	by := map[string][]touch{}
	for _, s := range st.subRuns {
		r, ok := st.subResults[s.RunID]
		if !ok || r.Status != runs.StatusDone {
			continue
		}
		for _, d := range r.Diff {
			by[d.Path] = append(by[d.Path], touch{helper: r.Goal, d: d})
		}
	}
	current := map[string]string{}
	if a.Forker != nil {
		var err error
		if current, err = a.Forker.Current(ctx, x.run.SpaceID, x.run.BotID); err != nil {
			return err
		}
	}
	paths := make([]string, 0, len(by))
	for p := range by {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	ev := evMerge{Applied: []string{}, Conflicts: []mergeConflict{}}
	for _, p := range paths {
		ts := by[p]
		baseChanged := current[p] != st.forkBase[p]
		if len(ts) == 1 && !baseChanged {
			if err := a.Forker.Apply(ctx, x.run.SpaceID, x.run.BotID, ts[0].d); err != nil {
				return err
			}
			ev.Applied = append(ev.Applied, p)
			continue
		}
		c := mergeConflict{Path: p, Reason: "two helpers changed this file"}
		if len(ts) == 1 {
			c.Reason = "the file changed on the computer after the fork"
		}
		for _, t := range ts {
			c.Versions = append(c.Versions, mergeVersion{Helper: t.helper, Status: t.d.Status, Content: truncate(string(t.d.Content), 16000)})
		}
		ev.Conflicts = append(ev.Conflicts, c)
	}
	_, err := x.append(ctx, nil, runs.New{Type: runs.EvMergeResult, Data: ev})
	return err
}

// renderMerge is the merge result in the owner's context.
func renderMerge(m evMerge) string {
	var b strings.Builder
	if len(m.Applied) > 0 {
		fmt.Fprintf(&b, "Fettle applied the helpers' changes to: %s.\n", strings.Join(m.Applied, ", "))
	}
	if len(m.Conflicts) == 0 {
		b.WriteString("No file conflicts.\n")
		return b.String()
	}
	b.WriteString("These files conflict. Read each version, write the right content with write_file (or delete the file), then report.\n")
	for _, c := range m.Conflicts {
		fmt.Fprintf(&b, "\nFile %s (%s):\n", c.Path, c.Reason)
		for _, v := range c.Versions {
			fmt.Fprintf(&b, "- Version of the helper for %q (%s):\n%s\n", v.Helper, v.Status, v.Content)
		}
	}
	return b.String()
}
