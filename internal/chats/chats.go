// Package chats keeps chats, messages, cards and the realtime hub.
package chats

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/alternayte/drel"
	"github.com/google/uuid"
)

// Errors of the chats service.
var (
	ErrNotFound      = errors.New("chat not found")
	ErrMsgNotFound   = errors.New("message not found")
	ErrCardNotFound  = errors.New("card not found")
	ErrCardAnswered  = errors.New("this card already has an answer")
	ErrEmptyMessage  = errors.New("a message needs text or a file")
	ErrMessageLength = errors.New("a message has at most 32,000 characters")
)

// Service manages chats and messages.
type Service struct {
	e   *drel.Engine
	Hub *Hub
	pg  bool
}

// New returns the chats service.
func New(e *drel.Engine, hub *Hub) *Service {
	return &Service{e: e, Hub: hub, pg: e.DialectName() == "postgres"}
}

// View is a chat with its member bots.
type View struct {
	Chat   *Chat
	BotIDs []uuid.UUID
}

// CreateDirect creates the direct chat of a bot.
func (s *Service) CreateDirect(ctx context.Context, spaceID, botID uuid.UUID, name string) (*Chat, error) {
	c := &Chat{SpaceID: spaceID, Kind: "direct", Name: name}
	err := s.e.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		drel.Repo(tx, ChatMeta).Add(c)
		drel.Repo(tx, ChatMemberMeta).Add(&ChatMember{ChatID: c.ID(), BotID: botID})
		return nil
	})
	return c, err
}

// DirectFor returns the direct chat of a bot.
func (s *Service) DirectFor(ctx context.Context, spaceID, botID uuid.UUID) (*Chat, error) {
	var id uuid.UUID
	err := s.e.QueryRow(ctx, `SELECT c.id FROM chats c JOIN chat_members m ON m.chat_id = c.id
		WHERE c.space_id = $1 AND c.kind = 'direct' AND m.bot_id = $2 LIMIT 1`, spaceID, botID).Scan(&id)
	if err != nil {
		return nil, ErrNotFound
	}
	return s.Get(ctx, spaceID, id)
}

// Get returns a chat of a space.
func (s *Service) Get(ctx context.Context, spaceID, id uuid.UUID) (*Chat, error) {
	c, err := drel.NewRepository(s.e, ChatMeta).Where(Chats.ID.Eq(id)).Where(Chats.SpaceID.Eq(spaceID)).FirstOrNil(ctx)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, ErrNotFound
	}
	return c, nil
}

// Members returns the bot IDs of a chat.
func (s *Service) Members(ctx context.Context, chatID uuid.UUID) ([]uuid.UUID, error) {
	ms, err := drel.NewRepository(s.e, ChatMemberMeta).Where(ChatMembers.ChatID.Eq(chatID)).OrderBy(ChatMembers.CreatedAt.Asc()).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]uuid.UUID, len(ms))
	for i, m := range ms {
		out[i] = m.BotID
	}
	return out, nil
}

// List returns the chats of a space with their members.
func (s *Service) List(ctx context.Context, spaceID uuid.UUID) ([]View, error) {
	cs, err := drel.NewRepository(s.e, ChatMeta).Where(Chats.SpaceID.Eq(spaceID)).OrderBy(Chats.CreatedAt.Asc()).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]View, 0, len(cs))
	for _, c := range cs {
		ids, err := s.Members(ctx, c.ID())
		if err != nil {
			return nil, err
		}
		out = append(out, View{Chat: c, BotIDs: ids})
	}
	return out, nil
}

// Page returns up to limit messages before the cursor message, oldest first, ordered by
// send time and then ID. next is the cursor of the following older page, or empty.
func (s *Service) Page(ctx context.Context, chatID uuid.UUID, before *uuid.UUID, limit int) ([]*Message, string, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `SELECT id FROM messages WHERE chat_id = $1 ORDER BY sent_at DESC, id DESC LIMIT $2`
	args := []any{chatID, limit + 1}
	if before != nil {
		cur, err := s.GetMessage(ctx, chatID, *before)
		if err != nil {
			return nil, "", err
		}
		q = `SELECT id FROM messages WHERE chat_id = $1 AND (sent_at < $2 OR (sent_at = $3 AND id < $4))
			ORDER BY sent_at DESC, id DESC LIMIT $5`
		args = []any{chatID, cur.SentAt, cur.SentAt, cur.ID(), limit + 1}
	}
	rows, err := s.e.Query(ctx, q, args...)
	if err != nil {
		return nil, "", err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, "", err
		}
		ids = append(ids, id)
	}
	rows.Close()
	next := ""
	if len(ids) > limit {
		ids = ids[:limit]
		next = ids[len(ids)-1].String()
	}
	if len(ids) == 0 {
		return []*Message{}, "", nil
	}
	ms, err := drel.NewRepository(s.e, MessageMeta).Where(Messages.ID.In(ids...)).All(ctx)
	if err != nil {
		return nil, "", err
	}
	byID := map[uuid.UUID]*Message{}
	for _, m := range ms {
		byID[m.ID()] = m
	}
	out := make([]*Message, 0, len(ids))
	for i := len(ids) - 1; i >= 0; i-- {
		if m := byID[ids[i]]; m != nil {
			out = append(out, m)
		}
	}
	return out, next, nil
}

// Recent returns the last n messages of a chat, oldest first.
func (s *Service) Recent(ctx context.Context, chatID uuid.UUID, n int) ([]*Message, error) {
	ms, _, err := s.Page(ctx, chatID, nil, n)
	return ms, err
}

// GetMessage returns one message of a chat.
func (s *Service) GetMessage(ctx context.Context, chatID, id uuid.UUID) (*Message, error) {
	m, err := drel.NewRepository(s.e, MessageMeta).Where(Messages.ID.Eq(id)).Where(Messages.ChatID.Eq(chatID)).FirstOrNil(ctx)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, ErrMsgNotFound
	}
	return m, nil
}

// Insert stores a message inside the transaction in ctx (or a new one) and indexes it for search.
// The caller publishes after commit with Publish.
func (s *Service) Insert(ctx context.Context, m *Message, botID *uuid.UUID) error {
	if len([]rune(m.Body)) > 32000 {
		return ErrMessageLength
	}
	if m.Attachments == nil {
		m.Attachments = []Attachment{}
	}
	if m.Reactions == nil {
		m.Reactions = map[string]string{}
	}
	if m.SentAt.IsZero() {
		m.SentAt = time.Now().UTC()
	}
	return s.e.WithTx(ctx, func(ctx context.Context) error {
		tx := drel.MustFromContext(ctx)
		drel.Repo(tx, MessageMeta).Add(m)
		if botID != nil && strings.TrimSpace(m.Body) != "" {
			if err := tx.SaveChanges(ctx); err != nil {
				return err
			}
			return s.index(ctx, tx, *botID, "message", m.ID().String(), m.Body, time.Now().UTC())
		}
		return nil
	})
}

// Index adds text to a bot's search history (messages and run events).
func (s *Service) Index(ctx context.Context, botID uuid.UUID, kind, ref, body string, at time.Time) error {
	return s.e.WithTx(ctx, func(ctx context.Context) error {
		return s.index(ctx, drel.MustFromContext(ctx), botID, kind, ref, body, at)
	})
}

func (s *Service) index(ctx context.Context, tx *drel.Tx, botID uuid.UUID, kind, ref, body string, at time.Time) error {
	if strings.TrimSpace(body) == "" {
		return nil
	}
	if s.pg {
		_, err := tx.Exec(ctx, "INSERT INTO history_search (bot_id, kind, ref, at, body) VALUES ($1, $2, $3, $4, $5)", botID, kind, ref, at, body)
		return err
	}
	_, err := tx.Exec(ctx, "INSERT INTO history_fts (body, bot_id, kind, ref, at) VALUES ($1, $2, $3, $4, $5)", body, botID.String(), kind, ref, at.Format(time.RFC3339))
	return err
}

// Publish sends a message to the chat's subscribers.
func (s *Service) Publish(chatID uuid.UUID, typ string, v any) {
	s.Hub.Publish(ChatTopic(chatID.String()), typ, v)
}

// UpdateBody changes the text of a message.
func (s *Service) UpdateBody(ctx context.Context, id uuid.UUID, body string) (*Message, error) {
	var out *Message
	err := s.e.WithTx(ctx, func(ctx context.Context) error {
		m, err := drel.Repo(drel.MustFromContext(ctx), MessageMeta).Where(Messages.ID.Eq(id)).FirstOrNil(ctx)
		if err != nil {
			return err
		}
		if m == nil {
			return ErrMsgNotFound
		}
		m.Body = body
		out = m
		return nil
	})
	return out, err
}

// FindCard returns the message that holds a card.
func (s *Service) FindCard(ctx context.Context, spaceID uuid.UUID, cardID string) (*Message, error) {
	var id uuid.UUID
	var q string
	if s.pg {
		q = "SELECT id FROM messages WHERE space_id = $1 AND card->>'id' = $2 LIMIT 1"
	} else {
		q = "SELECT id FROM messages WHERE space_id = $1 AND json_extract(card, '$.id') = $2 LIMIT 1"
	}
	if err := s.e.QueryRow(ctx, q, spaceID, cardID).Scan(&id); err != nil {
		return nil, ErrCardNotFound
	}
	return drel.NewRepository(s.e, MessageMeta).Where(Messages.ID.Eq(id)).First(ctx)
}

// SetCard replaces the card of a message inside the transaction in ctx.
func (s *Service) SetCard(ctx context.Context, msgID uuid.UUID, card *Card) (*Message, error) {
	var out *Message
	err := s.e.WithTx(ctx, func(ctx context.Context) error {
		m, err := drel.Repo(drel.MustFromContext(ctx), MessageMeta).Where(Messages.ID.Eq(msgID)).FirstOrNil(ctx)
		if err != nil {
			return err
		}
		if m == nil {
			return ErrMsgNotFound
		}
		c := *card
		m.Card = &c
		out = m
		return nil
	})
	return out, err
}

// Hit is one search result.
type Hit struct {
	Cursor int64     `json:"cursor"`
	Kind   string    `json:"kind"`
	Ref    string    `json:"ref"`
	At     time.Time `json:"at"`
	Text   string    `json:"text"`
}

// Search finds a bot's past messages and run events that contain every word of query.
// It returns hits newest first and the cursor of the next page (0 when none).
func (s *Service) Search(ctx context.Context, botID uuid.UUID, query string, cursor int64, limit int) ([]Hit, int64, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	words := strings.Fields(query)
	if len(words) == 0 {
		return nil, 0, errors.New("the query is empty")
	}
	if cursor <= 0 {
		cursor = 1 << 62
	}
	var rows drel.Rows
	var err error
	if s.pg {
		rows, err = s.e.Query(ctx, `SELECT id, kind, ref, at, body FROM history_search
			WHERE bot_id = $1 AND tsv @@ plainto_tsquery('simple', $2) AND id < $3 ORDER BY id DESC LIMIT $4`, botID, strings.Join(words, " "), cursor, limit+1)
	} else {
		quoted := make([]string, len(words))
		for i, w := range words {
			quoted[i] = `"` + strings.ReplaceAll(w, `"`, `""`) + `"`
		}
		rows, err = s.e.Query(ctx, `SELECT rowid, kind, ref, at, body FROM history_fts
			WHERE history_fts MATCH $1 AND bot_id = $2 AND rowid < $3 ORDER BY rowid DESC LIMIT $4`, strings.Join(quoted, " "), botID.String(), cursor, limit+1)
	}
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var hits []Hit
	for rows.Next() {
		var h Hit
		if s.pg {
			err = rows.Scan(&h.Cursor, &h.Kind, &h.Ref, &h.At, &h.Text)
		} else {
			var at string
			err = rows.Scan(&h.Cursor, &h.Kind, &h.Ref, &at, &h.Text)
			h.At, _ = time.Parse(time.RFC3339, at)
		}
		if err != nil {
			return nil, 0, err
		}
		if len(h.Text) > 600 {
			h.Text = h.Text[:600] + "…"
		}
		hits = append(hits, h)
	}
	var next int64
	if len(hits) > limit {
		hits = hits[:limit]
		next = hits[len(hits)-1].Cursor
	}
	return hits, next, rows.Err()
}
