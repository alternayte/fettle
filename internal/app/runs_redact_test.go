package app_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/apptest"
)

// Redaction must keep each run event valid JSON. A secret name with a quote, or a secret
// that matches a number, once made GET run fail with "error marshaling response", so the
// timeline could not load.
func TestRunEventsStayJSONAfterRedaction(t *testing.T) {
	e := apptest.Start(t, "sqlite")
	e.SetupModel("openai-chat", "", 128000)
	chief := e.Chief()
	sp := uuid.MustParse(e.Space)
	e.App.Vault.Redactor.Register(sp, `Jira "DC" token`, "zq-secret-9911")
	e.App.Vault.Redactor.Register(sp, "pin", "4242")
	run, _ := e.Post(chief.ChatID, "use zq-secret-9911 and count 4242 items")
	r := e.WaitRun(run, "done", "failed", "escalated", "waiting")
	all := ""
	for _, ev := range r.Events {
		if !json.Valid(ev.Data) {
			t.Fatalf("event %d %s is not JSON: %s", ev.Seq, ev.Type, ev.Data)
		}
		all += string(ev.Data)
	}
	if strings.Contains(all, "zq-secret-9911") || !strings.Contains(all, `[secret:Jira \"DC\" token]`) {
		t.Fatalf("secret not redacted: %s", all)
	}
}

// A run whose journal holds an event that is not JSON, written before redaction kept JSON
// valid, still loads; the timeline shows that event as text.
func TestRunWithAnOldBrokenEventLoads(t *testing.T) {
	e := apptest.Start(t, "sqlite")
	e.SetupModel("openai-chat", "", 128000)
	chief := e.Chief()
	run, _ := e.Post(chief.ChatID, "hello")
	e.WaitRun(run, "done", "failed", "escalated", "waiting")
	broken := `{"n":[secret:pin]}`
	if _, err := e.App.DB.Engine.Exec(context.Background(), "UPDATE run_events SET data = $1 WHERE run_id = $2 AND seq = 1", broken, uuid.MustParse(run)); err != nil {
		t.Fatal(err)
	}
	r := e.GetRun(run)
	var got string
	if len(r.Events) == 0 || json.Unmarshal(r.Events[0].Data, &got) != nil || got != broken {
		t.Fatalf("first event: %+v", r.Events)
	}
}
