package api

import (
	"crypto/rand"
	"log/slog"
	"net/http"
	"time"

	"github.com/hami9/shipyard/internal/logging"
)

const headerRequestID = "X-Request-ID"

// withRequestID tags each request with an ID for log correlation. A
// client-supplied ID is kept only if it is short and uses a safe charset, so
// it cannot inject content into logs.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(headerRequestID)
		if !validRequestID(id) {
			id = rand.Text()
		}
		w.Header().Set(headerRequestID, id)
		next.ServeHTTP(w, r.WithContext(logging.With(r.Context(), logging.RequestID, id)))
	})
}

func validRequestID(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
		default:
			return false
		}
	}
	return true
}

// withAccessLog logs one line per request. It logs the path only: query
// strings and bodies may carry secrets.
func withAccessLog(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		log.LogAttrs(r.Context(), slog.LevelInfo, "http request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.status),
			slog.Int64("bytes", rec.bytes),
			slog.Duration("duration", time.Since(start)),
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.status = code
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	r.wroteHeader = true
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer, which SSE
// streaming needs for flushing.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
