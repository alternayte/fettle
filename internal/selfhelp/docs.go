// Package selfhelp lets a bot read the Fettle docs and draft a GitHub issue about Fettle
// with redacted diagnostics.
package selfhelp

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// MaxRead is the largest text that ReadDoc returns.
const MaxRead = 32 << 10

// Page is one docs page.
type Page struct {
	Name  string `json:"name"`
	Title string `json:"title"`
}

// Hit is one docs section that contains the words of a search.
type Hit struct {
	Page    string `json:"page"`
	Section string `json:"section"`
	Snippet string `json:"snippet"`
	score   int
}

// Docs reads the Markdown pages of one file system.
type Docs struct{ fsys fs.FS }

// NewDocs returns the docs of fsys. It reads *.md and specs/*.md.
func NewDocs(fsys fs.FS) *Docs { return &Docs{fsys: fsys} }

func (d *Docs) names() []string {
	var out []string
	for _, pat := range []string{"*.md", "specs/*.md"} {
		m, _ := fs.Glob(d.fsys, pat)
		out = append(out, m...)
	}
	sort.Strings(out)
	return out
}

// Pages lists the pages with their titles.
func (d *Docs) Pages() []Page {
	var out []Page
	for _, n := range d.names() {
		b, err := fs.ReadFile(d.fsys, n)
		if err != nil {
			continue
		}
		out = append(out, Page{Name: n, Title: title(string(b), n)})
	}
	return out
}

func title(md, name string) string {
	for _, l := range strings.Split(md, "\n") {
		if strings.HasPrefix(l, "# ") {
			return strings.TrimSpace(l[2:])
		}
	}
	return strings.TrimSuffix(path.Base(name), ".md")
}

type section struct {
	heading string
	text    string
}

// sections splits a page at its headings of level 1 to 3.
func sections(md string) []section {
	var out []section
	cur := section{}
	var b strings.Builder
	inCode := false
	for _, l := range strings.Split(md, "\n") {
		if strings.HasPrefix(l, "```") {
			inCode = !inCode
		}
		if !inCode && (strings.HasPrefix(l, "# ") || strings.HasPrefix(l, "## ") || strings.HasPrefix(l, "### ")) {
			cur.text = b.String()
			if strings.TrimSpace(cur.text) != "" || cur.heading != "" {
				out = append(out, cur)
			}
			cur = section{heading: strings.TrimSpace(strings.TrimLeft(l, "#"))}
			b.Reset()
		}
		b.WriteString(l)
		b.WriteByte('\n')
	}
	cur.text = b.String()
	out = append(out, cur)
	return out
}

// Search returns at most limit sections that contain the most words of query. Words of
// fewer than 3 letters do not count.
func (d *Docs) Search(query string, limit int) []Hit {
	var words []string
	for _, w := range strings.Fields(strings.ToLower(query)) {
		w = strings.Trim(w, ".,;:!?\"'()`")
		if len([]rune(w)) >= 3 {
			words = append(words, w)
		}
	}
	if len(words) == 0 {
		return nil
	}
	var hits []Hit
	for _, n := range d.names() {
		b, err := fs.ReadFile(d.fsys, n)
		if err != nil {
			continue
		}
		for _, s := range sections(string(b)) {
			low := strings.ToLower(s.text)
			head := strings.ToLower(s.heading)
			score, first := 0, -1
			for _, w := range words {
				c := strings.Count(low, w)
				if c == 0 {
					continue
				}
				score += 10 + min(c, 5)
				if strings.Contains(head, w) {
					score += 5
				}
				if i := strings.Index(low, w); first < 0 || i < first {
					first = i
				}
			}
			if score == 0 {
				continue
			}
			// The specs are for builders of Fettle; a user page comes first.
			if strings.HasPrefix(n, "specs/") {
				score /= 2
			}
			hits = append(hits, Hit{Page: n, Section: s.heading, Snippet: snippet(s.text, first), score: score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

func snippet(text string, at int) string {
	r := []rune(text)
	// at is a byte index; find the rune position.
	pos := len([]rune(text[:max(0, min(at, len(text)))]))
	start := max(0, pos-120)
	end := min(len(r), pos+280)
	s := strings.Join(strings.Fields(string(r[start:end])), " ")
	if start > 0 {
		s = "…" + s
	}
	if end < len(r) {
		s += "…"
	}
	return s
}

// ErrNoPage is returned for a page that does not exist.
var ErrNoPage = errors.New("no docs page has this name")

// ReadDoc returns one page, or one section of it when section is set. The text is cut at
// MaxRead.
func (d *Docs) ReadDoc(page, section string) (string, error) {
	page = strings.TrimPrefix(strings.TrimSpace(page), "docs/")
	if page != "" && !strings.HasSuffix(page, ".md") {
		page += ".md"
	}
	b, err := fs.ReadFile(d.fsys, page)
	if err != nil {
		return "", fmt.Errorf("%w: %s. Call fettle_docs with no arguments to list the pages", ErrNoPage, page)
	}
	text := string(b)
	if s := strings.TrimSpace(section); s != "" {
		want := strings.ToLower(s)
		found := false
		for _, sec := range sections(text) {
			if strings.Contains(strings.ToLower(sec.heading), want) {
				text, found = sec.text, true
				break
			}
		}
		if !found {
			return "", fmt.Errorf("the page %s has no section %q", page, s)
		}
	}
	if len(text) > MaxRead {
		text = strings.ToValidUTF8(text[:MaxRead], "") + "\n\n[The page is cut. Read one section of it.]"
	}
	return text, nil
}
