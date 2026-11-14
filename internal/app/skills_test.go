package app_test

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/alternayte/fettle/internal/apptest"
	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
)

const weeklyV1 = "---\nname: weekly-report\ndescription: Write the weekly report from the week's notes.\n---\n\nVersion one: list the notes.\n"
const weeklyV2 = "---\nname: weekly-report\ndescription: Write the weekly report from the week's notes.\n---\n\nVersion two: group the notes by project, then write three lines each.\n"

type skillOut struct {
	ID string `json:"id"`
}

// weeklySkill stores two versions and gives the skill to Chief.
func weeklySkill(e *apptest.Env) skillOut {
	e.T.Helper()
	var sk skillOut
	e.MustDo("POST", e.SP("/skills"), map[string]any{"skill_md": weeklyV1}, &sk)
	e.MustDo("POST", e.SP("/skills"), map[string]any{"skill_md": weeklyV2}, &sk)
	e.MustDo("PUT", e.SP("/skills/%s/bots/%s", sk.ID, e.Chief().ID), map[string]any{"on": true}, nil)
	return sk
}

func TestSCN_SKL_001_SkillIndexLoadAndVersions(t *testing.T) {
	e := apptest.Start(t, "sqlite")
	e.SetupModel("openai-chat", "", 128000)
	sk := weeklySkill(e)
	e.Fake.AddScript(fakemodel.Script{Name: "load", Match: fakemodel.Match{User: "[[load-skill]]"}, Steps: []fakemodel.Step{
		{ToolCalls: []fakemodel.ScriptCall{{Name: "load_skill", Args: map[string]any{"name": "weekly-report"}}}}, {Text: "Loaded."},
	}})
	chief := e.Chief()
	run, _ := e.Post(chief.ChatID, "[[load-skill]] get ready for the report")
	r := e.WaitRun(run, "done")
	var loaded string
	for _, ev := range r.Events {
		if ev.Type == "tool_call" && strings.Contains(string(ev.Data), "load_skill") {
			loaded = string(ev.Data)
		}
	}
	if !strings.Contains(loaded, "Version two") || strings.Contains(loaded, "Version one") {
		t.Fatalf("load_skill did not load the current version: %s", loaded)
	}
	indexed := false
	for _, q := range e.Fake.Requests() {
		indexed = indexed || strings.Contains(q.System, "- weekly-report: Write the weekly report from the week's notes.")
	}
	if !indexed {
		t.Fatal("the bot's context has no skill index line")
	}
	var detail struct {
		Versions []struct{ Version int } `json:"versions"`
	}
	e.MustDo("GET", e.SP("/skills/%s", sk.ID), nil, &detail)
	if len(detail.Versions) != 2 || detail.Versions[0].Version != 2 || detail.Versions[1].Version != 1 {
		t.Fatalf("version history %+v", detail.Versions)
	}
}

func TestSCN_SKL_002_SlashNameStartsWithTheSkill(t *testing.T) {
	e := apptest.Start(t, "sqlite")
	e.SetupModel("openai-chat", "", 128000)
	weeklySkill(e)
	chief := e.Chief()
	run, _ := e.Post(chief.ChatID, "/weekly-report for this week")
	e.WaitRun(run, "done")
	found := false
	for _, q := range e.Fake.Requests() {
		found = found || (strings.Contains(q.System, "The user started this work with a skill") && strings.Contains(q.System, "Version two"))
	}
	if !found {
		t.Fatal("the envelope did not start with the skill loaded")
	}
}

func TestSCN_SKL_003_ZipRoundTripAndSaveAsSkill(t *testing.T) {
	e := apptest.Start(t, "sqlite")
	e.SetupModel("openai-chat", "", 128000)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := map[string]string{"triage/SKILL.md": "---\nname: triage\ndescription: Sort the inbox.\n---\n\nSort by urgency.\n", "triage/scripts/sort.py": "print('sorted')\n", "triage/evals.json": `{"cases":[]}`}
	for n, c := range files {
		w, _ := zw.Create(n)
		_, _ = w.Write([]byte(c))
	}
	_ = zw.Close()
	req, _ := http.NewRequest("POST", e.URL+"/api/v1"+e.SP("/skills/import"), bytes.NewReader(buf.Bytes()))
	req.Header.Set("Authorization", "Bearer "+e.Token)
	req.Header.Set("Content-Type", "application/zip")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var sk skillOut
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), `"name":"triage"`) {
		t.Fatalf("import %d %s", resp.StatusCode, b)
	}
	sk.ID = strings.Split(strings.Split(string(b), `"id":"`)[1], `"`)[0]
	req, _ = http.NewRequest("GET", e.URL+"/api/v1"+e.SP("/skills/%s/export", sk.ID), nil)
	req.Header.Set("Authorization", "Bearer "+e.Token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("export is not a zip: %v", err)
	}
	var got []string
	for _, f := range zr.File {
		rc, _ := f.Open()
		c, _ := io.ReadAll(rc)
		rc.Close()
		if files[f.Name] != string(c) {
			t.Fatalf("%s differs after the round trip", f.Name)
		}
		got = append(got, f.Name)
	}
	sort.Strings(got)
	if len(got) != 3 {
		t.Fatalf("export has %v", got)
	}

	// A new SKILL.md text keeps the other files of the skill.
	e.MustDo("POST", e.SP("/skills"), map[string]any{"skill_md": "---\nname: triage\ndescription: Sort the inbox.\n---\n\nSort by urgency, then by sender.\n"}, nil)
	var detail struct {
		Versions []struct {
			Version int      `json:"version"`
			Files   []string `json:"files"`
		} `json:"versions"`
	}
	e.MustDo("GET", e.SP("/skills/%s", sk.ID), nil, &detail)
	if len(detail.Versions) != 2 || len(detail.Versions[0].Files) != 3 {
		t.Fatalf("the edit lost files: %+v", detail.Versions)
	}

	// Save as skill drafts a skill with the steps of a finished run.
	e.Fake.AddScript(fakemodel.Script{Name: "steps", Match: fakemodel.Match{User: "[[steps]]"}, Steps: []fakemodel.Step{
		{ToolCalls: []fakemodel.ScriptCall{{Name: "search_history", Args: map[string]any{"query": "invoices"}}}}, {Text: "Found three invoices."},
	}})
	run, _ := e.Post(e.Chief().ChatID, "[[steps]] find the invoices")
	e.WaitRun(run, "done")
	var draft struct {
		SkillMD string `json:"skill_md"`
	}
	e.MustDo("POST", e.SP("/runs/%s/draft-skill", run), nil, &draft)
	if !strings.Contains(draft.SkillMD, "## Steps") || !strings.Contains(draft.SkillMD, "search_history") || !strings.HasPrefix(draft.SkillMD, "---\nname: ") {
		t.Fatalf("draft:\n%s", draft.SkillMD)
	}
	e.MustDo("POST", e.SP("/skills"), map[string]any{"skill_md": draft.SkillMD}, nil)
}
