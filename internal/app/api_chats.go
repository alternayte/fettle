package app

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/sse"
	"github.com/google/uuid"

	"github.com/alternayte/fettle/internal/agent"
	"github.com/alternayte/fettle/internal/chats"
)

// ChatView is a chat with its bots.
type ChatView struct {
	ID     string   `json:"id"`
	Kind   string   `json:"kind" enum:"direct,group"`
	Name   string   `json:"name"`
	BotIDs []string `json:"bot_ids"`
}

type ChatPath struct {
	Space uuid.UUID `path:"space" doc:"Space ID"`
	Chat  uuid.UUID `path:"chat"`
}

// MessagePage is one page of chat messages with the run lines of their envelopes.
type MessagePage struct {
	Messages []agent.MessageView `json:"messages"`
	Runs     []agent.RunLine     `json:"runs"`
	Next     string              `json:"next" doc:"Cursor of the next older page, or empty."`
}

// SSE event payloads. Each is raw JSON from the hub with its own type for the event name.
type (
	SSEMessage        json.RawMessage
	SSEDelta          json.RawMessage
	SSEMessageRemoved json.RawMessage
	SSERun            json.RawMessage
	SSEBot            json.RawMessage
)

func (m SSEMessage) MarshalJSON() ([]byte, error)        { return m, nil }
func (m SSEDelta) MarshalJSON() ([]byte, error)          { return m, nil }
func (m SSEMessageRemoved) MarshalJSON() ([]byte, error) { return m, nil }
func (m SSERun) MarshalJSON() ([]byte, error)            { return m, nil }
func (m SSEBot) MarshalJSON() ([]byte, error)            { return m, nil }

func sseData(ev chats.Event) any {
	switch ev.Type {
	case "message":
		return SSEMessage(ev.Data)
	case "delta":
		return SSEDelta(ev.Data)
	case "message_removed":
		return SSEMessageRemoved(ev.Data)
	case "run":
		return SSERun(ev.Data)
	default:
		return SSEBot(ev.Data)
	}
}

// stream sends hub events of topic until the client leaves. A comment every 15 s keeps proxies open.
func (a *App) stream(ctx context.Context, topic string, send sse.Sender) {
	ch, cancel := a.Hub.Subscribe(topic)
	defer cancel()
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	if send(sse.Message{Comment: "ready", Retry: 2000}) != nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if send.Comment("ping") != nil {
				return
			}
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if err := send.Data(sseData(ev)); err != nil {
				return
			}
		}
	}
}

func (a *App) registerChats(api huma.API) {
	// Event names come from the Go type of the data.
	types := map[string]any{"message": SSEMessage{}, "delta": SSEDelta{}, "message_removed": SSEMessageRemoved{}, "run": SSERun{}, "bot": SSEBot{}}

	huma.Register(api, op("list-chats", http.MethodGet, "/spaces/{space}/chats", "List the chats of a space", "chats"), func(ctx context.Context, in *SpacePath) (*struct {
		Body struct {
			Chats []ChatView `json:"chats"`
		}
	}, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, false); err != nil {
			return nil, err
		}
		vs, err := a.Chats.List(ctx, in.Space)
		if err != nil {
			return nil, err
		}
		out := &struct {
			Body struct {
				Chats []ChatView `json:"chats"`
			}
		}{}
		out.Body.Chats = []ChatView{}
		for _, v := range vs {
			cv := ChatView{ID: v.Chat.ID().String(), Kind: v.Chat.Kind, Name: v.Chat.Name, BotIDs: []string{}}
			for _, b := range v.BotIDs {
				cv.BotIDs = append(cv.BotIDs, b.String())
			}
			out.Body.Chats = append(out.Body.Chats, cv)
		}
		return out, nil
	})

	huma.Register(api, op("list-messages", http.MethodGet, "/spaces/{space}/chats/{chat}/messages", "One page of messages, oldest first", "chats"), func(ctx context.Context, in *struct {
		ChatPath
		Before string `query:"before" doc:"Message ID cursor"`
		Limit  int    `query:"limit" minimum:"1" maximum:"200" default:"50"`
	}) (*struct{ Body MessagePage }, error) {
		if _, _, err := a.ctxSpace(ctx, in.Space, false); err != nil {
			return nil, err
		}
		if _, err := a.Chats.Get(ctx, in.Space, in.Chat); err != nil {
			return nil, apiError(err)
		}
		var before *uuid.UUID
		if in.Before != "" {
			id, err := uuid.Parse(in.Before)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity("before must be a message ID")
			}
			before = &id
		}
		ms, next, err := a.Chats.Page(ctx, in.Chat, before, in.Limit)
		if err != nil {
			return nil, err
		}
		page := MessagePage{Messages: []agent.MessageView{}, Runs: []agent.RunLine{}, Next: next}
		seen := map[uuid.UUID]bool{}
		for _, m := range ms {
			page.Messages = append(page.Messages, a.Agent.MessageView(ctx, m))
			if m.EnvelopeID != nil && !seen[*m.EnvelopeID] {
				seen[*m.EnvelopeID] = true
				if r, err := a.Runs.ForEnvelope(ctx, *m.EnvelopeID); err == nil {
					if line, err := a.Agent.RunLineFor(ctx, r); err == nil {
						page.Runs = append(page.Runs, line)
					}
				}
			}
		}
		return &struct{ Body MessagePage }{Body: page}, nil
	})

	huma.Register(api, op("post-message", http.MethodPost, "/spaces/{space}/chats/{chat}/messages", "Send a message. It starts a run, or steers the running one.", "chats"), func(ctx context.Context, in *struct {
		ChatPath
		Body struct {
			Body    string `json:"body" maxLength:"32000"`
			ReplyTo string `json:"reply_to,omitempty"`
		}
	}) (*struct {
		Body struct {
			Message    agent.MessageView `json:"message"`
			EnvelopeID string            `json:"envelope_id,omitempty"`
			RunID      string            `json:"run_id,omitempty"`
			Steered    bool              `json:"steered"`
		}
	}, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, false)
		if err != nil {
			return nil, err
		}
		var reply *uuid.UUID
		if in.Body.ReplyTo != "" {
			id, err := uuid.Parse(in.Body.ReplyTo)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity("reply_to must be a message ID")
			}
			if _, err := a.Chats.GetMessage(ctx, in.Chat, id); err != nil {
				return nil, apiError(err)
			}
			reply = &id
		}
		res, err := a.Agent.PostMessage(ctx, actorOf(p), in.Space, in.Chat, agent.PostInput{Body: in.Body.Body, ReplyTo: reply})
		if err != nil {
			return nil, apiError(err)
		}
		out := &struct {
			Body struct {
				Message    agent.MessageView `json:"message"`
				EnvelopeID string            `json:"envelope_id,omitempty"`
				RunID      string            `json:"run_id,omitempty"`
				Steered    bool              `json:"steered"`
			}
		}{}
		out.Body.Message = a.Agent.MessageView(ctx, res.Message)
		out.Body.Steered = res.Steered
		if res.EnvelopeID != nil {
			out.Body.EnvelopeID = res.EnvelopeID.String()
		}
		if res.RunID != nil {
			out.Body.RunID = res.RunID.String()
		}
		return out, nil
	})

	sse.Register(api, op("chat-stream", http.MethodGet, "/spaces/{space}/chats/{chat}/stream", "Server-sent events of a chat: messages, token deltas and run lines", "chats"), types, func(ctx context.Context, in *ChatPath, send sse.Sender) {
		if _, _, err := a.ctxSpace(ctx, in.Space, false); err != nil {
			return
		}
		if _, err := a.Chats.Get(ctx, in.Space, in.Chat); err != nil {
			return
		}
		a.stream(ctx, chats.ChatTopic(in.Chat.String()), send)
	})

	sse.Register(api, op("space-stream", http.MethodGet, "/spaces/{space}/stream", "Server-sent events of a space: run lines and bot activity", "chats"), types, func(ctx context.Context, in *SpacePath, send sse.Sender) {
		if _, _, err := a.ctxSpace(ctx, in.Space, false); err != nil {
			return
		}
		a.stream(ctx, chats.SpaceTopic(in.Space.String()), send)
	})

	huma.Register(api, op("answer-card", http.MethodPost, "/spaces/{space}/cards/{card}/answer", "Answer a card", "chats"), func(ctx context.Context, in *struct {
		SpacePath
		Card string `path:"card"`
		Body struct {
			Action string `json:"action" enum:"allow,always,deny,answer,continue,stop,add_budget,done,not_done"`
			Value  string `json:"value,omitempty" maxLength:"2000"`
		}
	}) (*struct{ Body agent.MessageView }, error) {
		p, _, err := a.ctxSpace(ctx, in.Space, false)
		if err != nil {
			return nil, err
		}
		m, err := a.Agent.AnswerCard(ctx, actorOf(p), in.Space, in.Card, in.Body.Action, in.Body.Value)
		if err != nil {
			return nil, apiError(err)
		}
		return &struct{ Body agent.MessageView }{Body: a.Agent.MessageView(ctx, m)}, nil
	})
}
