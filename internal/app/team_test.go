package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	// The maintenance tick also sends digests with the real clock. A zone where the real time
	// is about noon keeps it before the 23:00 digest, so only this test's calls send it.
	off := 12 - time.Now().UTC().Hour()
	zone := "Etc/GMT"
	if off > 0 {
		zone = fmt.Sprintf("Etc/GMT-%d", off)
	} else if off < 0 {
		zone = fmt.Sprintf("Etc/GMT+%d", -off)
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		t.Fatal(err)
	}
	e.MustDo("PUT", e.SP("/settings/digest.time"), map[string]any{"value": "23:00"}, nil)
	e.MustDo("PATCH", e.SP(""), map[string]any{"time_zone": zone}, nil)
	done, _ := e.Post(chief.ChatID, "Summarize the Q3 notes.")
	e.WaitRun(done, "done")
	failed, _ := e.Post(chief.ChatID, "Book the Acme meeting room.")
	e.WaitRun(failed, "done")
	ctx := context.Background()
	if _, err := e.App.DB.Engine.Exec(ctx, "UPDATE runs SET status = 'failed' WHERE id = $1", uuid.MustParse(failed)); err != nil {
		t.Fatal(err)
	}
	e.MustDo("POST", e.SP("/bots/%s/primary", chief.ID), nil, &reply) // a card that waits
	now := time.Now().In(loc)
	before := time.Date(now.Year(), now.Month(), now.Day(), 22, 59, 0, 0, loc)
	e.App.SendDigests(ctx, before)
	var inboxBot apptest.Bot
	e.MustDo("GET", e.SP("/bots/%s", inbox.ID), nil, &inboxBot)
	for _, m := range e.Messages(inboxBot.ChatID) {
		if strings.Contains(m["body"].(string), "Daily digest") {
			t.Fatal("the digest went out before its time")
		}
	}
	at := time.Date(now.Year(), now.Month(), now.Day(), 23, 1, 0, 0, loc)
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

type postOut struct {
	Message struct {
		ID          string `json:"id"`
		Attachments []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Size int64  `json:"size"`
		} `json:"attachments"`
		ReplyTo   string            `json:"reply_to"`
		Reactions map[string]string `json:"reactions"`
	} `json:"message"`
	RunID string `json:"run_id"`
}

func groupOf(e *apptest.Env, name string, ids ...string) string {
	e.T.Helper()
	var g struct {
		ID string `json:"id"`
	}
	e.MustDo("POST", e.SP("/chats"), map[string]any{"name": name, "bot_ids": ids}, &g)
	return g.ID
}

// runsOf counts the runs that a message started.
func runsOf(e *apptest.Env, msgID string) int {
	e.T.Helper()
	var n int
	if err := e.App.DB.Engine.QueryRow(context.Background(), "SELECT COUNT(*) FROM runs r JOIN envelopes v ON v.id = r.envelope_id WHERE v.message_id = $1 AND r.kind = 'owner'", uuid.MustParse(msgID)).Scan(&n); err != nil {
		e.T.Fatal(err)
	}
	return n
}

// idle waits until no run of the space is active, so the next message starts new runs
// instead of steering busy ones.
func idle(e *apptest.Env) {
	e.T.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		var n int
		_ = e.App.DB.Engine.QueryRow(context.Background(), "SELECT COUNT(*) FROM runs WHERE status NOT IN ('done', 'failed', 'cancelled', 'escalated')").Scan(&n)
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			e.T.Fatalf("%d runs are still active", n)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestSCN_TEAM_007_GroupMentionsAndEveryone(t *testing.T) {
	e := apptest.Start(t, "sqlite")
	e.SetupModel("openai-chat", "", 128000)
	var research, writer apptest.Bot
	e.MustDo("POST", e.SP("/bots"), map[string]any{"name": "Research", "job": "Finds sources"}, &research)
	e.MustDo("POST", e.SP("/bots"), map[string]any{"name": "Writer", "job": "Writes notes"}, &writer)
	group := groupOf(e, "Desk team", e.Chief().ID, research.ID, writer.ID)

	var out postOut
	e.MustDo("POST", e.SP("/chats/%s/messages", group), map[string]any{"body": "What did we decide about the desks?"}, &out)
	if n := runsOf(e, out.Message.ID); n != 1 {
		t.Fatalf("a message without a mention started %d runs, want 1", n)
	}
	idle(e)
	e.MustDo("POST", e.SP("/chats/%s/messages", group), map[string]any{"body": "@Research @Writer compare the desks."}, &out)
	var named int
	_ = e.App.DB.Engine.QueryRow(context.Background(), "SELECT COUNT(DISTINCT r.bot_id) FROM runs r JOIN envelopes v ON v.id = r.envelope_id WHERE v.message_id = $1 AND r.bot_id IN ($2, $3)", uuid.MustParse(out.Message.ID), uuid.MustParse(research.ID), uuid.MustParse(writer.ID)).Scan(&named)
	if n := runsOf(e, out.Message.ID); n != 2 || named != 2 {
		t.Fatalf("two mentions started %d runs, %d of the named bots", n, named)
	}
	idle(e)
	code, body := e.Do("POST", e.SP("/chats/%s/messages", group), map[string]any{"body": "@everyone stand up, please."}, nil)
	if code != 409 || !strings.Contains(body, "wakes 3 bots") {
		t.Fatalf("@everyone without confirm: %d %s", code, body)
	}
	e.MustDo("POST", e.SP("/chats/%s/messages", group), map[string]any{"body": "@everyone stand up, please.", "confirm_everyone": true}, &out)
	if n := runsOf(e, out.Message.ID); n != 3 {
		t.Fatalf("@everyone started %d runs", n)
	}
}

func TestSCN_BOT_003_GroupMessagesAttachmentsRepliesReactions(t *testing.T) {
	e := apptest.Start(t, "sqlite")
	e.SetupModel("openai-chat", "", 128000)
	var research, writer apptest.Bot
	e.MustDo("POST", e.SP("/bots"), map[string]any{"name": "Research", "job": "Finds sources"}, &research)
	e.MustDo("POST", e.SP("/bots"), map[string]any{"name": "Writer", "job": "Writes notes"}, &writer)
	group := groupOf(e, "Writers room", research.ID, writer.ID)

	data := make([]byte, 10<<20)
	for i := range data {
		data[i] = byte(i % 251)
	}
	req, _ := http.NewRequest("POST", e.URL+"/api/v1"+e.SP("/chats/%s/attachments?name=q3-report.bin", group), bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+e.Token)
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var at struct {
		ID   string `json:"id"`
		Size int64  `json:"size"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&at)
	resp.Body.Close()
	if resp.StatusCode != 200 || at.Size != 10<<20 {
		t.Fatalf("upload %d %+v", resp.StatusCode, at)
	}
	var first, second postOut
	e.MustDo("POST", e.SP("/chats/%s/messages", group), map[string]any{"body": "**Q3** notes:\n\n- one\n- two\n\n@Writer read the file.", "attachments": []string{at.ID}}, &first)
	if len(first.Message.Attachments) != 1 || first.Message.Attachments[0].Name != "q3-report.bin" {
		t.Fatalf("attachments %+v", first.Message.Attachments)
	}
	e.MustDo("POST", e.SP("/chats/%s/messages", group), map[string]any{"body": "@Research and this one?", "reply_to": first.Message.ID}, &second)
	if second.Message.ReplyTo != first.Message.ID {
		t.Fatalf("reply_to %q", second.Message.ReplyTo)
	}
	var reacted struct {
		Reactions map[string]string `json:"reactions"`
	}
	e.MustDo("PUT", e.SP("/chats/%s/messages/%s/reaction", group, first.Message.ID), map[string]any{"emoji": "👍"}, &reacted)
	if len(reacted.Reactions) != 1 {
		t.Fatalf("reactions %v", reacted.Reactions)
	}
	req, _ = http.NewRequest("GET", e.URL+"/api/v1"+e.SP("/chats/%s/attachments/%s", group, at.ID), nil)
	req.Header.Set("Authorization", "Bearer "+e.Token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Equal(got, data) {
		t.Fatalf("download gives %d bytes", len(got))
	}
	// An attachment of another chat cannot ride on a message.
	if code, _ := e.Do("POST", e.SP("/chats/%s/messages", e.Chief().ChatID), map[string]any{"body": "here", "attachments": []string{at.ID}}, nil); code != 422 {
		t.Fatalf("a foreign attachment got %d", code)
	}

	// A group has 2 to 6 bots.
	ids := []string{e.Chief().ID}
	for i := 0; i < 6; i++ {
		var b apptest.Bot
		e.MustDo("POST", e.SP("/bots"), map[string]any{"name": "Bot " + string(rune('A'+i)), "job": "x"}, &b)
		ids = append(ids, b.ID)
	}
	if code, _ := e.Do("POST", e.SP("/chats"), map[string]any{"name": "Too big", "bot_ids": ids}, nil); code != 422 {
		t.Fatalf("a group of 7 got %d", code)
	}
}
