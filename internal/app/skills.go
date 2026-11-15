package app

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/agent"
	"github.com/alternayte/fettle/internal/audit"
	"github.com/alternayte/fettle/internal/computers"
	"github.com/alternayte/fettle/internal/runs"
	"github.com/alternayte/fettle/internal/settings"
	"github.com/alternayte/fettle/internal/skills"
)

func init() {
	settings.Register(settings.Def{Key: "skael.url", Scope: "space", Default: "", Doc: "The Skael registry to pull skills from."})
	settings.Register(settings.Def{Key: "skael.set", Scope: "space", Default: "", Doc: "The Skael set to pull."})
	settings.Register(settings.Def{Key: "skael.key_item", Scope: "space", Default: "", Doc: "The name of the vault secret that holds the Skael API key."})
	settings.Register(settings.Def{Key: "skael.sync_minutes", Scope: "space", Default: 60, Doc: "Minutes between Skael syncs."})
}

// setupSkills connects the skill library to the agent (§13.1).
func (a *App) setupSkills() {
	a.Skills = skills.New(a.DB.Engine, a.Audit, a.Vault, a.Objects)
	a.Skael = &skills.Skael{HTTP: a.HTTP}
	a.Agent.SkillIndex = func(ctx context.Context, spaceID, botID uuid.UUID) []agent.Skill {
		list, err := a.Skills.ForBot(ctx, spaceID, botID)
		if err != nil {
			return nil
		}
		out := make([]agent.Skill, len(list))
		for i, sk := range list {
			out[i] = agent.Skill{Name: sk.Name, Description: sk.Description}
		}
		return out
	}
	a.Agent.LoadSkill = a.loadSkill
}

// skaelConfig reads the Skael sync of a space; the key comes from the vault.
func (a *App) skaelConfig(ctx context.Context, spaceID uuid.UUID) (skills.SkaelConfig, error) {
	c := skills.SkaelConfig{URL: a.Settings.String(ctx, &spaceID, "skael.url"), Set: a.Settings.String(ctx, &spaceID, "skael.set")}
	name := a.Settings.String(ctx, &spaceID, "skael.key_item")
	if c.URL == "" || c.Set == "" || name == "" {
		return c, skills.ErrNoSkael
	}
	items, err := a.Vault.List(ctx, spaceID)
	if err != nil {
		return c, err
	}
	for _, it := range items {
		if it.Name == name {
			c.Key, err = a.Vault.Reveal(ctx, spaceID, it.ID())
			return c, err
		}
	}
	return c, fmt.Errorf("%w: the vault has no item %q", skills.ErrNoSkael, name)
}

// loadSkill returns a skill's text for a run, copies its files into the bot home and
// reports the activation to Skael.
func (a *App) loadSkill(ctx context.Context, env *agent.ToolEnv, name string) (string, error) {
	l, err := a.Skills.Load(ctx, env.SpaceID, env.BotID, name)
	if err != nil {
		return "", fmt.Errorf("you have no skill %q; use a name from your skill index", name)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Skill %s, version %d:\n%s", l.Skill.Name, l.Version.Version, l.Files["SKILL.md"])
	// evals.json is for the eval runner, not the bot. Without other files the skill needs
	// no computer, so a bot can follow it while its computer still prepares (§15.3).
	files := skills.Files{}
	for n, v := range l.Files {
		if n != "evals.json" {
			files[n] = v
		}
	}
	if len(files) > 1 {
		dir := ".skills/" + l.Skill.Name
		if err := a.copySkill(ctx, env, dir, files); err != nil {
			fmt.Fprintf(&b, "\n\nThe skill's files could not be copied to your home: %v", err)
		} else {
			names := make([]string, 0, len(files))
			for n := range files {
				names = append(names, dir+"/"+n)
			}
			sort.Strings(names)
			fmt.Fprintf(&b, "\n\nThe skill's files are in your home:\n- %s", strings.Join(names, "\n- "))
		}
	}
	if l.Skill.Source == "skael" {
		go func() {
			ctx := context.WithoutCancel(ctx)
			if c, err := a.skaelConfig(ctx, env.SpaceID); err == nil {
				if err := a.Skael.Activation(ctx, c, l.Skill.Name, l.Version.Upstream, env.SpaceID, env.BotID); err != nil {
					a.Log.Warn("skael activation", "skill", l.Skill.Name, "error", err)
				}
			}
		}()
	}
	return b.String(), nil
}

func (a *App) copySkill(ctx context.Context, env *agent.ToolEnv, dir string, files skills.Files) error {
	b, err := a.Bots.Get(ctx, env.SpaceID, env.BotID)
	if err != nil || b.ZoneID == nil {
		return fmt.Errorf("the bot has no computer")
	}
	l, err := a.Computers.Acquire(ctx, *b.ZoneID)
	if err != nil {
		return err
	}
	defer l.Release()
	if err := a.Computers.EnsureBotHome(ctx, l, env.BotID); err != nil {
		return err
	}
	home := computers.BotHome(env.BotID)
	for n, data := range files {
		if err := l.Client.WriteFile(ctx, path.Join(home, dir, n), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// skaelSync pulls the Skael set of each space whose sync is due (maintenance).
type skaelClock struct {
	mu   sync.Mutex
	last map[uuid.UUID]time.Time
}

var skaelLast = &skaelClock{last: map[uuid.UUID]time.Time{}}

// SyncSkael pulls the Skael sets that are due.
func (a *App) SyncSkael(ctx context.Context) {
	rows, err := a.DB.Engine.Query(ctx, "SELECT id FROM spaces")
	if err != nil {
		return
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, sp := range ids {
		c, err := a.skaelConfig(ctx, sp)
		if err != nil {
			continue
		}
		mins := a.Settings.Int(ctx, &sp, "skael.sync_minutes")
		skaelLast.mu.Lock()
		due := time.Since(skaelLast.last[sp]) >= time.Duration(max(mins, 1))*time.Minute
		if due {
			skaelLast.last[sp] = time.Now()
		}
		skaelLast.mu.Unlock()
		if !due {
			continue
		}
		if _, err := a.Skills.Sync(ctx, a.Skael, audit.System, sp, c); err != nil {
			a.Log.Warn("skael sync", "space", sp, "error", err)
		}
	}
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// DraftSkill writes a SKILL.md draft from a finished run: its goal, the steps it took
// with their tools, and its result (Save as skill, §13.1).
func (a *App) DraftSkill(ctx context.Context, spaceID, runID uuid.UUID) (string, error) {
	r, err := a.Runs.Get(ctx, spaceID, runID)
	if err != nil {
		return "", err
	}
	env, err := a.Envs.Get(ctx, spaceID, r.EnvelopeID)
	if err != nil {
		return "", err
	}
	evs, err := a.Journal.Load(ctx, runID)
	if err != nil {
		return "", err
	}
	var steps []string
	result := ""
	for _, ev := range evs {
		switch ev.Type {
		case runs.EvModelTurn:
			calls, text := turnCalls(ev.Data)
			steps = append(steps, calls...)
			if text != "" {
				result = text
			}
		case runs.EvSubagentsStarted:
			// Helpers do the work of a fanout or forked step, so their calls are steps too.
			var d struct {
				Runs []struct {
					RunID string `json:"run_id"`
					Goal  string `json:"goal"`
				} `json:"runs"`
			}
			_ = json.Unmarshal([]byte(ev.Data), &d)
			if len(d.Runs) == 0 {
				continue
			}
			var sb strings.Builder
			fmt.Fprintf(&sb, "Split the work into %d helpers that work in parallel:", len(d.Runs))
			for _, h := range d.Runs {
				fmt.Fprintf(&sb, "\n   - %s", strings.ReplaceAll(strings.TrimSpace(h.Goal), "\n", " "))
				id, err := uuid.Parse(h.RunID)
				if err != nil {
					continue
				}
				hevs, err := a.Journal.Load(ctx, id)
				if err != nil {
					continue
				}
				for _, hev := range hevs {
					if hev.Type != runs.EvModelTurn {
						continue
					}
					calls, _ := turnCalls(hev.Data)
					for _, c := range calls {
						fmt.Fprintf(&sb, "\n     - %s", c)
					}
				}
			}
			steps = append(steps, sb.String(), "Combine the results of the helpers.")
		case runs.EvRunFinished:
			var d struct {
				Summary string `json:"summary"`
			}
			_ = json.Unmarshal([]byte(ev.Data), &d)
			if d.Summary != "" {
				result = d.Summary
			}
		}
	}
	goal := strings.TrimSpace(env.Goal)
	name := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(goal), "-"), "-")
	if len(name) > 40 {
		name = strings.Trim(name[:40], "-")
	}
	if name == "" {
		name = "new-skill"
	}
	desc := strings.ReplaceAll(goal, "\n", " ")
	if len(desc) > 200 {
		desc = desc[:200]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "---\nname: %s\ndescription: %q\n---\n\n# %s\n\n## Steps\n\n", name, desc, strings.SplitN(goal, "\n", 2)[0])
	if len(steps) == 0 {
		b.WriteString("1. Answer the request directly.\n")
	}
	for i, s := range steps {
		fmt.Fprintf(&b, "%d. %s\n", i+1, s)
	}
	if result != "" {
		if len(result) > 1500 {
			result = result[:1500] + "…"
		}
		fmt.Fprintf(&b, "\n## Result of the run that this skill comes from\n\n%s\n", result)
	}
	return a.Vault.Redactor.String(ctx, spaceID, b.String()), nil
}

// turnCalls returns the tool calls of a model turn as skill steps, and the turn's text.
func turnCalls(data string) (calls []string, text string) {
	var d struct {
		Text      string `json:"text"`
		ToolCalls []struct {
			Name string          `json:"name"`
			Args json.RawMessage `json:"args"`
		} `json:"tool_calls"`
	}
	_ = json.Unmarshal([]byte(data), &d)
	for _, c := range d.ToolCalls {
		args := string(c.Args)
		if len(args) > 160 {
			args = args[:160] + "…"
		}
		calls = append(calls, fmt.Sprintf("Use `%s` with %s.", c.Name, args))
	}
	return calls, strings.TrimSpace(d.Text)
}
