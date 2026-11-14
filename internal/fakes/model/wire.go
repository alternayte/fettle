package model

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

func str(v any) string {
	s, _ := v.(string)
	return s
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func raw(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// normalize reads a request body of any of the three APIs.
func normalize(api string, b map[string]any) nreq {
	r := nreq{api: api, model: str(b["model"])}
	switch api {
	case "openai-chat":
		for _, mv := range asList(b["messages"]) {
			m := asMap(mv)
			role := str(m["role"])
			switch role {
			case "system":
				r.system += contentText(m["content"])
			case "tool":
				r.messages = append(r.messages, NMessage{Role: "user", ToolResult: contentText(m["content"])})
			default:
				nm := NMessage{Role: role, Text: contentText(m["content"]), Images: contentImages(m["content"])}
				for _, tc := range asList(m["tool_calls"]) {
					nm.ToolCalls = append(nm.ToolCalls, str(asMap(asMap(tc)["function"])["name"]))
				}
				r.messages = append(r.messages, nm)
			}
		}
		for _, tv := range asList(b["tools"]) {
			f := asMap(asMap(tv)["function"])
			r.tools = append(r.tools, toolSpec{Name: str(f["name"]), Schema: raw(f["parameters"])})
		}
		if rf := asMap(b["response_format"]); str(rf["type"]) == "json_schema" {
			r.schema = raw(asMap(rf["json_schema"])["schema"])
		}
	case "openai-responses":
		r.system = str(b["instructions"])
		for _, iv := range asList(b["input"]) {
			it := asMap(iv)
			switch str(it["type"]) {
			case "function_call_output":
				r.messages = append(r.messages, NMessage{Role: "user", ToolResult: str(it["output"])})
			case "function_call":
				// A function call follows its assistant message or stands alone.
				if n := len(r.messages); n > 0 && r.messages[n-1].Role == "assistant" && len(r.messages[n-1].ToolCalls) > 0 {
					r.messages[n-1].ToolCalls = append(r.messages[n-1].ToolCalls, str(it["name"]))
				} else {
					r.messages = append(r.messages, NMessage{Role: "assistant", ToolCalls: []string{str(it["name"])}})
				}
			default:
				role := str(it["role"])
				nm := NMessage{Role: role}
				for _, cv := range asList(it["content"]) {
					c := asMap(cv)
					switch str(c["type"]) {
					case "input_text", "output_text":
						nm.Text += str(c["text"])
					case "input_image":
						nm.Images++
					}
				}
				r.messages = append(r.messages, nm)
			}
		}
		for _, tv := range asList(b["tools"]) {
			t := asMap(tv)
			r.tools = append(r.tools, toolSpec{Name: str(t["name"]), Schema: raw(t["parameters"])})
		}
		if f := asMap(asMap(b["text"])["format"]); str(f["type"]) == "json_schema" {
			r.schema = raw(f["schema"])
		}
	case "anthropic-messages":
		switch s := b["system"].(type) {
		case string:
			r.system = s
		case []any:
			for _, x := range s {
				r.system += str(asMap(x)["text"])
			}
		}
		for _, mv := range asList(b["messages"]) {
			m := asMap(mv)
			role := str(m["role"])
			if s, ok := m["content"].(string); ok {
				r.messages = append(r.messages, NMessage{Role: role, Text: s})
				continue
			}
			nm := NMessage{Role: role}
			var results []string
			for _, cv := range asList(m["content"]) {
				c := asMap(cv)
				switch str(c["type"]) {
				case "text":
					nm.Text += str(c["text"])
				case "image":
					nm.Images++
				case "tool_use":
					nm.ToolCalls = append(nm.ToolCalls, str(c["name"]))
				case "tool_result":
					results = append(results, contentText(c["content"]))
				}
			}
			if len(results) > 0 {
				for _, res := range results {
					r.messages = append(r.messages, NMessage{Role: "user", ToolResult: res})
				}
				if nm.Text == "" {
					continue
				}
			}
			r.messages = append(r.messages, nm)
		}
		forced := str(asMap(b["tool_choice"])["name"])
		for _, tv := range asList(b["tools"]) {
			t := asMap(tv)
			if forced != "" && str(t["name"]) == forced {
				r.schema = raw(t["input_schema"])
				continue
			}
			r.tools = append(r.tools, toolSpec{Name: str(t["name"]), Schema: raw(t["input_schema"])})
		}
	}
	return r
}

func contentText(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case []any:
		s := ""
		for _, p := range c {
			pm := asMap(p)
			if t := str(pm["text"]); t != "" {
				s += t
			}
		}
		return s
	}
	return ""
}

func contentImages(v any) int {
	n := 0
	for _, p := range asList(v) {
		if t := str(asMap(p)["type"]); t == "image_url" || t == "image" || t == "input_image" {
			n++
		}
	}
	return n
}

// streamer writes one scripted step in the wire format of an API.
type streamer struct {
	w       http.ResponseWriter
	api     string
	model   string
	chunk   time.Duration
	cut     bool
	onFirst func()
	started bool
	sent    int
}

func (s *streamer) event(name string, v any) {
	if name != "" {
		fmt.Fprintf(s.w, "event: %s\n", name)
	}
	b, _ := json.Marshal(v)
	fmt.Fprintf(s.w, "data: %s\n\n", b)
	if f, ok := s.w.(http.Flusher); ok {
		f.Flush()
	}
}

// token marks the first token and applies the chunk delay. It reports false when the
// stream must be cut.
func (s *streamer) token() bool {
	if !s.started {
		s.started = true
		if s.onFirst != nil {
			s.onFirst()
		}
	} else if s.chunk > 0 {
		time.Sleep(s.chunk)
	}
	s.sent++
	if s.cut && s.sent > 1 {
		// Abort the connection after the first token (a cut stream).
		panic(http.ErrAbortHandler)
	}
	return true
}

func words(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	cur := ""
	for _, r := range s {
		cur += string(r)
		if r == ' ' || r == '\n' {
			out = append(out, cur)
			cur = ""
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func (s *streamer) write(st Step, schema bool, in, out int64, cost *float64) {
	s.w.Header().Set("Content-Type", "text/event-stream")
	s.w.Header().Set("Cache-Control", "no-cache")
	s.w.WriteHeader(200)
	text := st.Text
	if schema && st.JSON != nil && s.api != "anthropic-messages" {
		b, _ := json.Marshal(st.JSON)
		text = string(b)
	}
	switch s.api {
	case "openai-chat":
		s.openAIChat(st, text, in, out, cost)
	case "openai-responses":
		s.openAIResponses(st, text, in, out, cost)
	default:
		s.anthropic(st, schema, text, in, out)
	}
}

func usageCost(u map[string]any, cost *float64) map[string]any {
	if cost != nil {
		u["cost"] = *cost
	}
	return u
}

func (s *streamer) openAIChat(st Step, text string, in, out int64, cost *float64) {
	id := "chatcmpl-fake"
	for _, w := range words(text) {
		s.token()
		s.event("", map[string]any{"id": id, "model": s.model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": w}}}})
	}
	for i, tc := range st.ToolCalls {
		s.token()
		args, _ := json.Marshal(tc.Args)
		if tc.Args == nil {
			args = []byte("{}")
		}
		half := len(args) / 2
		s.event("", map[string]any{"id": id, "model": s.model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{"index": i, "id": fmt.Sprintf("call_%d_%s", i, tc.Name), "type": "function", "function": map[string]any{"name": tc.Name, "arguments": string(args[:half])}}}}}}})
		s.event("", map[string]any{"id": id, "model": s.model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{"index": i, "function": map[string]any{"arguments": string(args[half:])}}}}}}})
	}
	finish := "stop"
	if len(st.ToolCalls) > 0 {
		finish = "tool_calls"
	}
	s.event("", map[string]any{"id": id, "model": s.model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finish}}})
	s.event("", map[string]any{"id": id, "model": s.model, "choices": []any{}, "usage": usageCost(map[string]any{"prompt_tokens": in, "completion_tokens": out, "total_tokens": in + out}, cost)})
	fmt.Fprint(s.w, "data: [DONE]\n\n")
}

func (s *streamer) openAIResponses(st Step, text string, in, out int64, cost *float64) {
	s.event("response.created", map[string]any{"type": "response.created", "response": map[string]any{"model": s.model, "status": "in_progress"}})
	idx := 0
	if text != "" {
		s.event("response.output_item.added", map[string]any{"type": "response.output_item.added", "output_index": idx, "item": map[string]any{"type": "message", "role": "assistant"}})
		for _, w := range words(text) {
			s.token()
			s.event("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "output_index": idx, "delta": w})
		}
		idx++
	}
	for _, tc := range st.ToolCalls {
		s.token()
		args, _ := json.Marshal(tc.Args)
		if tc.Args == nil {
			args = []byte("{}")
		}
		item := map[string]any{"type": "function_call", "call_id": "call_" + tc.Name + fmt.Sprint(idx), "name": tc.Name, "arguments": ""}
		s.event("response.output_item.added", map[string]any{"type": "response.output_item.added", "output_index": idx, "item": item})
		s.event("response.function_call_arguments.delta", map[string]any{"type": "response.function_call_arguments.delta", "output_index": idx, "delta": string(args)})
		item["arguments"] = string(args)
		s.event("response.output_item.done", map[string]any{"type": "response.output_item.done", "output_index": idx, "item": item})
		idx++
	}
	s.event("response.completed", map[string]any{"type": "response.completed", "response": map[string]any{"model": s.model, "status": "completed", "usage": usageCost(map[string]any{"input_tokens": in, "output_tokens": out}, cost)}})
}

func (s *streamer) anthropic(st Step, schema bool, text string, in, out int64) {
	s.event("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": "msg_fake", "model": s.model, "role": "assistant", "usage": map[string]any{"input_tokens": in, "output_tokens": 1}}})
	idx := 0
	if text != "" && (!schema || st.JSON == nil) {
		s.event("content_block_start", map[string]any{"type": "content_block_start", "index": idx, "content_block": map[string]any{"type": "text", "text": ""}})
		for _, w := range words(text) {
			s.token()
			s.event("content_block_delta", map[string]any{"type": "content_block_delta", "index": idx, "delta": map[string]any{"type": "text_delta", "text": w}})
		}
		s.event("content_block_stop", map[string]any{"type": "content_block_stop", "index": idx})
		idx++
	}
	calls := st.ToolCalls
	if schema && st.JSON != nil {
		calls = append([]ScriptCall{{Name: "structured_output", Args: st.JSON}}, calls...)
	}
	for _, tc := range calls {
		s.token()
		args, _ := json.Marshal(tc.Args)
		if tc.Args == nil {
			args = []byte("{}")
		}
		s.event("content_block_start", map[string]any{"type": "content_block_start", "index": idx, "content_block": map[string]any{"type": "tool_use", "id": "toolu_" + tc.Name + fmt.Sprint(idx), "name": tc.Name, "input": map[string]any{}}})
		half := len(args) / 2
		s.event("content_block_delta", map[string]any{"type": "content_block_delta", "index": idx, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(args[:half])}})
		s.event("content_block_delta", map[string]any{"type": "content_block_delta", "index": idx, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(args[half:])}})
		s.event("content_block_stop", map[string]any{"type": "content_block_stop", "index": idx})
		idx++
	}
	stop := "end_turn"
	if len(st.ToolCalls) > 0 {
		stop = "tool_use"
	}
	s.event("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop}, "usage": map[string]any{"output_tokens": out}})
	s.event("message_stop", map[string]any{"type": "message_stop"})
}
