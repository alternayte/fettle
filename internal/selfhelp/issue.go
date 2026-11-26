package selfhelp

import (
	"fmt"
	"net/url"
	"strings"
)

// MaxURL is the longest new-issue URL. Browsers and GitHub refuse much longer URLs.
const MaxURL = 7500

// DefaultRepo gets the issues when the setting fettle.issue_repo is empty.
const DefaultRepo = "alternayte/fettle"

// Diagnostics are the facts about the instance that an issue carries.
type Diagnostics struct {
	Version          string   `json:"version"`
	OS               string   `json:"os"`
	Arch             string   `json:"arch"`
	Mode             string   `json:"mode"`
	ComputerProvider string   `json:"computer_provider"`
	ModelProviders   []string `json:"model_providers"`
	FailedChecks     []string `json:"failed_checks"`
}

// Draft is the text of an issue from a person or a bot.
type Draft struct {
	Title    string `json:"title"`
	What     string `json:"what"`
	Expected string `json:"expected"`
	Steps    string `json:"steps"`
}

// Markdown returns the diagnostics as a Markdown block.
func (d Diagnostics) Markdown() string {
	var b strings.Builder
	b.WriteString("### Diagnostics\n\n")
	row := func(k, v string) {
		if strings.TrimSpace(v) == "" || v == "/" {
			v = "unknown"
		}
		fmt.Fprintf(&b, "- %s: %s\n", k, v)
	}
	row("Version", d.Version)
	row("OS", d.OS+"/"+d.Arch)
	row("Mode", d.Mode)
	row("Computer provider", d.ComputerProvider)
	row("Model providers", strings.Join(d.ModelProviders, ", "))
	if len(d.FailedChecks) == 0 {
		row("Failed doctor checks", "none")
	} else {
		b.WriteString("- Failed doctor checks:\n")
		for _, c := range d.FailedChecks {
			fmt.Fprintf(&b, "  - %s\n", c)
		}
	}
	return b.String()
}

// Body returns the issue body of a draft with the diagnostics. It does not redact.
func Body(dr Draft, d Diagnostics) string {
	var b strings.Builder
	part := func(h, v string) {
		if v = strings.TrimSpace(v); v != "" {
			fmt.Fprintf(&b, "### %s\n\n%s\n\n", h, v)
		}
	}
	part("What happened", dr.What)
	part("What I expected", dr.Expected)
	part("Steps", dr.Steps)
	b.WriteString(d.Markdown())
	return b.String()
}

// Issue is a redacted draft that is ready to open on GitHub.
type Issue struct {
	Repo  string `json:"repo"`
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
	// Cut is set when the URL holds only the start of the body.
	Cut bool `json:"cut"`
}

const cutNote = "\n\n[The text is cut. Paste the full text from Fettle.]"

// Repo returns repo when it has the form owner/name, else DefaultRepo.
func Repo(repo string) string {
	repo = strings.Trim(strings.TrimSpace(repo), "/")
	if strings.Count(repo, "/") != 1 || strings.ContainsAny(repo, " ?#") {
		return DefaultRepo
	}
	return repo
}

// Prepare redacts the draft and the diagnostics and builds the new-issue URL of repo.
// names are more words to hide, such as user names.
func Prepare(repo string, dr Draft, d Diagnostics, names ...string) Issue {
	repo = Repo(repo)
	title := strings.TrimSpace(Redact(dr.Title, names...))
	if r := []rune(title); len(r) > 200 {
		title = string(r[:200])
	}
	body := Redact(Body(dr, d), names...)
	u, cut := NewIssueURL(repo, title, body)
	return Issue{Repo: repo, Title: title, Body: body, URL: u, Cut: cut}
}

// NewIssueURL returns the GitHub new-issue URL with the title and the body. When the URL
// would pass MaxURL, it cuts the body and adds a note, and cut is true.
func NewIssueURL(repo, title, body string) (u string, cut bool) {
	build := func(b string) string {
		q := url.Values{"title": {title}, "body": {b}}
		return "https://github.com/" + repo + "/issues/new?" + q.Encode()
	}
	u = build(body)
	if len(u) <= MaxURL {
		return u, false
	}
	r := []rune(body)
	lo, hi := 0, len(r)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if len(build(string(r[:mid])+cutNote)) <= MaxURL {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return build(string(r[:lo]) + cutNote), true
}
