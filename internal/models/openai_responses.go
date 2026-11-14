package models

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
)

// openAIResponses speaks the OpenAI Responses API.
type openAIResponses struct{}

func (openAIResponses) body(req Request) map[string]any {
	input := []map[string]any{}
	for _, m := range req.Messages {
		switch m.Role {
		case "tool":
			input = append(input, map[string]any{"type": "function_call_output", "call_id": m.ToolCallID, "output": m.Text()})
		case "assistant":
			if t := m.Text(); t != "" {
				input = append(input, map[string]any{"type": "message", "role": "assistant", "content": []map[string]any{{"type": "output_text", "text": t}}})
			}
			for _, tc := range m.ToolCalls {
				input = append(input, map[string]any{"type": "function_call", "call_id": tc.ID, "name": tc.Name, "arguments": string(tc.Args)})
			}
		default:
			content := []map[string]any{}
			for _, p := range m.Parts {
				if p.Type == "image" {
					content = append(content, map[string]any{"type": "input_image", "image_url": "data:" + p.MediaType + ";base64," + base64.StdEncoding.EncodeToString(p.Data)})
				} else {
					content = append(content, map[string]any{"type": "input_text", "text": p.Text})
				}
			}
			input = append(input, map[string]any{"type": "message", "role": "user", "content": content})
		}
	}
	b := map[string]any{"model": req.Model, "input": input, "stream": true, "store": false}
	if req.System != "" {
		b["instructions"] = req.System
	}
	if req.MaxTokens > 0 {
		b["max_output_tokens"] = req.MaxTokens
	}
	if req.Temperature != nil {
		b["temperature"] = *req.Temperature
	}
	if len(req.Tools) > 0 {
		tools := []map[string]any{}
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{"type": "function", "name": t.Name, "description": t.Description, "parameters": t.Schema})
		}
		b["tools"] = tools
	}
	if req.Output != nil {
		b["text"] = map[string]any{"format": map[string]any{"type": "json_schema", "name": req.Output.Name, "schema": req.Output.Schema, "strict": true}}
	}
	return b
}

type orEvent struct {
	Type        string `json:"type"`
	Delta       string `json:"delta"`
	OutputIndex int    `json:"output_index"`
	Item        *struct {
		Type      string `json:"type"`
		CallID    string `json:"call_id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"item"`
	Response *struct {
		Model             string `json:"model"`
		Status            string `json:"status"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		Usage *struct {
			InputTokens        int64    `json:"input_tokens"`
			OutputTokens       int64    `json:"output_tokens"`
			Cost               *float64 `json:"cost"`
			InputTokensDetails *struct {
				CachedTokens int64 `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"response"`
	Message string `json:"message"`
}

func (a openAIResponses) Call(ctx context.Context, hc *http.Client, c Conn, req Request, onDelta func(string)) (Response, error) {
	resp, err := post(ctx, hc, c, "/responses", a.body(req), nil)
	if err != nil {
		return Response{}, err
	}
	var out Response
	var text strings.Builder
	type call struct {
		tc   ToolCall
		args strings.Builder
	}
	calls := map[int]*call{}
	order := []int{}
	got := false
	err = readSSE(ctx, resp.Body, func(ev sseEvent) error {
		var e orEvent
		if err := json.Unmarshal(ev.Data, &e); err != nil {
			return err
		}
		switch e.Type {
		case "response.output_text.delta":
			got = true
			text.WriteString(e.Delta)
			if onDelta != nil {
				onDelta(e.Delta)
			}
		case "response.output_item.added":
			if e.Item != nil && e.Item.Type == "function_call" {
				got = true
				calls[e.OutputIndex] = &call{tc: ToolCall{ID: e.Item.CallID, Name: e.Item.Name}}
				order = append(order, e.OutputIndex)
			}
		case "response.function_call_arguments.delta":
			if c := calls[e.OutputIndex]; c != nil {
				c.args.WriteString(e.Delta)
			}
		case "response.output_item.done":
			if e.Item != nil && e.Item.Type == "function_call" {
				if c := calls[e.OutputIndex]; c != nil && e.Item.Arguments != "" {
					c.args.Reset()
					c.args.WriteString(e.Item.Arguments)
				}
			}
		case "response.completed", "response.incomplete":
			if r := e.Response; r != nil {
				out.Model = r.Model
				out.StopReason = r.Status
				if r.IncompleteDetails != nil {
					out.StopReason = r.IncompleteDetails.Reason
				}
				if r.Usage != nil {
					out.Usage.In, out.Usage.Out, out.Usage.Cost = r.Usage.InputTokens, r.Usage.OutputTokens, r.Usage.Cost
					if r.Usage.InputTokensDetails != nil {
						out.Usage.CacheRead = r.Usage.InputTokensDetails.CachedTokens
					}
				}
			}
			return errStreamDone
		case "response.failed":
			msg := "the response failed"
			if e.Response != nil && e.Response.Error != nil {
				msg = e.Response.Error.Message
			}
			return &CallError{Message: msg, FirstToken: got}
		case "error":
			return &CallError{Message: e.Message, FirstToken: got}
		}
		return nil
	})
	if err != nil {
		return Response{}, streamFailure(err, got)
	}
	out.Text = text.String()
	for _, i := range order {
		c := calls[i]
		c.tc.Args = normalizeArgs(c.args.String())
		out.ToolCalls = append(out.ToolCalls, c.tc)
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

func (openAIResponses) ListModels(ctx context.Context, hc *http.Client, c Conn) ([]ModelInfo, error) {
	return openAIChat{}.ListModels(ctx, hc, c)
}
