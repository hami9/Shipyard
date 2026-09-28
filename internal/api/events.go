package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/hami9/shipyard/internal/app"
	"github.com/hami9/shipyard/internal/store"
)

// Stream timing (ARCHITECTURE §7, [WHATWG-SSE]).
const (
	defaultStreamPoll      = 500 * time.Millisecond
	defaultStreamKeepalive = 15 * time.Second // a comment line keeps proxies from closing the stream
	streamRetry            = 2 * time.Second  // the client's reconnect delay
	streamBatch            = 500
	// A worker can append an event just after the operation ends (activate
	// logs the drain plan after its commit). The stream ends only after this
	// many empty polls of a finished operation.
	streamEndPolls = 2
)

type eventJSON struct {
	Seq     int64     `json:"seq"`
	TS      time.Time `json:"ts"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

// events streams an operation's events as SSE [WHATWG-SSE]. Each event's id
// is its seq, so a client resumes with Last-Event-ID and misses nothing: seq
// is gapless per operation (store.AppendOperationEvent). When the operation
// has finished and every event was sent, a final "end" event carries the
// operation, and the stream closes.
//
// The API polls PostgreSQL rather than listening for notifications: one
// indexed query per stream every half second, which the MVP's handful of
// operators does not notice.
func (h *opHandlers) events(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !uuidRE.MatchString(id) {
		writeError(w, r, h.log, store.ErrNotFound)
		return
	}
	after, err := lastEventID(r)
	if err != nil {
		writeError(w, r, h.log, app.FieldErrors{{Field: "Last-Event-ID", Detail: err.Error()}})
		return
	}
	ctx := r.Context()
	op, err := h.ops.OperationByID(ctx, id)
	if err != nil {
		writeError(w, r, h.log, err)
		return
	}

	rc := http.NewResponseController(w)
	hdr := w.Header()
	hdr.Set("Content-Type", "text/event-stream")
	hdr.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	s := &sseWriter{w: w, rc: rc}
	s.printf("retry: %d\n\n", streamRetry.Milliseconds())

	quiet := 0
	for {
		// The status is read before the events: an event committed before a
		// finished status was read is always fetched below.
		events, err := h.ops.OperationEvents(ctx, id, after, streamBatch)
		if err != nil {
			if ctx.Err() == nil {
				h.log.WarnContext(ctx, "event stream stopped", slog.String("operation_id", id), slog.Any("err", err))
			}
			return // the client reconnects and resumes
		}
		for _, e := range events {
			data, _ := json.Marshal(eventJSON{e.Seq, e.TS, e.Level, e.Message})
			s.printf("id: %d\ndata: %s\n\n", e.Seq, data)
			after = e.Seq
		}
		if len(events) == streamBatch {
			if s.flush() != nil {
				return
			}
			continue // more are waiting
		}
		if len(events) == 0 && finished(op.Status) {
			if quiet++; quiet > streamEndPolls {
				data, _ := json.Marshal(toOperationJSON(op))
				s.printf("event: end\ndata: %s\n\n", data)
				s.flush()
				return
			}
		} else {
			quiet = 0
		}
		if !s.wrote && time.Since(s.last) >= h.keepalive {
			s.printf(": keepalive\n\n")
		}
		if s.flush() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-stopping(ctx):
			return // shutdown: the client reconnects to the next process
		case <-time.After(h.poll):
		}
		if op, err = h.ops.OperationByID(ctx, id); err != nil {
			if ctx.Err() == nil {
				h.log.WarnContext(ctx, "event stream stopped", slog.String("operation_id", id), slog.Any("err", err))
			}
			return
		}
	}
}

// lastEventID is the seq a reconnecting client saw last; 0 for a new stream.
func lastEventID(r *http.Request) (int64, error) {
	v := r.Header.Get("Last-Event-ID")
	if v == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%q is not an event id from this stream", v)
	}
	return n, nil
}

func finished(status string) bool {
	return status == store.OpSucceeded || status == store.OpFailed || status == store.OpCancelled
}

// sseWriter batches writes until flush and remembers the first error, after
// which the client is gone and the handler returns.
type sseWriter struct {
	w     http.ResponseWriter
	rc    *http.ResponseController
	err   error
	wrote bool // since the last flush
	last  time.Time
}

func (s *sseWriter) printf(format string, args ...any) {
	if s.err == nil {
		_, s.err = fmt.Fprintf(s.w, format, args...)
		s.wrote = true
	}
}

func (s *sseWriter) flush() error {
	if s.err == nil && s.wrote {
		s.err = s.rc.Flush()
		s.wrote, s.last = false, time.Now()
	}
	return s.err
}

type stoppingKey struct{}

// stopping is closed when the server starts shutting down, so long-lived
// streams end instead of holding up Shutdown until its timeout (Serve).
func stopping(ctx context.Context) <-chan struct{} {
	ch, _ := ctx.Value(stoppingKey{}).(chan struct{})
	return ch // nil (never ready) outside Serve, e.g. in tests
}
