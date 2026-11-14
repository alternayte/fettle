package models

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
)

// anthropic speaks the Anthropic Messages API.
type anthropic struct{}

const anthropicVersion = "2023-06-01"

// outputTool is the forced tool that carries structured output.
const outputTool = "structured_output"

func (anthropic) body(req Request) map[string]any {
	msgs := []map[string]any{}
	add := func(role string, blocks []map[string]any) {
		// Anthropic needs alternating roles; merge consecutive messages of one role.
		if n := len(msgs); n > 0 && msgs[n-1]["role"] == role {
			msgs[n-1]["content"] = append(msgs[n-1]["content"].([]map[string]any), blocks...)
			return
		}
		msgs = append(msgs, map[string]any{"role": role, "content": blocks})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case "tool":
			add("user", []map[string]any{{"type": "tool_result", "tool_use_id": m.ToolCallID, "content": m.Text()}})
		case "assistant":
			blocks := []map[string]any{}
			if t := m.Text(); t != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": t})
			}
			for _, tc := range m.ToolCalls {
				blocks = append(blocks, map[string]any{"type": "tool_use", "id": tc.ID, "name": tc.Name, "input": json.RawMessage(tc.Args)})
			}
			if len(blocks) > 0 {
				add("assistant", blocks)
			}
		default:
			blocks := []map[string]any{}
			for _, p := range m.Parts {
				if p.Type == "image" {
					blocks = append(blocks, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": p.MediaType, "data": base64.StdEncoding.EncodeToString(p.Data)}})
				} else if p.Text != "" {
					blocks = append(blocks, map[string]any{"type": "text", "text": p.Text})
				}
			}
			if len(blocks) == 0 {
				blocks = append(blocks, map[string]any{"type": "text", "text": " "})
			}
			add("user", blocks)
		}
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 8192
	}
	b := map[string]any{"model": req.Model, "messages": msgs, "stream": true, "max_tokens": maxTokens}
	if req.System != "" {
		b["system"] = req.System
	}
	if req.Temperature != nil {
		b["temperature"] = *req.Temperature
	}
	tools := []map[string]any{}
	for _, t := range req.Tools {
		tools = append(tools, map[string]any{"name": t.Name, "description": t.Description, "input_schema": json.RawMessage(t.Schema)})
	}
	if req.Output != nil {
		tools = append(tools, map[string]any{"name": outputTool, "description": "Return the answer in this structure.", "input_schema": json.RawMessage(req.Output.Schema)})
		b["tool_choice"] = map[string]any{"type": "tool", "name": outputTool}
	}
	if len(tools) > 0 {
		b["tools"] = tools
	}
	return b
}

type anEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message *struct {
		Model string  `json:"model"`
		Usage anUsage `json:"usage"`
	} `json:"message"`
	ContentBlock *struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
	Delta *struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage *anUsage `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type anUsage struct {
	InputTokens          int64 `json:"input_tokens"`
	OutputTokens         int64 `json:"output_tokens"`
	CacheReadInputTokens int64 `json:"cache_read_input_tokens"`
}

func (a anthropic) Call(ctx context.Context, hc *http.Client, c Conn, req Request, onDelta func(string)) (Response, error) {
	if c.AuthStyle == "" {
		c.AuthStyle = "x_api_key"
	}
	resp, err := post(ctx, hc, c, "/messages", a.body(req), map[string]string{"anthropic-version": anthropicVersion})
	if err != nil {
		return Response{}, err
	}
	var out Response
	var text strings.Builder
	type block struct {
		tc   ToolCall
		args strings.Builder
	}
	blocks := map[int]*block{}
	order := []int{}
	got := false
	err = readSSE(ctx, resp.Body, func(ev sseEvent) error {
		var e anEvent
		if err := json.Unmarshal(ev.Data, &e); err != nil {
			return err
		}
		switch e.Type {
		case "message_start":
			if e.Message != nil {
				out.Model = e.Message.Model
				out.Usage.In = e.Message.Usage.InputTokens
				out.Usage.CacheRead = e.Message.Usage.CacheReadInputTokens
				out.Usage.Out = e.Message.Usage.OutputTokens
			}
		case "content_block_start":
			if e.ContentBlock != nil && e.ContentBlock.Type == "tool_use" {
				got = true
				blocks[e.Index] = &block{tc: ToolCall{ID: e.ContentBlock.ID, Name: e.ContentBlock.Name}}
				order = append(order, e.Index)
			}
		case "content_block_delta":
			if e.Delta == nil {
				return nil
			}
			switch e.Delta.Type {
			case "text_delta":
				got = true
				text.WriteString(e.Delta.Text)
				if onDelta != nil && req.Output == nil {
					onDelta(e.Delta.Text)
				}
			case "input_json_delta":
				if b := blocks[e.Index]; b != nil {
					b.args.WriteString(e.Delta.PartialJSON)
				}
			}
		case "message_delta":
			if e.Delta != nil && e.Delta.StopReason != "" {
				out.StopReason = e.Delta.StopReason
			}
			if e.Usage != nil {
				out.Usage.Out = e.Usage.OutputTokens
				if e.Usage.InputTokens > 0 {
					out.Usage.In = e.Usage.InputTokens
				}
			}
		case "message_stop":
			return errStreamDone
		case "error":
			msg := "stream error"
			status := 0
			if e.Error != nil {
				msg = e.Error.Message
				if e.Error.Type == "overloaded_error" {
					status = 529
				}
			}
			return &CallError{Status: status, Message: msg, FirstToken: got}
		}
		return nil
	})
	if err != nil {
		return Response{}, streamFailure(err, got)
	}
	out.Text = text.String()
	for _, i := range order {
		b := blocks[i]
		b.tc.Args = normalizeArgs(b.args.String())
		if req.Output != nil && b.tc.Name == outputTool {
			out.JSON = b.tc.Args
			continue
		}
		out.ToolCalls = append(out.ToolCalls, b.tc)
	}
	if req.Output != nil && out.JSON == nil {
		j, err := ParseJSONOutput(out.Text)
		if err != nil {
			return Response{}, err
		}
		out.JSON = j
	}
	return out, nil
}

func (anthropic) ListModels(ctx context.Context, hc *http.Client, c Conn) ([]ModelInfo, error) {
	if c.AuthStyle == "" {
		c.AuthStyle = "x_api_key"
	}
	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := getJSON(ctx, hc, c, "/models", &resp, map[string]string{"anthropic-version": anthropicVersion}); err != nil {
		return nil, err
	}
	out := make([]ModelInfo, 0, len(resp.Data))
	for _, d := range resp.Data {
		out = append(out, ModelInfo{ID: d.ID})
	}
	return out, nil
}
