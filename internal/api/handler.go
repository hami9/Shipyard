// Package api implements shipyard-api's HTTP surface. The API validates
// requests and records intent in PostgreSQL; it never touches Docker, Caddy,
// or repository code (CLAUDE.md invariant 1).
package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"
)

const readinessTimeout = 2 * time.Second

// Pinger reports whether the database is reachable.
type Pinger interface {
	Ping(ctx context.Context) error
}

// NewHandler returns the root handler with middleware applied.
func NewHandler(log *slog.Logger, db Pinger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz)
	mux.HandleFunc("GET /readyz", handleReadyz(log, db))
	return withRequestID(withAccessLog(log, mux))
}

// handleHealthz reports liveness: the process is up and serving HTTP.
func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, "application/json", map[string]string{"status": "ok"})
}

// handleReadyz reports readiness: dependencies needed to serve requests work.
func handleReadyz(log *slog.Logger, db Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
		defer cancel()
		if err := db.Ping(ctx); err != nil {
			log.WarnContext(r.Context(), "readiness check failed", slog.String("dependency", "postgres"), slog.Any("err", err))
			writeProblem(w, http.StatusServiceUnavailable, "database unavailable")
			return
		}
		writeJSON(w, http.StatusOK, "application/json", map[string]string{"status": "ready"})
	}
}
