package models

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
)

// openAIChat speaks the OpenAI Chat Completions API and compatible APIs.
type openAIChat struct{}

type ocMessage struct {
	Role       string       `json:"role"`
	Content    any          `json:"content,omitempty"`
	ToolCalls  []ocToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}

type ocToolCall struct {
	Index    *int   `json:"index,omitempty"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func ocContent(parts []Part) any {
	hasImage := false
	for _, p := range parts {
		if p.Type == "image" {
			hasImage = true
		}
	}
	if !hasImage {
		s := ""
		for _, p := range parts {
			s += p.Text
		}
		return s
	}
	out := []map[string]any{}
	for _, p := range parts {
		if p.Type == "image" {
			out = append(out, map[string]any{"type": "image_url", "image_url": map[string]string{"url": "data:" + p.MediaType + ";base64," + base64.StdEncoding.EncodeToString(p.Data)}})
		} else {
			out = append(out, map[string]any{"type": "text", "text": p.Text})
		}
	}
	return out
}

func (openAIChat) body(req Request) map[string]any {
	msgs := []ocMessage{}
	if req.System != "" {
		msgs = append(msgs, ocMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case "tool":
			msgs = append(msgs, ocMessage{Role: "tool", ToolCallID: m.ToolCallID, Content: m.Text()})
		case "assistant":
			om := ocMessage{Role: "assistant"}
			if t := m.Text(); t != "" {
				om.Content = t
			}
			for _, tc := range m.ToolCalls {
				c := ocToolCall{ID: tc.ID, Type: "function"}
				c.Function.Name, c.Function.Arguments = tc.Name, string(tc.Args)
				om.ToolCalls = append(om.ToolCalls, c)
			}
			msgs = append(msgs, om)
		default:
			msgs = append(msgs, ocMessage{Role: "user", Content: ocContent(m.Parts)})
		}
	}
	b := map[string]any{
		"model":          req.Model,
		"messages":       msgs,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}
	if req.MaxTokens > 0 {
		b["max_tokens"] = req.MaxTokens
	}
	if req.Temperature != nil {
		b["temperature"] = *req.Temperature
	}
	if len(req.Tools) > 0 {
		tools := []map[string]any{}
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": t.Name, "description": t.Description, "parameters": json.RawMessage(t.Schema)}})
		}
		b["tools"] = tools
	}
	if req.Output != nil {
		b["response_format"] = map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": req.Output.Name, "schema": json.RawMessage(req.Output.Schema), "strict": true}}
	}
	return b
}

type ocChunk struct {
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Content   string       `json:"content"`
			ToolCalls []ocToolCall `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int64    `json:"prompt_tokens"`
		CompletionTokens    int64    `json:"completion_tokens"`
		Cost                *float64 `json:"cost"`
		PromptTokensDetails *struct {
			CachedTokens int64 `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Code    any    `json:"code"`
	} `json:"error"`
}

func (a openAIChat) Call(ctx context.Context, hc *http.Client, c Conn, req Request, onDelta func(string)) (Response, error) {
	resp, err := post(ctx, hc, c, "/chat/completions", a.body(req), nil)
	if err != nil {
		return Response{}, err
	}
	var out Response
	var text strings.Builder
	calls := map[int]*ToolCall{}
	args := map[int]*strings.Builder{}
	got := false
	err = readSSE(ctx, resp.Body, func(ev sseEvent) error {
		var ch ocChunk
		if err := json.Unmarshal(ev.Data, &ch); err != nil {
			return err
		}
		if ch.Error != nil {
			return &CallError{Message: ch.Error.Message, FirstToken: got}
		}
		if ch.Model != "" {
			out.Model = ch.Model
		}
		for _, choice := range ch.Choices {
			if d := choice.Delta.Content; d != "" {
				got = true
				text.WriteString(d)
				if onDelta != nil {
					onDelta(d)
				}
			}
			for _, tc := range choice.Delta.ToolCalls {
				got = true
				i := 0
				if tc.Index != nil {
					i = *tc.Index
				}
				if calls[i] == nil {
					calls[i] = &ToolCall{}
					args[i] = &strings.Builder{}
				}
				if tc.ID != "" {
					calls[i].ID = tc.ID
				}
				if tc.Function.Name != "" {
					calls[i].Name = tc.Function.Name
				}
				args[i].WriteString(tc.Function.Arguments)
			}
			if choice.FinishReason != nil {
				out.StopReason = *choice.FinishReason
			}
		}
		if ch.Usage != nil {
			out.Usage.In, out.Usage.Out, out.Usage.Cost = ch.Usage.PromptTokens, ch.Usage.CompletionTokens, ch.Usage.Cost
			if ch.Usage.PromptTokensDetails != nil {
				out.Usage.CacheRead = ch.Usage.PromptTokensDetails.CachedTokens
			}
		}
		return nil
	})
	if err != nil {
		return Response{}, streamFailure(err, got)
	}
	out.Text = text.String()
	idx := make([]int, 0, len(calls))
	for i := range calls {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	for _, i := range idx {
		tc := calls[i]
		tc.Args = normalizeArgs(args[i].String())
		out.ToolCalls = append(out.ToolCalls, *tc)
	}
	if req.Output != nil {
		j, err := ParseJSONOutput(out.Text)
		if err != nil {
			return Response{}, err
		}
		out.JSON = j
	}
	return out, nil
}

func normalizeArgs(s string) json.RawMessage {
	s = strings.TrimSpace(s)
	if s == "" || !json.Valid([]byte(s)) {
		return json.RawMessage("{}")
	}
	return json.RawMessage(s)
}

func (openAIChat) ListModels(ctx context.Context, hc *http.Client, c Conn) ([]ModelInfo, error) {
	var resp struct {
		Data []struct {
			ID            string `json:"id"`
			ContextLength int    `json:"context_length"`
			Pricing       *struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if err := getJSON(ctx, hc, c, "/models", &resp, nil); err != nil {
		return nil, err
	}
	out := make([]ModelInfo, 0, len(resp.Data))
	for _, d := range resp.Data {
		mi := ModelInfo{ID: d.ID, ContextWindow: d.ContextLength}
		if d.Pricing != nil {
			mi.PriceIn = perMillion(d.Pricing.Prompt)
			mi.PriceOut = perMillion(d.Pricing.Completion)
		}
		out = append(out, mi)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
