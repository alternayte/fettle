package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/auth"
	"github.com/alternayte/fettle/internal/chats"
)

// FileView is one file that a bot sent in a chat of the space (§6.2).
type FileView struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	Type      string    `json:"type"`
	ChatID    string    `json:"chat_id"`
	MessageID string    `json:"message_id"`
	BotID     string    `json:"bot_id"`
	BotName   string    `json:"bot_name"`
	SentAt    time.Time `json:"sent_at"`
}

// UserView is one account of the instance (instance admins).
type UserView struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	IsAdmin   bool      `json:"is_admin"`
	Spaces    int       `json:"spaces"`
	CreatedAt time.Time `json:"created_at"`
}

func (a *App) registerFiles(api huma.API) {
	huma.Register(api, op("list-files", http.MethodGet, "/spaces/{space}/files", "The files that bots sent in the chats of the space, newest first. q searches the file names.", "chats"), func(ctx context.Context, in *struct {
		SpacePath
		Q      string `query:"q" maxLength:"200"`
		Before string `query:"before" doc:"Return files sent before this time (RFC 3339), for the next page."`
		Limit  int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
	}) (*struct {
		Body struct {
			Files []FileView `json:"files"`
			Next  string     `json:"next,omitempty"`
		}
	}, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, false); err != nil {
			return nil, err
		}
		before := time.Now().UTC().Add(time.Hour)
		if in.Before != "" {
			t, err := time.Parse(time.RFC3339Nano, in.Before)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity("before must be an RFC 3339 time")
			}
			before = t.UTC()
		}
		files, next, err := a.listFiles(ctx, in.Space, strings.TrimSpace(in.Q), before, in.Limit)
		if err != nil {
			return nil, err
		}
		out := &struct {
			Body struct {
				Files []FileView `json:"files"`
				Next  string     `json:"next,omitempty"`
			}
		}{}
		out.Body.Files, out.Body.Next = files, next
		return out, nil
	})

	huma.Register(api, op("list-users", http.MethodGet, "/admin/users", "The accounts of this instance (instance admins)", "admin"), func(ctx context.Context, _ *struct{}) (*struct {
		Body struct {
			Users []UserView `json:"users"`
		}
	}, error) {
		if p := auth.From(ctx); p == nil || !p.IsAdmin {
			return nil, huma.Error403Forbidden("Only an instance admin can do this.")
		}
		rows, err := a.DB.Engine.Query(ctx, `SELECT u.id, u.email, u.name, u.status, u.is_admin, u.created_at,
			(SELECT COUNT(*) FROM space_members m WHERE m.user_id = u.id) FROM users u ORDER BY u.created_at, u.email`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := &struct {
			Body struct {
				Users []UserView `json:"users"`
			}
		}{}
		out.Body.Users = []UserView{}
		for rows.Next() {
			var v UserView
			var id uuid.UUID
			if err := rows.Scan(&id, &v.Email, &v.Name, &v.Status, &v.IsAdmin, &v.CreatedAt, &v.Spaces); err != nil {
				return nil, err
			}
			v.ID, v.CreatedAt = id.String(), v.CreatedAt.UTC()
			out.Body.Users = append(out.Body.Users, v)
		}
		return out, rows.Err()
	})
}

// listFiles reads bot messages with attachments page by page and filters the names.
func (a *App) listFiles(ctx context.Context, spaceID uuid.UUID, q string, before time.Time, limit int) ([]FileView, string, error) {
	names := map[string]string{}
	if bs, err := a.Bots.List(ctx, spaceID, true); err == nil {
		for _, b := range bs {
			names[b.ID().String()] = b.Name
		}
	}
	att := "m.attachments"
	if a.DB.Postgres {
		att = "m.attachments::text"
	}
	files := []FileView{}
	needle := strings.ToLower(q)
	for len(files) < limit {
		rows, err := a.DB.Engine.Query(ctx, `SELECT m.id, m.chat_id, m.author_id, `+att+`, m.sent_at FROM messages m JOIN chats c ON c.id = m.chat_id
			WHERE c.space_id = $1 AND m.author_kind = 'bot' AND `+att+` <> '[]' AND m.sent_at < $2 ORDER BY m.sent_at DESC LIMIT 200`, spaceID, before)
		if err != nil {
			return nil, "", err
		}
		n := 0
		for rows.Next() {
			n++
			var mid, cid uuid.UUID
			var author, raw string
			var at time.Time
			if err := rows.Scan(&mid, &cid, &author, &raw, &at); err != nil {
				rows.Close()
				return nil, "", err
			}
			before = at
			var list []chats.Attachment
			_ = json.Unmarshal([]byte(raw), &list)
			for _, f := range list {
				if needle != "" && !strings.Contains(strings.ToLower(f.Name), needle) {
					continue
				}
				files = append(files, FileView{ID: f.ID, Name: f.Name, Size: f.Size, Type: f.Type, ChatID: cid.String(), MessageID: mid.String(), BotID: author, BotName: names[author], SentAt: at.UTC()})
			}
		}
		rows.Close()
		if n < 200 {
			return files, "", nil
		}
	}
	if len(files) > limit {
		files = files[:limit]
	}
	return files, files[len(files)-1].SentAt.Format(time.RFC3339Nano), nil
}
