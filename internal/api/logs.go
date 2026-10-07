package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/hami9/shipyard/internal/app"
	"github.com/hami9/shipyard/internal/applogs"
)

// LogSource opens an app's log stream from the worker (applogs.Client);
// the API never reads Docker itself (invariant 1, ADR-0008).
type LogSource interface {
	Logs(ctx context.Context, appID string, tail int, follow bool) (LogStream, error)
}

// LogStream returns lines until one with End set.
type LogStream interface {
	Next() (applogs.Line, error)
	Close() error
}

// LogClient adapts applogs.Client to LogSource.
type LogClient struct{ *applogs.Client }

func (c LogClient) Logs(ctx context.Context, appID string, tail int, follow bool) (LogStream, error) {
	return c.Client.Logs(ctx, appID, tail, follow)
}

type logHandlers struct {
	*appHandlers
	source    LogSource
	keepalive time.Duration
}

type logLineJSON struct {
	TS     time.Time `json:"ts"`
	Stream string    `json:"stream"`
	Line   string    `json:"line"`
}

// logs streams the app's active container output as SSE: the last tail
// lines, then, with follow, new ones until the container stops. Frames have
// no id: container logs cannot be resumed, and a reconnect starts from a
// fresh tail (ADR-0008). The worker redacts known secret values.
func (h *logHandlers) logs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var fe app.FieldErrors
	tail, err := applogs.ParseTail(q.Get("tail"))
	if err != nil {
		fe = append(fe, app.FieldError{Field: "tail", Detail: err.Error()})
	}
	follow := false
	if v := q.Get("follow"); v != "" {
		if follow, err = strconv.ParseBool(v); err != nil {
			fe = append(fe, app.FieldError{Field: "follow", Detail: "must be true or false"})
		}
	}
	if len(fe) > 0 {
		writeError(w, r, h.log, fe)
		return
	}
	a, err := h.lookup(r)
	if err != nil {
		writeError(w, r, h.log, err)
		return
	}
	if h.source == nil {
		writeProblem(w, http.StatusServiceUnavailable, "logs are not available: no worker log socket is configured")
		return
	}
	ctx := r.Context()
	st, err := h.source.Logs(ctx, a.ID, tail, follow)
	switch {
	case errors.Is(err, applogs.ErrNoDeployment):
		writeProblem(w, http.StatusNotFound, "the app has no active deployment")
		return
	case errors.Is(err, applogs.ErrUnavailable):
		h.log.WarnContext(ctx, "worker log service unreachable", slog.Any("err", err))
		writeProblem(w, http.StatusServiceUnavailable, "logs are unavailable: the worker is not running")
		return
	case err != nil:
		h.log.WarnContext(ctx, "log stream failed", slog.Any("err", err))
		writeProblem(w, http.StatusBadGateway, "logs are unavailable")
		return
	}
	defer st.Close()

	// Next blocks, so a reader goroutine feeds the loop, which also sends
	// keepalives and notices the client or the server leaving.
	type result struct {
		l   applogs.Line
		err error
	}
	lines, done := make(chan result), make(chan struct{})
	defer close(done)
	go func() {
		for {
			l, err := st.Next()
			select {
			case lines <- result{l, err}:
			case <-done:
				return
			}
			if err != nil || l.End != "" {
				return
			}
		}
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	s := &sseWriter{w: w, rc: http.NewResponseController(w)}
	s.printf(": logs\n\n") // open the stream at once
	if s.flush() != nil {
		return
	}
	keepalive := time.NewTicker(h.keepalive)
	defer keepalive.Stop()
	for {
		select {
		case res := <-lines:
			if res.err != nil || res.l.End != "" {
				reason := res.l.End
				if res.err != nil {
					h.log.WarnContext(ctx, "log stream cut short", slog.Any("err", res.err))
					reason = "the worker stopped streaming"
				}
				data, _ := json.Marshal(map[string]string{"reason": reason})
				s.printf("event: end\ndata: %s\n\n", data)
				s.flush()
				return
			}
			data, _ := json.Marshal(logLineJSON{res.l.TS, res.l.Stream, res.l.Text})
			s.printf("data: %s\n\n", data)
		case <-keepalive.C:
			if time.Since(s.last) >= h.keepalive {
				s.printf(": keepalive\n\n")
			}
		case <-ctx.Done():
			return
		case <-stopping(ctx):
			return
		}
		if s.flush() != nil {
			return
		}
	}
}
