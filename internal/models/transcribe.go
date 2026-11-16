package models

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// ErrNoTranscribe means the space has no model for the transcribe role.
var ErrNoTranscribe = errors.New("no transcribe model is set")

// MaxAudioBytes is the largest spoken note that Fettle sends for transcription.
const MaxAudioBytes = 25 << 20

// transcribeAPI reports whether a provider API can fill the transcribe role: the
// OpenAI-compatible /audio/transcriptions endpoint.
func transcribeAPI(apiType string) bool {
	return apiType == APIOpenAIChat || apiType == APIOpenAIResponses
}

// HasTranscribe reports whether a bot of a space has a transcribe model.
func (s *Service) HasTranscribe(ctx context.Context, spaceID uuid.UUID, botID *uuid.UUID) bool {
	targets, err := s.Resolve(ctx, spaceID, botID, RoleTranscribe)
	return err == nil && len(targets) > 0
}

// Transcribe turns a spoken note into text with the transcribe role (§13.3). It tries
// the targets in order. Fettle keeps no audio: the caller drops it after this call.
func (s *Service) Transcribe(ctx context.Context, spaceID uuid.UUID, botID *uuid.UUID, audio []byte, mediaType string) (string, error) {
	if len(audio) == 0 {
		return "", errors.New("the recording is empty")
	}
	if len(audio) > MaxAudioBytes {
		return "", errors.New("the recording is larger than 25 MiB")
	}
	targets, err := s.Resolve(ctx, spaceID, botID, RoleTranscribe)
	if errors.Is(err, ErrRoleNoTargets) {
		return "", ErrNoTranscribe
	}
	if err != nil {
		return "", err
	}
	var last error
	for _, t := range targets {
		text, err := transcribe(ctx, s.HTTP, t.Conn, t.Target.Model, audio, mediaType)
		if err == nil {
			return text, nil
		}
		last = err
		var ce *CallError
		if errors.As(err, &ce) && !ce.Retryable() {
			return "", err
		}
	}
	return "", last
}

func audioName(mediaType string) string {
	mt := strings.ToLower(strings.TrimSpace(strings.SplitN(mediaType, ";", 2)[0]))
	switch mt {
	case "audio/mp4", "audio/m4a", "audio/x-m4a", "audio/aac":
		return "note.m4a"
	case "audio/ogg":
		return "note.ogg"
	case "audio/wav", "audio/x-wav", "audio/wave":
		return "note.wav"
	case "audio/mpeg", "audio/mp3":
		return "note.mp3"
	}
	return "note.webm"
}

func transcribe(ctx context.Context, hc *http.Client, c Conn, model string, audio []byte, mediaType string) (string, error) {
	if !transcribeAPI(c.APIType) {
		return "", fmt.Errorf("the provider API %s has no transcription endpoint", c.APIType)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("model", model)
	_ = mw.WriteField("response_format", "json")
	h := make(map[string][]string)
	h["Content-Disposition"] = []string{fmt.Sprintf(`form-data; name="file"; filename=%q`, audioName(mediaType))}
	if mediaType == "" {
		mediaType = "application/octet-stream"
	}
	h["Content-Type"] = []string{mediaType}
	part, err := mw.CreatePart(h)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(audio); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, joinURL(c.BaseURL, "/audio/transcriptions"), &body)
	if err != nil {
		return "", err
	}
	r.Header.Set("Content-Type", mw.FormDataContentType())
	setAuth(r, c)
	resp, err := hc.Do(r)
	if err != nil {
		return "", transportError(ctx, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return "", &CallError{Status: resp.StatusCode, Message: errorMessage(b)}
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return "", fmt.Errorf("the transcription reply is not valid: %w", err)
	}
	return strings.TrimSpace(out.Text), nil
}
