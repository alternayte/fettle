//go:build !drelgen

package models

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strings"
	"time"
)

// probeImage is an 8x8 red PNG.
var probeImage = func() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: 220, A: 255})
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}()

const probeTimeout = 45 * time.Second

// Probe runs the four provider checks (§8.3): plain answer, tool call round trip,
// JSON Schema output and image input. A pass of the image check marks vision.
func Probe(ctx context.Context, hc *http.Client, c Conn, model string) Smoke {
	ad, err := AdapterFor(c.APIType)
	s := Smoke{Model: model}
	if err != nil {
		s.Checks = []CheckResult{{Key: "plain", Detail: err.Error()}}
		return s
	}
	run := func(key string, fn func(ctx context.Context) error) bool {
		cctx, cancel := context.WithTimeout(ctx, probeTimeout)
		defer cancel()
		start := time.Now()
		err := fn(cctx)
		r := CheckResult{Key: key, Pass: err == nil, MS: time.Since(start).Milliseconds()}
		if err != nil {
			r.Detail = err.Error()
		}
		s.Checks = append(s.Checks, r)
		return r.Pass
	}
	run("plain", func(ctx context.Context) error {
		r, err := ad.Call(ctx, hc, c, Request{Model: model, MaxTokens: 64, Messages: []Message{{Role: "user", Parts: []Part{TextPart("Reply with the word ready.")}}}}, nil)
		if err != nil {
			return err
		}
		if strings.TrimSpace(r.Text) == "" {
			return fmt.Errorf("the model returned no text")
		}
		return nil
	})
	s.Tools = run("tools", func(ctx context.Context) error {
		tool := ToolDef{Name: "get_weather", Description: "Get the weather of a city.", Schema: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"],"additionalProperties":false}`)}
		msgs := []Message{{Role: "user", Parts: []Part{TextPart("Use the get_weather tool for Paris, then tell me the weather.")}}}
		r, err := ad.Call(ctx, hc, c, Request{Model: model, MaxTokens: 256, Tools: []ToolDef{tool}, Messages: msgs}, nil)
		if err != nil {
			return err
		}
		if len(r.ToolCalls) == 0 {
			return fmt.Errorf("the model did not call the tool")
		}
		tc := r.ToolCalls[0]
		msgs = append(msgs, Message{Role: "assistant", Parts: []Part{TextPart(r.Text)}, ToolCalls: []ToolCall{tc}},
			Message{Role: "tool", ToolCallID: tc.ID, ToolName: tc.Name, Parts: []Part{TextPart(`{"city":"Paris","weather":"sunny"}`)}})
		r2, err := ad.Call(ctx, hc, c, Request{Model: model, MaxTokens: 256, Tools: []ToolDef{tool}, Messages: msgs}, nil)
		if err != nil {
			return err
		}
		if strings.TrimSpace(r2.Text) == "" {
			return fmt.Errorf("the model gave no answer after the tool result")
		}
		return nil
	})
	run("schema", func(ctx context.Context) error {
		schema := json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"},"count":{"type":"integer"}},"required":["answer","count"],"additionalProperties":false}`)
		r, err := ad.Call(ctx, hc, c, Request{Model: model, MaxTokens: 128, Output: &Schema{Name: "probe", Schema: schema}, Messages: []Message{{Role: "user", Parts: []Part{TextPart("Answer yes and count to 3.")}}}}, nil)
		if err != nil {
			return err
		}
		var v struct {
			Answer *string `json:"answer"`
			Count  *int    `json:"count"`
		}
		if err := json.Unmarshal(r.JSON, &v); err != nil || v.Answer == nil || v.Count == nil {
			return fmt.Errorf("the output does not match the schema")
		}
		return nil
	})
	s.Vision = run("vision", func(ctx context.Context) error {
		r, err := ad.Call(ctx, hc, c, Request{Model: model, MaxTokens: 64, Messages: []Message{{Role: "user", Parts: []Part{{Type: "image", MediaType: "image/png", Data: probeImage}, TextPart("What colour is this image? Answer in one word.")}}}}, nil)
		if err != nil {
			return err
		}
		if strings.TrimSpace(r.Text) == "" {
			return fmt.Errorf("the model returned no text")
		}
		return nil
	})
	return s
}
