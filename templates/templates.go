// Package templates holds the bot templates that ship with Fettle (§15.5). Each template
// has instructions, one skill with evals.json and one routine, which starts paused.
// This folder is licensed Apache-2.0 (see LICENSE), so templates can be shared freely.
package templates

import (
	"embed"
	"encoding/json"
	"io/fs"
	"path"
	"sort"
	"strings"
)

//go:embed */template.json */instructions.md */skill
var files embed.FS

// Routine is the template's routine.
type Routine struct {
	Name   string `json:"name"`
	Cron   string `json:"cron"`
	Prompt string `json:"prompt"`
}

// Template is one bot template.
type Template struct {
	Slug         string            `json:"slug"`
	Name         string            `json:"name"`
	Job          string            `json:"job"`
	PublicDesc   string            `json:"public_desc"`
	Zone         string            `json:"zone"`
	Skill        string            `json:"skill"`
	Routine      Routine           `json:"routine"`
	Instructions string            `json:"instructions"`
	SkillFiles   map[string][]byte `json:"-"`
}

// All returns the templates, Chief of Staff first.
func All() ([]Template, error) {
	dirs, err := fs.ReadDir(files, ".")
	if err != nil {
		return nil, err
	}
	var out []Template
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		t, err := Get(d.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Slug == "chief-of-staff" || (out[j].Slug != "chief-of-staff" && out[i].Name < out[j].Name)
	})
	return out, nil
}

// Get returns one template.
func Get(slug string) (Template, error) {
	var t Template
	b, err := files.ReadFile(path.Join(slug, "template.json"))
	if err != nil {
		return t, err
	}
	if err := json.Unmarshal(b, &t); err != nil {
		return t, err
	}
	t.Slug = slug
	ins, err := files.ReadFile(path.Join(slug, "instructions.md"))
	if err != nil {
		return t, err
	}
	t.Instructions = strings.TrimSpace(string(ins))
	t.SkillFiles = map[string][]byte{}
	root := path.Join(slug, "skill")
	err = fs.WalkDir(files, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := files.ReadFile(p)
		if err != nil {
			return err
		}
		t.SkillFiles[strings.TrimPrefix(p, root+"/")] = data
		return nil
	})
	return t, err
}
