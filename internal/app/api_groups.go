package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/agent"
	"github.com/alternayte/fettle/internal/chats"
	"github.com/alternayte/fettle/internal/learning"
)

// attachmentKey is where an attachment of a chat lives in the object store. The chat is in
// the key, so a message can only carry attachments of its own chat.
func attachmentKey(spaceID, chatID uuid.UUID, id string) string {
	return fmt.Sprintf("attachments/%s/%s/%s", spaceID, chatID, id)
}

// loadAttachment reads the metadata of an uploaded attachment of a chat.
func (a *App) loadAttachment(ctx context.Context, spaceID, chatID uuid.UUID, id string) (chats.Attachment, error) {
	var at chats.Attachment
	if _, err := uuid.Parse(id); err != nil {
		return at, huma.Error422UnprocessableEntity("an attachment ID is not valid")
	}
	rc, err := a.Objects.Get(ctx, attachmentKey(spaceID, chatID, id)+".json")
	if err != nil {
		return at, huma.Error422UnprocessableEntity("an attachment is not in this chat; upload it first")
	}
	defer rc.Close()
	if err := json.NewDecoder(io.LimitReader(rc, 64<<10)).Decode(&at); err != nil {
		return at, err
	}
	return at, nil
}

// AttachmentData returns the content of an attachment, opened with the space data key.
func (a *App) AttachmentData(ctx context.Context, spaceID uuid.UUID, at chats.Attachment) ([]byte, error) {
	rc, err := a.Objects.Get(ctx, at.ObjectKey)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	sealed, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	return a.Vault.Open(ctx, spaceID, sealed, at.ObjectKey)
}

// ChatView is a chat with its bot IDs.
type groupView struct {
	ID     string   `json:"id"`
	Kind   string   `json:"kind"`
	Name   string   `json:"name"`
	BotIDs []string `json:"bot_ids"`
}

func (a *App) registerGroups(api huma.API) {
	huma.Register(api, op("create-group", http.MethodPost, "/spaces/{space}/chats", "Create a group chat with 2 to 6 bots", "chats"), func(ctx context.Context, in *struct {
		SpacePath
		Body struct {
			Name   string      `json:"name" minLength:"1" maxLength:"60"`
			BotIDs []uuid.UUID `json:"bot_ids" minItems:"1" maxItems:"20"`
		}
	}) (*struct{ Body groupView }, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, false); err != nil {
			return nil, err
		}
		for _, id := range in.Body.BotIDs {
			b, err := a.Bots.Get(ctx, in.Space, id)
			if err != nil {
				return nil, apiError(err)
			}
			if b.Status != "active" {
				return nil, huma.Error422UnprocessableEntity(b.Name + " is archived.")
			}
		}
		c, err := a.Chats.CreateGroup(ctx, in.Space, in.Body.Name, in.Body.BotIDs)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		ids, _ := a.Chats.Members(ctx, c.ID())
		v := groupView{ID: c.ID().String(), Kind: c.Kind, Name: c.Name, BotIDs: []string{}}
		for _, id := range ids {
			v.BotIDs = append(v.BotIDs, id.String())
		}
		return &struct{ Body groupView }{Body: v}, nil
	})

	upload := op("upload-attachment", http.MethodPost, "/spaces/{space}/chats/{chat}/attachments", "Upload a file for the next message of a chat (the setting chat.attachment_max_mb limits it)", "chats")
	upload.MaxBodyBytes = 1 << 30
	huma.Register(api, upload, func(ctx context.Context, in *struct {
		ChatPath
		Name        string `query:"name" minLength:"1" maxLength:"200"`
		ContentType string `header:"Content-Type"`
		RawBody     []byte `contentType:"application/octet-stream"`
	}) (*struct{ Body chats.Attachment }, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, false); err != nil {
			return nil, err
		}
		if _, err := a.Chats.Get(ctx, in.Space, in.Chat); err != nil {
			return nil, apiError(err)
		}
		limit := int64(a.Settings.Int(ctx, &in.Space, "chat.attachment_max_mb")) << 20
		if int64(len(in.RawBody)) > limit {
			return nil, huma.Error413RequestEntityTooLarge(fmt.Sprintf("The file is larger than %d MB.", limit>>20))
		}
		if len(in.RawBody) == 0 {
			return nil, huma.Error422UnprocessableEntity("The file is empty.")
		}
		name := path.Base(strings.ReplaceAll(strings.TrimSpace(in.Name), "\\", "/"))
		if name == "." || name == "/" || name == "" {
			return nil, huma.Error422UnprocessableEntity("The file needs a name.")
		}
		typ := in.ContentType
		if typ == "" || typ == "application/octet-stream" {
			if t := mime.TypeByExtension(path.Ext(name)); t != "" {
				typ = t
			}
		}
		id := uuid.Must(uuid.NewV7()).String()
		at := chats.Attachment{ID: id, Name: name, Size: int64(len(in.RawBody)), Type: typ, ObjectKey: attachmentKey(in.Space, in.Chat, id)}
		sealed, err := a.Vault.Seal(ctx, in.Space, in.RawBody, at.ObjectKey)
		if err != nil {
			return nil, err
		}
		if err := a.Objects.Put(ctx, at.ObjectKey, bytes.NewReader(sealed), int64(len(sealed))); err != nil {
			return nil, err
		}
		meta, _ := json.Marshal(at)
		if err := a.Objects.Put(ctx, at.ObjectKey+".json", bytes.NewReader(meta), int64(len(meta))); err != nil {
			return nil, err
		}
		return &struct{ Body chats.Attachment }{Body: at}, nil
	})

	huma.Register(api, op("download-attachment", http.MethodGet, "/spaces/{space}/chats/{chat}/attachments/{attachment}", "Download an attachment of a chat", "chats"), func(ctx context.Context, in *struct {
		ChatPath
		Attachment string `path:"attachment"`
	}) (*struct {
		ContentType        string `header:"Content-Type"`
		ContentDisposition string `header:"Content-Disposition"`
		Body               []byte
	}, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, false); err != nil {
			return nil, err
		}
		at, err := a.loadAttachment(ctx, in.Space, in.Chat, in.Attachment)
		if err != nil {
			return nil, huma.Error404NotFound("attachment not found")
		}
		data, err := a.AttachmentData(ctx, in.Space, at)
		if err != nil {
			return nil, err
		}
		typ := at.Type
		if typ == "" {
			typ = "application/octet-stream"
		}
		return &struct {
			ContentType        string `header:"Content-Type"`
			ContentDisposition string `header:"Content-Disposition"`
			Body               []byte
		}{ContentType: typ, ContentDisposition: mime.FormatMediaType("attachment", map[string]string{"filename": at.Name}), Body: data}, nil
	})

	huma.Register(api, op("react", http.MethodPut, "/spaces/{space}/chats/{chat}/messages/{message}/reaction", "Set your reaction on a message; the same emoji again clears it", "chats"), func(ctx context.Context, in *struct {
		ChatPath
		Message uuid.UUID `path:"message"`
		Body    struct {
			Emoji string `json:"emoji" maxLength:"16"`
		}
	}) (*struct{ Body agent.MessageView }, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, false)
		if err != nil {
			return nil, err
		}
		if _, err := a.Chats.Get(ctx, in.Space, in.Chat); err != nil {
			return nil, apiError(err)
		}
		m, err := a.Chats.SetReaction(ctx, in.Chat, in.Message, p.UserID.String(), strings.TrimSpace(in.Body.Emoji))
		if err != nil {
			return nil, apiError(err)
		}
		v := a.Agent.MessageView(ctx, m)
		a.Chats.Publish(in.Chat, "message", v)
		// A thumbs down on a bot's reply is a steering signal on the run that wrote it (§14.1).
		if isThumbsDown(in.Body.Emoji) && m.AuthorKind == "bot" {
			sig := learning.Input{SpaceID: in.Space, Type: learning.ThumbsDown, Ref: "message:" + m.ID().String() + ":" + p.UserID.String(), UserID: p.UserID.String(), Detail: truncateText(m.Body, 300)}
			if m.EnvelopeID != nil {
				sig.RunID = a.Agent.OwnerRunOf(ctx, *m.EnvelopeID)
			}
			if bot, err := uuid.Parse(m.AuthorID); err == nil {
				sig.BotID = &bot
			}
			a.Agent.Signal(ctx, sig)
		}
		return &struct{ Body agent.MessageView }{Body: v}, nil
	})
}

// isThumbsDown reports whether a reaction is a thumbs down, with any skin tone.
func isThumbsDown(emoji string) bool {
	return strings.HasPrefix(strings.TrimSpace(emoji), "👎")
}

func truncateText(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
