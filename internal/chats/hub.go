//go:build !drelgen

package chats

import (
	"encoding/json"
	"sync"
)

// Event is one realtime event for SSE subscribers.
type Event struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// Hub fans events out to subscribers of a topic, such as "chat:<id>" or "space:<id>".
// A slow subscriber loses events instead of blocking the publisher; the client then
// reloads from the API.
type Hub struct {
	mu   sync.RWMutex
	subs map[string]map[chan Event]struct{}
}

// NewHub returns an empty hub.
func NewHub() *Hub { return &Hub{subs: map[string]map[chan Event]struct{}{}} }

// Subscribe returns a channel of events for topic and a function that ends the subscription.
func (h *Hub) Subscribe(topic string) (<-chan Event, func()) {
	ch := make(chan Event, 256)
	h.mu.Lock()
	if h.subs[topic] == nil {
		h.subs[topic] = map[chan Event]struct{}{}
	}
	h.subs[topic][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		if _, ok := h.subs[topic][ch]; ok {
			delete(h.subs[topic], ch)
			close(ch)
		}
		if len(h.subs[topic]) == 0 {
			delete(h.subs, topic)
		}
		h.mu.Unlock()
	}
}

// Publish sends an event to every subscriber of topic.
func (h *Hub) Publish(topic, typ string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	ev := Event{Type: typ, Data: b}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs[topic] {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Subscribers returns the number of subscribers of topic.
func (h *Hub) Subscribers(topic string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs[topic])
}

// ChatTopic is the topic of a chat.
func ChatTopic(id string) string { return "chat:" + id }

// SpaceTopic is the topic of a space.
func SpaceTopic(id string) string { return "space:" + id }
