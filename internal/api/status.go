package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/hami9/shipyard/internal/applogs"
)

// WorkerStatus asks the worker, over its socket, what its reconciler last
// saw (applogs.Client). The API still never reads Docker (invariant 1).
type WorkerStatus interface {
	Status(ctx context.Context) (applogs.Status, error)
}

// statusTimeout bounds the question: a worker that does not answer within
// it counts as down, and the page asking still gets an answer.
const statusTimeout = 2 * time.Second

type statusJSON struct {
	// Worker is "up" when it answered, "down" when it did not.
	Worker string `json:"worker"`
	// CheckedAt is when the worker last checked the apps; null before its
	// first pass, or when it is down.
	CheckedAt *time.Time          `json:"checked_at"`
	Apps      []applogs.AppHealth `json:"apps"`
	// PublicIPs are the addresses domains must point at (SHIPYARD_PUBLIC_IPS).
	PublicIPs []string `json:"public_ips"`
}

type statusHandlers struct {
	log    *slog.Logger
	worker WorkerStatus
	ips    []string
}

// status reports what a dashboard needs and the database does not hold:
// whether the worker is there, how the running apps last answered their
// health checks, and where DNS records should point (P6.7d).
func (h *statusHandlers) status(w http.ResponseWriter, r *http.Request) {
	out := statusJSON{Worker: "down", Apps: []applogs.AppHealth{}, PublicIPs: h.ips}
	if h.worker != nil {
		ctx, cancel := context.WithTimeout(r.Context(), statusTimeout)
		defer cancel()
		st, err := h.worker.Status(ctx)
		if err == nil {
			out.Worker = "up"
			if st.Apps != nil {
				out.Apps = st.Apps
			}
			if !st.CheckedAt.IsZero() {
				out.CheckedAt = &st.CheckedAt
			}
		} else {
			h.log.DebugContext(r.Context(), "worker status", slog.Any("err", err))
		}
	}
	writeJSON(w, http.StatusOK, "application/json", out)
}
