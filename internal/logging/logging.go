// Package logging builds Shipyard's structured logger and carries correlation
// IDs through context.Context so every log line can be joined across the API,
// the worker, and PostgreSQL rows.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// Field is a correlation attribute stored in a context and added to every
// record logged with that context.
type Field string

const (
	RequestID    Field = "request_id"
	OperationID  Field = "operation_id"
	App          Field = "app"
	DeploymentID Field = "deployment_id"
)

var fields = [...]Field{RequestID, OperationID, App, DeploymentID}

// With returns a copy of ctx that carries value for f.
func With(ctx context.Context, f Field, value string) context.Context {
	return context.WithValue(ctx, f, value)
}

// Get returns the value of f in ctx, or "" when it is absent.
func Get(ctx context.Context, f Field) string {
	v, _ := ctx.Value(f).(string)
	return v
}

// Format selects the log encoding.
type Format string

const (
	FormatJSON Format = "json"
	FormatText Format = "text"
)

// ParseFormat validates a format name.
func ParseFormat(s string) (Format, error) {
	switch f := Format(strings.ToLower(s)); f {
	case FormatJSON, FormatText:
		return f, nil
	default:
		return "", fmt.Errorf("unknown log format %q (want json or text)", s)
	}
}

// New returns a logger that writes to w and adds correlation fields found in
// the context of each call (use the *Context logging methods).
func New(w io.Writer, level slog.Level, format Format) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler
	if format == FormatText {
		h = slog.NewTextHandler(w, opts)
	} else {
		h = slog.NewJSONHandler(w, opts)
	}
	return slog.New(contextHandler{h})
}

type contextHandler struct{ slog.Handler }

func (h contextHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, f := range fields {
		if v := Get(ctx, f); v != "" {
			r.AddAttrs(slog.String(string(f), v))
		}
	}
	return h.Handler.Handle(ctx, r)
}

func (h contextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return contextHandler{h.Handler.WithAttrs(attrs)}
}

func (h contextHandler) WithGroup(name string) slog.Handler {
	return contextHandler{h.Handler.WithGroup(name)}
}
