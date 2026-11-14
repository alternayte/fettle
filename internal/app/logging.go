package app

import (
	"context"
	"io"
	"log/slog"
	"strings"

	"github.com/alternayte/fettle/internal/vault"
)

// redactHandler replaces known secret values in every log record (§17 Logs, SI-01).
type redactHandler struct {
	next slog.Handler
	r    *vault.Redactor
}

func (h *redactHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.next.Enabled(ctx, l)
}

func (h *redactHandler) Handle(ctx context.Context, rec slog.Record) error {
	out := slog.NewRecord(rec.Time, rec.Level, h.r.Global(rec.Message), rec.PC)
	rec.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(h.attr(a))
		return true
	})
	return h.next.Handle(ctx, out)
}

func (h *redactHandler) attr(a slog.Attr) slog.Attr {
	switch a.Value.Kind() {
	case slog.KindString:
		return slog.String(a.Key, h.r.Global(a.Value.String()))
	case slog.KindGroup:
		as := a.Value.Group()
		out := make([]any, len(as))
		for i, x := range as {
			out[i] = h.attr(x)
		}
		return slog.Group(a.Key, out...)
	case slog.KindAny:
		if err, ok := a.Value.Any().(error); ok {
			return slog.String(a.Key, h.r.Global(err.Error()))
		}
	}
	return a
}

func (h *redactHandler) WithAttrs(as []slog.Attr) slog.Handler {
	out := make([]slog.Attr, len(as))
	for i, a := range as {
		out[i] = h.attr(a)
	}
	return &redactHandler{next: h.next.WithAttrs(out), r: h.r}
}

func (h *redactHandler) WithGroup(n string) slog.Handler {
	return &redactHandler{next: h.next.WithGroup(n), r: h.r}
}

// NewLogger returns a JSON logger that redacts secrets.
func NewLogger(level string, w io.Writer, r *vault.Redactor) *slog.Logger {
	var l slog.Level
	switch strings.ToLower(level) {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	return slog.New(&redactHandler{next: slog.NewJSONHandler(w, &slog.HandlerOptions{Level: l}), r: r})
}
