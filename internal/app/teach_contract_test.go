//go:build contract

package app_test

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/app"
	"github.com/alternayte/fettle/internal/apptest"
	"github.com/alternayte/fettle/internal/teach"
)

func objectFiles(t *testing.T, dir string) int {
	t.Helper()
	n := 0
	_ = filepath.WalkDir(filepath.Join(dir, "objects"), func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}

// A spoken note goes through the transcribe role to the fake transcription endpoint and
// attaches as text to the current step; Fettle stores no audio. Without a transcribe model,
// the teach view has no microphone (SCN-TCH-004).
func TestSCN_TCH_004_SpokenNotesUseTheTranscribeRole(t *testing.T) {
	e := apptest.Start(t, "sqlite")
	provider := e.SetupModel("openai-chat", "", 128000)
	chief := e.Chief()
	ctx := context.Background()
	sess := &teach.Session{SpaceID: uuid.MustParse(e.Space), BotID: uuid.MustParse(chief.ID), UserID: uuid.MustParse(e.Owner), ChatID: uuid.MustParse(chief.ChatID), Title: "Save the acme invoice total"}
	if err := e.App.Teach.Create(ctx, sess); err != nil {
		t.Fatal(err)
	}
	if err := e.App.Teach.AddSteps(ctx, sess.ID(), []*teach.Step{
		{Kind: "browser", Action: "navigate", URL: "http://docs.test/invoices"},
		{Kind: "browser", Action: "click", URL: "http://docs.test/invoices", Role: "link", Name: "Invoice 2026-10"},
	}, 0, 2); err != nil {
		t.Fatal(err)
	}
	type stepOut struct {
		Seq   int      `json:"seq"`
		Notes []string `json:"notes"`
	}
	var view struct {
		Transcribe bool      `json:"transcribe"`
		Steps      []stepOut `json:"steps"`
	}
	speak := func(audio string) (int, string) {
		req, _ := http.NewRequest("POST", e.URL+app.APIPrefix+e.SP("/teach/%s/notes/audio", sess.ID()), bytes.NewReader([]byte(audio)))
		req.Header.Set("Authorization", "Bearer "+e.Token)
		req.Header.Set("Content-Type", "audio/webm;codecs=opus")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	// Without a transcribe model: no microphone, and no fallback to the owner models.
	e.MustDo("GET", e.SP("/teach/%s", sess.ID()), nil, &view)
	if view.Transcribe {
		t.Fatal("the teach view offers a microphone without a transcribe model")
	}
	if code, body := speak("Pick the newest month."); code != http.StatusConflict {
		t.Fatalf("a spoken note without a transcribe model got %d: %s", code, body)
	}

	// An Anthropic provider has no transcription endpoint.
	var anth struct {
		ID string `json:"id"`
	}
	e.MustDo("POST", e.SP("/model-providers"), map[string]any{"name": "fake anthropic", "api_type": "anthropic-messages", "base_url": e.FakeURL + "/v1", "auth_style": "x_api_key", "key": "sk-fake-test-key-123456"}, &anth)
	if code, _ := e.Do("PUT", e.SP("/model-roles/transcribe"), map[string]any{"targets": []map[string]any{{"provider_id": anth.ID, "model": "fake/whisper"}}}, nil); code != http.StatusUnprocessableEntity {
		t.Fatalf("an Anthropic transcribe target got %d", code)
	}

	e.MustDo("PUT", e.SP("/model-roles/transcribe"), map[string]any{"targets": []map[string]any{{"provider_id": provider, "model": "fake/whisper"}}}, nil)
	e.MustDo("GET", e.SP("/teach/%s", sess.ID()), nil, &view)
	if !view.Transcribe {
		t.Fatal("the teach view has no microphone with a transcribe model")
	}
	before := objectFiles(t, e.Cfg.Data)
	if code, body := speak("Pick the newest month."); code != http.StatusOK {
		t.Fatalf("spoken note: %d %s", code, body)
	}
	e.MustDo("GET", e.SP("/teach/%s", sess.ID()), nil, &view)
	last := view.Steps[len(view.Steps)-1]
	if last.Seq != 2 || len(last.Notes) != 1 || last.Notes[0] != "Pick the newest month." {
		t.Fatalf("the note is not on the current step: %+v", view.Steps)
	}
	heard := false
	for _, r := range e.Fake.Requests() {
		if r.API == "audio-transcriptions" && r.Model == "fake/whisper" {
			heard = true
		}
	}
	if !heard {
		t.Fatal("the fake transcription endpoint got no request")
	}
	if after := objectFiles(t, e.Cfg.Data); after != before {
		t.Fatalf("the object store has %d new objects after a spoken note", after-before)
	}
}
