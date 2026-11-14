// Command licensecheck fails when a Go module or a UI package has a licence that is not
// compatible with AGPL-3.0 (C-11).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// allowed lists SPDX identifiers that are compatible with AGPL-3.0 (C-11). 0BSD, CC0-1.0,
// Unlicense, BlueOak-1.0.0 and Python-2.0 are permissive licences of the BSD and MIT kind (D-121).
var allowed = map[string]bool{
	"MIT": true, "MIT-0": true, "BSD-2-Clause": true, "BSD-3-Clause": true, "0BSD": true, "Apache-2.0": true, "ISC": true,
	"MPL-2.0": true, "LGPL-2.1": true, "LGPL-3.0": true, "GPL-3.0": true, "AGPL-3.0": true,
	"CC0-1.0": true, "Unlicense": true, "BlueOak-1.0.0": true, "Python-2.0": true, "Zlib": true,
}

var detectors = []struct {
	id string
	re *regexp.Regexp
}{
	{"AGPL-3.0", regexp.MustCompile(`(?i)GNU AFFERO GENERAL PUBLIC LICENSE`)},
	{"LGPL-3.0", regexp.MustCompile(`(?i)GNU LESSER GENERAL PUBLIC LICENSE\s+Version 3`)},
	{"LGPL-2.1", regexp.MustCompile(`(?i)GNU LESSER GENERAL PUBLIC LICENSE\s+Version 2\.1`)},
	{"GPL-3.0", regexp.MustCompile(`(?i)GNU GENERAL PUBLIC LICENSE\s+Version 3`)},
	{"MPL-2.0", regexp.MustCompile(`(?i)Mozilla Public License,? version 2\.0`)},
	{"Apache-2.0", regexp.MustCompile(`(?i)Apache License(,)?\s+Version 2\.0`)},
	{"BSD-3-Clause", regexp.MustCompile(`(?is)Redistribution and use in source and binary forms.*Neither the name`)},
	{"BSD-3-Clause", regexp.MustCompile(`(?is)Redistribution and use in source and binary forms.*names of its\s+contributors`)},
	{"BSD-2-Clause", regexp.MustCompile(`(?is)Redistribution and use in source and binary forms`)},
	{"MIT", regexp.MustCompile(`(?i)Permission is hereby granted, free of charge`)},
	{"ISC", regexp.MustCompile(`(?i)Permission to use, copy, modify, and(/or)? distribute this software for any`)},
	{"Unlicense", regexp.MustCompile(`(?i)This is free and unencumbered software released into the public domain`)},
	{"CC0-1.0", regexp.MustCompile(`(?i)CC0 1\.0 Universal`)},
	{"Zlib", regexp.MustCompile(`(?i)This software is provided 'as-is', without any express or implied`)},
}

func detect(text string) string {
	for _, d := range detectors {
		if d.re.MatchString(text) {
			return d.id
		}
	}
	return ""
}

func goModules() ([]string, error) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{if .Module}}{{.Module.Path}}\t{{.Module.Dir}}\t{{.Module.Main}}{{end}}", "./...").Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w", err)
	}
	seen := map[string]bool{}
	var problems []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) != 3 || parts[2] == "true" || seen[parts[0]] {
			continue
		}
		seen[parts[0]] = true
		id := licenseOfDir(parts[1])
		if id == "" {
			// Some modules keep the licence at the repository root (sub-module of a larger repo).
			id = licenseOfParent(parts[1])
		}
		if !allowed[id] {
			problems = append(problems, fmt.Sprintf("go module %s: licence %q", parts[0], id))
		}
	}
	return problems, nil
}

func licenseOfDir(dir string) string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range ents {
		n := strings.ToUpper(e.Name())
		isLicense := strings.HasPrefix(n, "LICENSE") || strings.HasPrefix(n, "LICENCE") || strings.HasPrefix(n, "COPYING")
		if e.IsDir() || !isLicense {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		if id := detect(string(b)); id != "" {
			return id
		}
	}
	return ""
}

func licenseOfParent(dir string) string {
	// Module cache paths look like .../github.com/org/repo/sub@v1.2.3; try the repo root of the same version.
	at := strings.LastIndex(dir, "@")
	if at < 0 {
		return ""
	}
	base, ver := dir[:at], dir[at:]
	for p := filepath.Dir(base); strings.Count(p, string(filepath.Separator)) > 3; p = filepath.Dir(p) {
		if id := licenseOfDir(p + ver); id != "" {
			return id
		}
	}
	return ""
}

type pkgJSON struct {
	Name                 string            `json:"name"`
	License              any               `json:"license"`
	Dependencies         map[string]string `json:"dependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
	PeerDependencies     map[string]string `json:"peerDependencies"`
}

func readPkg(dir string) (*pkgJSON, error) {
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return nil, err
	}
	var p pkgJSON
	return &p, json.Unmarshal(b, &p)
}

// findPkg resolves a dependency the way Node does: the nearest node_modules up from dir.
func findPkg(from, root, name string) string {
	for d := from; ; d = filepath.Dir(d) {
		cand := filepath.Join(d, "node_modules", name)
		if st, err := os.Stat(filepath.Join(cand, "package.json")); err == nil && !st.IsDir() {
			return cand
		}
		if d == root || d == filepath.Dir(d) {
			return ""
		}
	}
}

// uiPackages checks the production dependency tree of the UI: the code that ships in the bundle.
func uiPackages(root string) []string {
	var problems []string
	rootPkg, err := readPkg(root)
	if err != nil {
		return []string{"ui package.json: " + err.Error()}
	}
	type item struct{ from, name string }
	var queue []item
	for n := range rootPkg.Dependencies {
		queue = append(queue, item{root, n})
	}
	seen := map[string]bool{}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		dir := findPkg(it.from, root, it.name)
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		p, err := readPkg(dir)
		if err != nil {
			continue
		}
		lic := ""
		switch v := p.License.(type) {
		case string:
			lic = v
		case map[string]any:
			lic, _ = v["type"].(string)
		}
		if lic == "" {
			lic = licenseOfDir(dir)
		}
		if !spdxOK(lic) {
			problems = append(problems, fmt.Sprintf("ui package %s: licence %q", p.Name, lic))
		}
		for n := range p.Dependencies {
			queue = append(queue, item{dir, n})
		}
		for n := range p.OptionalDependencies {
			queue = append(queue, item{dir, n})
		}
	}
	return problems
}

// spdxOK accepts an SPDX expression where every OR branch or every AND term is allowed.
func spdxOK(expr string) bool {
	e := strings.Trim(expr, "() ")
	if e == "" {
		return false
	}
	if strings.Contains(e, " OR ") {
		for _, p := range strings.Split(e, " OR ") {
			if spdxOK(p) {
				return true
			}
		}
		return false
	}
	if strings.Contains(e, " AND ") {
		for _, p := range strings.Split(e, " AND ") {
			if !spdxOK(p) {
				return false
			}
		}
		return true
	}
	e = strings.TrimSuffix(strings.TrimSuffix(e, "-only"), "-or-later")
	return allowed[e]
}

func main() {
	var problems []string
	goProblems, err := goModules()
	if err != nil {
		fmt.Fprintln(os.Stderr, "licensecheck:", err)
		os.Exit(1)
	}
	problems = append(problems, goProblems...)
	for _, dir := range os.Args[1:] {
		problems = append(problems, uiPackages(dir)...)
	}
	sort.Strings(problems)
	problems = uniq(problems)
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, "licensecheck:", p)
	}
	if len(problems) > 0 {
		os.Exit(1)
	}
	fmt.Println("licensecheck: all licences are compatible with AGPL-3.0")
}

func uniq(s []string) []string {
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}
