package selfhelp

import (
	"net/url"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/alternayte/fettle/docs"
)

func TestRedactRemovesEachPrivateDetail(t *testing.T) {
	cases := []struct {
		name, in, gone, want string
	}{
		{"email", "Mail jo.bloggs@acme.example.co.uk for access", "jo.bloggs", "[email]"},
		{"ipv4", "The proxy at 10.20.30.40:3128 refused it", "10.20.30.40", "[ip]"},
		{"ipv6", "agentd on fd00:1234:abcd::17 timed out", "fd00:1234", "[ip]"},
		{"private host", "Could not reach git.corp.acme.internal and build01.acme.lan", "acme.internal", "[host]"},
		{"company host", "The gateway llm-gw.acmecorp.com returned 502", "acmecorp", "[host]"},
		{"linux home", "open /home/jbloggs/.local/share/fettle/fettle.db: locked", "jbloggs", "~/.local/share/fettle/fettle.db"},
		{"mac home", "path /Users/jbloggs/Library/Application Support/Fettle", "jbloggs", "~/Library"},
		{"windows home", `C:\Users\jbloggs\AppData\Fettle`, "jbloggs", `~\AppData`},
		{"user name", "Jo Bloggs asked the bot, and jbloggs saw the error", "Bloggs", "[user]"},
		{"credential url", "HTTPS_PROXY=http://jo:hunter2@proxy.acme.lan:8080", "hunter2", "[credentials]@"},
		{"api key", "key sk-or-v1-0123456789abcdef0123 was refused", "0123456789abcdef", "[token]"},
		{"github token", "token ghp_AbCdEf0123456789AbCdEf0123456789 failed", "AbCdEf0123", "[token]"},
		{"long token", "Bearer eyJhbGciOiJIUzI1NiJ9abc123XYZ456def789", "eyJhbGci", "[token]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Redact(c.in, "Jo Bloggs", "jbloggs")
			if strings.Contains(got, c.gone) {
				t.Fatalf("Redact(%q) = %q, still contains %q", c.in, got, c.gone)
			}
			if !strings.Contains(got, c.want) {
				t.Fatalf("Redact(%q) = %q, want it to contain %q", c.in, got, c.want)
			}
		})
	}
}

func TestRedactKeepsPublicFacts(t *testing.T) {
	in := "Fettle v1.2.3 on linux/amd64 with openrouter.ai and api.anthropic.com; see github.com/alternayte/fettle and README.md, main.go, at 12:30:45. The function buildRunTimelineForSpaceWithoutCards failed."
	if got := Redact(in); got != in {
		t.Fatalf("Redact changed public text:\n%s\n%s", in, got)
	}
}

func TestIssueURLHasRepoTitleAndBodyAndCutsLongBodies(t *testing.T) {
	d := Diagnostics{Version: "1.0.0", OS: "linux", Arch: "amd64", Mode: "local", ComputerProvider: "docker", ModelProviders: []string{"openrouter"}, FailedChecks: []string{"computers: no docker on dev01.acme.lan"}}
	is := Prepare("acme/fettle-fork", Draft{Title: "Browser fails for jo@acme.example", What: "It stops."}, d, "jo")
	u, err := url.Parse(is.URL)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "github.com" || u.Path != "/acme/fettle-fork/issues/new" {
		t.Fatalf("url = %s", is.URL)
	}
	q := u.Query()
	if q.Get("title") != "Browser fails for [email]" || !strings.Contains(q.Get("body"), "- Version: 1.0.0") || strings.Contains(q.Get("body"), "acme.lan") {
		t.Fatalf("title %q body %q", q.Get("title"), q.Get("body"))
	}
	if is.Cut {
		t.Fatal("a short issue is cut")
	}
	if Prepare("not a repo", Draft{Title: "x"}, d).Repo != DefaultRepo {
		t.Fatal("a bad repo does not fall back to the default")
	}

	long := Prepare("", Draft{Title: "Long", What: strings.Repeat("The run stopped with ä error. ", 800)}, d)
	if !long.Cut || len(long.URL) > MaxURL || len(long.URL) < MaxURL-200 {
		t.Fatalf("cut %v, len %d", long.Cut, len(long.URL))
	}
	lu, _ := url.Parse(long.URL)
	if !strings.HasSuffix(lu.Query().Get("body"), cutNote) {
		t.Fatal("the cut body has no note")
	}
	if !strings.Contains(long.Body, "Diagnostics") {
		t.Fatal("the full body lost the diagnostics")
	}
}

func TestDocsListSearchAndRead(t *testing.T) {
	fsys := fstest.MapFS{
		"models.md": {Data: []byte("# Models\n\nIntro.\n\n## Add an OpenRouter key\n\nOpen /settings/models and add the key.\n\n## Roles\n\nOwner role.\n")},
		"web.md":    {Data: []byte("# Web\n\nSearch providers.\n")},
	}
	d := NewDocs(fsys)
	if p := d.Pages(); len(p) != 2 || p[0].Title != "Models" {
		t.Fatalf("pages = %+v", p)
	}
	hits := d.Search("How do I add an OpenRouter key?", 3)
	if len(hits) == 0 || hits[0].Section != "Add an OpenRouter key" || !strings.Contains(hits[0].Snippet, "/settings/models") {
		t.Fatalf("hits = %+v", hits)
	}
	s, err := d.ReadDoc("models", "roles")
	if err != nil || !strings.Contains(s, "Owner role") || strings.Contains(s, "OpenRouter") {
		t.Fatalf("section = %q, %v", s, err)
	}
	if _, err := d.ReadDoc("nope.md", ""); err == nil {
		t.Fatal("a missing page has no error")
	}
}

func TestEmbeddedDocsAnswerHowToAddAModelKey(t *testing.T) {
	d := NewDocs(docs.FS)
	if len(d.Pages()) < 10 {
		t.Fatalf("only %d embedded pages", len(d.Pages()))
	}
	hits := d.Search("How do I add an OpenRouter key?", 5)
	if len(hits) == 0 || strings.HasPrefix(hits[0].Page, "specs/") || !strings.Contains(hits[0].Snippet, "OpenRouter") {
		t.Fatalf("the first hit is not a user page about OpenRouter: %+v", hits)
	}
}
