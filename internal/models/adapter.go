package models

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Adapter speaks one model API type.
type Adapter interface {
	// Call runs one streamed call. onDelta gets text as it arrives and may be nil.
	Call(ctx context.Context, hc *http.Client, c Conn, req Request, onDelta func(string)) (Response, error)
	// ListModels returns the model IDs from /models when the API has it.
	ListModels(ctx context.Context, hc *http.Client, c Conn) ([]ModelInfo, error)
}

// AdapterFor returns the adapter of an API type.
func AdapterFor(apiType string) (Adapter, error) {
	switch apiType {
	case APIOpenAIChat:
		return openAIChat{}, nil
	case APIOpenAIResponses:
		return openAIResponses{}, nil
	case APIAnthropic:
		return anthropic{}, nil
	}
	return nil, fmt.Errorf("unknown API type %q", apiType)
}

func joinURL(base, path string) string {
	return strings.TrimRight(base, "/") + path
}

func setAuth(r *http.Request, c Conn) {
	switch c.AuthStyle {
	case "bearer", "":
		if c.Key != "" {
			r.Header.Set("Authorization", "Bearer "+c.Key)
		}
	case "x_api_key":
		r.Header.Set("x-api-key", c.Key)
	case "header":
		if c.AuthHeader != "" {
			r.Header.Set(c.AuthHeader, c.Key)
		}
	}
	for k, v := range c.Headers {
		r.Header.Set(k, v)
	}
}

// post sends a JSON body and returns the response, or a CallError for a non-2xx status.
func post(ctx context.Context, hc *http.Client, c Conn, path string, body any, extra map[string]string) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, joinURL(c.BaseURL, path), bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "text/event-stream")
	for k, v := range extra {
		r.Header.Set(k, v)
	}
	setAuth(r, c)
	resp, err := hc.Do(r)
	if err != nil {
		return nil, transportError(ctx, err)
	}
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		return nil, &CallError{Status: resp.StatusCode, Message: errorMessage(msg)}
	}
	return resp, nil
}

func getJSON(ctx context.Context, hc *http.Client, c Conn, path string, out any, extra map[string]string) error {
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, joinURL(c.BaseURL, path), nil)
	if err != nil {
		return err
	}
	for k, v := range extra {
		r.Header.Set(k, v)
	}
	setAuth(r, c)
	resp, err := hc.Do(r)
	if err != nil {
		return transportError(ctx, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &CallError{Status: resp.StatusCode, Message: errorMessage(msg)}
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func transportError(ctx context.Context, err error) error {
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return &CallError{Timeout: true, Message: err.Error()}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return &CallError{Message: err.Error()}
}

func errorMessage(b []byte) string {
	var e struct {
		Error any `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil && e.Error != nil {
		switch v := e.Error.(type) {
		case string:
			return v
		case map[string]any:
			if m, ok := v["message"].(string); ok {
				return m
			}
		}
	}
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

// sseEvent is one server-sent event.
type sseEvent struct {
	Event string
	Data  []byte
}

// readSSE calls fn for each event. It stops at [DONE], EOF or an error from fn.
// A stall of FirstTokenTimeout before the first event is a timeout.
func readSSE(ctx context.Context, body io.ReadCloser, fn func(sseEvent) error) error {
	defer body.Close()
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var ev sseEvent
	var data bytes.Buffer
	lines := make(chan string)
	errc := make(chan error, 1)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			case <-stop:
				return
			}
		}
		errc <- sc.Err()
		close(lines)
	}()
	first := true
	timer := time.NewTimer(FirstTokenTimeout)
	defer timer.Stop()
	for {
		var line string
		var ok bool
		if first {
			select {
			case line, ok = <-lines:
			case <-timer.C:
				return &CallError{Timeout: true, Message: "no response before the first token timeout"}
			case <-ctx.Done():
				return ctx.Err()
			}
		} else {
			select {
			case line, ok = <-lines:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if !ok {
			if err := <-errc; err != nil {
				return err
			}
			if data.Len() > 0 {
				ev.Data = data.Bytes()
				return fn(ev)
			}
			return io.ErrUnexpectedEOF
		}
		first = false
		switch {
		case line == "":
			if data.Len() == 0 {
				continue
			}
			if bytes.Equal(bytes.TrimSpace(data.Bytes()), []byte("[DONE]")) {
				return nil
			}
			ev.Data = append([]byte(nil), data.Bytes()...)
			if err := fn(ev); err != nil {
				if errors.Is(err, errStreamDone) {
					return nil
				}
				return err
			}
			ev, data = sseEvent{}, bytes.Buffer{}
		case strings.HasPrefix(line, "event:"):
			ev.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
}

var errStreamDone = errors.New("stream done")

// streamFailure wraps an error that happened after the first token, which is never retried.
func streamFailure(err error, gotToken bool) error {
	if err == nil {
		return nil
	}
	var ce *CallError
	if errors.As(err, &ce) {
		ce.FirstToken = ce.FirstToken || gotToken
		return ce
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	return &CallError{Message: err.Error(), FirstToken: gotToken}
}

// ParseJSONOutput extracts the JSON object from a structured output text.
func ParseJSONOutput(s string) (json.RawMessage, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	if !json.Valid([]byte(s)) {
		return nil, fmt.Errorf("model output is not valid JSON")
	}
	return json.RawMessage(s), nil
}
