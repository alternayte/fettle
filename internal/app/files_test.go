package app_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/apptest"
	"github.com/alternayte/fettle/internal/chats"
	"github.com/alternayte/fettle/internal/testenv"
)

// The files page lists the attachments of bot messages only, and searches their names.
func TestFilesListsBotAttachments(t *testing.T) {
	testenv.Each(t, func(t *testing.T, dialect string) {
		e := apptest.Start(t, dialect)
		ctx := context.Background()
		chief := e.Chief()
		space, chat, bot := uuid.MustParse(e.Space), uuid.MustParse(chief.ChatID), uuid.MustParse(chief.ID)
		for _, m := range []*chats.Message{
			{SpaceID: space, ChatID: chat, AuthorKind: "bot", AuthorID: chief.ID, Body: "Here.", Attachments: []chats.Attachment{{ID: "a1", Name: "acme-report.pdf", Size: 10, Type: "application/pdf"}}},
			{SpaceID: space, ChatID: chat, AuthorKind: "bot", AuthorID: chief.ID, Body: "No file."},
			{SpaceID: space, ChatID: chat, AuthorKind: "user", AuthorID: e.Owner, Body: "Mine.", Attachments: []chats.Attachment{{ID: "u1", Name: "acme-upload.pdf", Size: 5}}},
		} {
			if err := e.App.Chats.Insert(ctx, m, &bot); err != nil {
				t.Fatal(err)
			}
		}
		var out struct {
			Files []struct {
				ID, Name, BotName string
			} `json:"files"`
		}
		e.MustDo("GET", e.SP("/files"), nil, &out)
		if len(out.Files) != 1 || out.Files[0].Name != "acme-report.pdf" {
			t.Fatalf("files %+v", out.Files)
		}
		e.MustDo("GET", e.SP("/files?q=REPORT"), nil, &out)
		if len(out.Files) != 1 {
			t.Fatalf("search: %+v", out.Files)
		}
		e.MustDo("GET", e.SP("/files?q=nothing"), nil, &out)
		if len(out.Files) != 0 {
			t.Fatalf("search for nothing: %+v", out.Files)
		}
	})
}
