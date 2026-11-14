package chats

import (
	"github.com/alternayte/drel"
	"github.com/google/uuid"
)

// Chat is one conversation thread.
type Chat struct {
	drel.Model[uuid.UUID] `db:"index=idx_chats_space[space_id]"`
	SpaceID               uuid.UUID `db:"space_id,references=spaces,on_delete=cascade"`
	Kind                  string    `db:"kind,check=kind IN ('direct','group')"`
	Name                  string    `db:"name"`
}

// ChatMember is one bot in a chat.
type ChatMember struct {
	drel.Model[uuid.UUID] `db:"table=chat_members,unique_index=uq_chat_members[chat_id,bot_id]"`
	ChatID                uuid.UUID `db:"chat_id,references=chats,on_delete=cascade"`
	BotID                 uuid.UUID `db:"bot_id,references=bots,on_delete=cascade,index"`
}

// Attachment is a file stored in the object store.
type Attachment struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	Type      string `json:"type"`
	ObjectKey string `json:"object_key"`
}

// Card is an interactive chat item.
type Card struct {
	ID      string            `json:"id"`
	Kind    string            `json:"kind"`
	Status  string            `json:"status"`
	Title   string            `json:"title"`
	Tool    string            `json:"tool,omitempty"`
	Preview map[string]string `json:"preview,omitempty"`
	Options []string          `json:"options,omitempty"`
	Detail  string            `json:"detail,omitempty"`
	HardAsk bool              `json:"hard_ask,omitempty"`
	Answer  string            `json:"answer,omitempty"`
	RunID   string            `json:"run_id,omitempty"`
	Data    map[string]string `json:"data,omitempty"`
}

// Message is one chat message.
type Message struct {
	drel.Model[uuid.UUID] `db:"index=idx_messages_chat_time[chat_id,created_at]"`
	SpaceID               uuid.UUID         `db:"space_id"`
	ChatID                uuid.UUID         `db:"chat_id,references=chats,on_delete=cascade"`
	AuthorKind            string            `db:"author_kind,check=author_kind IN ('user','bot','system')"`
	AuthorID              string            `db:"author_id"`
	Body                  string            `db:"body"`
	Attachments           []Attachment      `db:"attachments"`
	EnvelopeID            *uuid.UUID        `db:"envelope_id,index"`
	Card                  *Card             `db:"card"`
	ReplyTo               *uuid.UUID        `db:"reply_to"`
	Reactions             map[string]string `db:"reactions"`
}
