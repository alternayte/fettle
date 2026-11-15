package app_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/apptest"
)

func primaries(e *apptest.Env) []string {
	e.T.Helper()
	var bs struct{ Bots []apptest.Bot }
	e.MustDo("GET", e.SP("/bots"), nil, &bs)
	var out []string
	for _, b := range bs.Bots {
		if b.IsPrimary {
			out = append(out, b.Name)
		}
	}
	return out
}

func TestSCN_TEAM_010_PrimaryRoleAndDigest(t *testing.T) {
	e := apptest.Start(t, "sqlite")
	e.SetupModel("openai-chat", "", 128000)
	chief := e.Chief()
	if p := primaries(e); len(p) != 1 || p[0] != "Chief" {
		t.Fatalf("primary bots %v", p)
	}
	e.MustDo("PATCH", e.SP("/bots/%s", chief.ID), map[string]any{"name": "Ada Chief", "job": "Chief of Staff", "instructions": ""}, nil)
	if p := primaries(e); len(p) != 1 || p[0] != "Ada Chief" {
		t.Fatalf("after a rename the primary bots are %v", p)
	}
	var inbox apptest.Bot
	e.MustDo("POST", e.SP("/bots"), map[string]any{"name": "Inbox", "job": "Triage mail"}, &inbox)
	var reply struct {
		CardID string `json:"card_id"`
	}
	e.MustDo("POST", e.SP("/bots/%s/primary", inbox.ID), nil, &reply)
	if p := primaries(e); len(p) != 1 || p[0] != "Ada Chief" {
		t.Fatalf("the role moved before the card: %v", p)
	}
	e.MustDo("POST", e.SP("/cards/%s/answer", reply.CardID), map[string]any{"action": "allow"}, nil)
	if p := primaries(e); len(p) != 1 || p[0] != "Inbox" {
		t.Fatalf("after the card the primary bots are %v", p)
	}

	// The daily digest lists finished work, waiting cards and failures.
	e.MustDo("PUT", e.SP("/settings/digest.enabled"), map[string]any{"value": true}, nil)
	e.MustDo("PUT", e.SP("/settings/digest.time"), map[string]any{"value": "08:00"}, nil)
	e.MustDo("PATCH", e.SP(""), map[string]any{"time_zone": "UTC"}, nil)
	done, _ := e.Post(chief.ChatID, "Summarize the Q3 notes.")
	e.WaitRun(done, "done")
	failed, _ := e.Post(chief.ChatID, "Book the Acme meeting room.")
	e.WaitRun(failed, "done")
	ctx := context.Background()
	if _, err := e.App.DB.Engine.Exec(ctx, "UPDATE runs SET status = 'failed' WHERE id = $1", uuid.MustParse(failed)); err != nil {
		t.Fatal(err)
	}
	e.MustDo("POST", e.SP("/bots/%s/primary", chief.ID), nil, &reply) // a card that waits
	now := time.Now().UTC()
	before := time.Date(now.Year(), now.Month(), now.Day(), 7, 59, 0, 0, time.UTC)
	e.App.SendDigests(ctx, before)
	var inboxBot apptest.Bot
	e.MustDo("GET", e.SP("/bots/%s", inbox.ID), nil, &inboxBot)
	for _, m := range e.Messages(inboxBot.ChatID) {
		if strings.Contains(m["body"].(string), "Daily digest") {
			t.Fatal("the digest went out before its time")
		}
	}
	at := time.Date(now.Year(), now.Month(), now.Day(), 8, 1, 0, 0, time.UTC)
	e.App.SendDigests(ctx, at)
	e.App.SendDigests(ctx, at.Add(time.Minute))
	var digests []string
	for _, m := range e.Messages(inboxBot.ChatID) {
		if b, _ := m["body"].(string); strings.Contains(b, "Daily digest") {
			digests = append(digests, b)
		}
	}
	if len(digests) != 1 {
		t.Fatalf("%d digests", len(digests))
	}
	d := digests[0]
	if !strings.Contains(d, "Summarize the Q3 notes.") || !strings.Contains(d, "Make Ada Chief the primary bot") || !strings.Contains(d, "Failures (1)") || !strings.Contains(d, "Book the Acme meeting room.") {
		t.Fatalf("digest:\n%s", d)
	}
}

func TestSCN_BOT_002_DuplicateArchiveDelete(t *testing.T) {
	e := apptest.Start(t, "sqlite")
	e.SetupModel("openai-chat", "", 128000)
	var inbox apptest.Bot
	e.MustDo("POST", e.SP("/bots"), map[string]any{"name": "Inbox", "job": "Triage mail", "instructions": "Sort mail."}, &inbox)
	e.MustDo("PUT", e.SP("/bots/%s/memory", inbox.ID), map[string]any{"content": "Ana is the CFO."}, nil)
	var r struct {
		ID string `json:"id"`
	}
	e.MustDo("POST", e.SP("/routines"), map[string]any{"bot_id": inbox.ID, "name": "Morning", "prompt": "Sort the inbox.", "trigger": "cron", "cron": "0 8 * * *", "tz": "UTC", "notify": "never", "enabled": true}, &r)

	var dup apptest.Bot
	e.MustDo("POST", e.SP("/bots/%s/duplicate", inbox.ID), nil, &dup)
	if dup.Name != "Inbox 2" {
		t.Fatalf("copy name %q", dup.Name)
	}
	var mem struct {
		Content string `json:"content"`
	}
	e.MustDo("GET", e.SP("/bots/%s/memory", dup.ID), nil, &mem)
	if mem.Content != "" {
		t.Fatalf("the copy has memory %q", mem.Content)
	}
	var rs struct {
		Routines []struct {
			BotID   string `json:"bot_id"`
			Enabled bool   `json:"enabled"`
		} `json:"routines"`
	}
	e.MustDo("GET", e.SP("/routines"), nil, &rs)
	copyPaused := false
	for _, x := range rs.Routines {
		if x.BotID == dup.ID {
			copyPaused = !x.Enabled
		}
	}
	if !copyPaused {
		t.Fatalf("the copy's routine is not paused: %+v", rs.Routines)
	}

	e.MustDo("POST", e.SP("/bots/%s/archive", inbox.ID), nil, nil)
	e.MustDo("GET", e.SP("/routines"), nil, &rs)
	for _, x := range rs.Routines {
		if x.BotID == inbox.ID && x.Enabled {
			t.Fatal("archive did not stop the routine")
		}
	}

	if code, _ := e.Do("DELETE", e.SP("/bots/%s?confirm=inbox", dup.ID), nil, nil); code != 422 {
		t.Fatalf("delete without the typed name got %d", code)
	}
	e.MustDo("GET", e.SP("/bots/%s", dup.ID), nil, &dup)
	e.MustDo("DELETE", e.SP("/bots/%s?confirm=%s", dup.ID, "Inbox%202"), nil, nil)
	if code, _ := e.Do("GET", e.SP("/bots/%s", dup.ID), nil, nil); code != 404 {
		t.Fatalf("the deleted bot answers %d", code)
	}
	if code, _ := e.Do("GET", e.SP("/chats/%s/messages", dup.ChatID), nil, nil); code != 404 {
		t.Fatalf("the deleted bot's chat answers %d", code)
	}
	e.MustDo("GET", e.SP("/routines"), nil, &rs)
	for _, x := range rs.Routines {
		if x.BotID == dup.ID {
			t.Fatal("the deleted bot's routine is still there")
		}
	}
	chief := e.Chief()
	if code, _ := e.Do("DELETE", e.SP("/bots/%s?confirm=%s", chief.ID, chief.Name), nil, nil); code != 409 {
		t.Fatalf("deleting the primary bot got %d", code)
	}
}
