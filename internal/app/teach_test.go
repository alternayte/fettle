package app_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/alternayte/fettle/internal/app"
	"github.com/alternayte/fettle/internal/apptest"
	fakemodel "github.com/alternayte/fettle/internal/fakes/model"
	"github.com/alternayte/fettle/internal/jobs"
	"github.com/alternayte/fettle/internal/teach"
)

type teachOut struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Error     string `json:"error"`
	CardID    string `json:"card_id"`
	Questions []struct {
		CardID string `json:"card_id"`
		Topic  string `json:"topic"`
		Answer string `json:"answer"`
	} `json:"questions"`
	Draft *struct {
		Name        string `json:"name"`
		SkillMD     string `json:"skill_md"`
		EvalsJSON   string `json:"evals_json"`
		BaseSkillID string `json:"base_skill_id"`
		Diff        string `json:"diff"`
	} `json:"draft"`
	SkillID      string `json:"skill_id"`
	SkillVersion int    `json:"skill_version"`
}

// recordedSession stores a teach session with the steps that a demo would record, ready
// for the questions, as the Done endpoint leaves it.
func recordedSession(t *testing.T, e *apptest.Env, title string, steps []*teach.Step) string {
	t.Helper()
	ctx := context.Background()
	chief := e.Chief()
	sess := &teach.Session{SpaceID: uuid.MustParse(e.Space), BotID: uuid.MustParse(chief.ID), UserID: uuid.MustParse(e.Owner), ChatID: uuid.MustParse(chief.ChatID), Title: title}
	if err := e.App.Teach.Create(ctx, sess); err != nil {
		t.Fatal(err)
	}
	if err := e.App.Teach.AddSteps(ctx, sess.ID(), steps, 0, len(steps)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.App.Teach.Modify(ctx, sess.ID(), func(_ context.Context, s *teach.Session) error {
		s.Status = teach.StatusAsking
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.App.Queue.Insert(ctx, app.TeachArgs{SessionID: sess.ID(), Phase: "questions"}, &river.InsertOpts{Queue: jobs.Background}); err != nil {
		t.Fatal(err)
	}
	return sess.ID().String()
}

func waitTeach(t *testing.T, e *apptest.Env, id string, ok func(teachOut) bool) teachOut {
	t.Helper()
	var s teachOut
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		e.MustDo("GET", e.SP("/teach/%s", id), nil, &s)
		if ok(s) {
			return s
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("teach session %s did not reach the state: %+v", id, s)
	return s
}

// answerAll answers the questions of a session in the chat.
func answerAll(t *testing.T, e *apptest.Env, id string, answers map[string]string) teachOut {
	t.Helper()
	s := waitTeach(t, e, id, func(s teachOut) bool { return len(s.Questions) > 0 || s.Status == "failed" })
	if s.Status == "failed" || len(s.Questions) > 3 {
		t.Fatalf("questions: %+v", s)
	}
	for _, q := range s.Questions {
		e.MustDo("POST", e.SP("/cards/%s/answer", q.CardID), map[string]any{"action": "answer", "value": answers[q.Topic]}, nil)
	}
	return waitTeach(t, e, id, func(s teachOut) bool { return s.Status == "review" || s.Status == "failed" })
}

// A demo of the task of an existing skill drafts a change of that skill: the review shows a
// diff, and Save makes a new version with steps.json and the eval case (SCN-TCH-005).
func TestSCN_TCH_005_TaughtTaskOfASkillIsAPatch(t *testing.T) {
	e := apptest.Start(t, "sqlite")
	e.SetupModel("openai-chat", "", 128000)
	var sk struct {
		ID             string `json:"id"`
		CurrentVersion int    `json:"current_version"`
	}
	e.MustDo("POST", e.SP("/skills"), map[string]any{"skill_md": "---\nname: invoice-total\ndescription: Save the total of an acme invoice.\n---\n\n# Invoice total\n\n## Steps\n\n1. Open http://docs.test/invoices.\n"}, &sk)
	e.Fake.AddScript(fakemodel.Script{Name: "teach-match", Match: fakemodel.Match{System: "You decide whether a task that was done once"}, Steps: []fakemodel.Step{
		{JSON: map[string]any{"match": "invoice-total", "reason": "The same task with another month."}},
	}})
	id := recordedSession(t, e, "Save the acme invoice total", []*teach.Step{
		{Kind: "browser", Action: "navigate", URL: "http://docs.test/invoices"},
		{Kind: "browser", Action: "click", URL: "http://docs.test/invoices", Role: "link", Name: "Invoice 2026-10", Notes: []string{"The newest month."}},
		{Kind: "command", Command: `echo "2026-10,1385.50" >> totals.csv`, Cwd: ".", ExitCode: 0},
	})
	s := answerAll(t, e, id, map[string]string{"inputs": "The month changes.", "success": "totals.csv has a line with the month and its total.", "unclear": "Nothing."})
	if s.Status != "review" || s.Draft == nil {
		t.Fatalf("no draft: %+v", s)
	}
	if s.Draft.Name != "invoice-total" || s.Draft.BaseSkillID != sk.ID || !strings.Contains(s.Draft.Diff, "+") || !strings.Contains(s.Draft.Diff, "Invoice 2026-10") {
		t.Fatalf("the draft is not a patch of the skill: %+v", s.Draft)
	}
	if !strings.Contains(s.Draft.EvalsJSON, "totals.csv has a line") {
		t.Fatalf("evals.json has no case from the answers:\n%s", s.Draft.EvalsJSON)
	}
	var card map[string]any
	for _, m := range e.Messages(e.Chief().ChatID) {
		if c, ok := m["card"].(map[string]any); ok && c["id"] == s.CardID {
			card = c
		}
	}
	data, _ := card["data"].(map[string]any)
	if card == nil || card["kind"] != "teach" || !strings.Contains(data["diff"].(string), "Invoice 2026-10") || !strings.Contains(data["steps"].(string), `"command":"echo`) {
		t.Fatalf("review card %+v", card)
	}

	e.MustDo("POST", e.SP("/teach/%s/save", id), map[string]any{}, &s)
	if s.Status != "saved" || s.SkillID != sk.ID || s.SkillVersion != 2 {
		t.Fatalf("save: %+v", s)
	}
	var got struct {
		Versions []struct {
			Version  int      `json:"version"`
			Files    []string `json:"files"`
			HasEvals bool     `json:"has_evals"`
			SkillMD  string   `json:"skill_md"`
		} `json:"versions"`
	}
	e.MustDo("GET", e.SP("/skills/%s", sk.ID), nil, &got)
	v := got.Versions[0]
	for _, x := range got.Versions {
		if x.Version > v.Version {
			v = x
		}
	}
	if v.Version != 2 || !v.HasEvals || !strings.Contains(strings.Join(v.Files, ","), "steps.json") || !strings.Contains(v.SkillMD, "Invoice 2026-10") {
		t.Fatalf("new version %+v", v)
	}
	// A second save of the same review is refused.
	if code, _ := e.Do("POST", e.SP("/teach/%s/save", id), map[string]any{}, nil); code != 409 {
		t.Fatalf("second save got %d", code)
	}
}
