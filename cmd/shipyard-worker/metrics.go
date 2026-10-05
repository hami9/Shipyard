package main

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/hami9/shipyard/internal/metrics"
	"github.com/hami9/shipyard/internal/reconcile"
	"github.com/hami9/shipyard/internal/store"
)

// metricsStore is what the worker's metrics read (internal/store).
type metricsStore interface {
	QueueStats(ctx context.Context) (store.QueueStats, error)
	FinishedOperations(ctx context.Context, after time.Time, limit int) ([]store.FinishedOperation, error)
	LastFinished(ctx context.Context) (time.Time, error)
}

const (
	// finishOverlap re-reads operations that finished shortly before the
	// newest one counted: a transaction that commits late has an earlier
	// finished_at (now() is its start time).
	finishOverlap = time.Minute
	finishPage    = 1000
	scrapeTimeout = 5 * time.Second
)

// durationBounds span a cached build (seconds) to a slow one (an hour).
var durationBounds = []float64{1, 5, 15, 30, 60, 120, 300, 600, 1200, 1800, 3600}

// workerMetrics exposes the queue and the outcome and duration of finished
// operations (ADR-0013). Both are read from PostgreSQL at scrape time, so
// they include every path that finishes an operation: a deploy, a delete, a
// cancel by a newer request, and the reconciler failing an expired lease.
type workerMetrics struct {
	store metricsStore
	log   *slog.Logger
	now   func() time.Time

	mu    sync.Mutex
	floor time.Time            // operations finished by then predate this process
	since time.Time            // the newest finish counted
	seen  map[string]time.Time // counted operations that finished within the overlap

	ops      *metrics.Counter
	duration *metrics.Histogram
	queue    *metrics.Gauge
	oldest   *metrics.Gauge
	dbUp     *metrics.Gauge

	// From the reconciler (ReportHealth), not the database.
	appUp, appHealthy, healthAt *metrics.Gauge
}

func newWorkerMetrics(ctx context.Context, s metricsStore, reg *metrics.Registry, log *slog.Logger) (*workerMetrics, error) {
	floor, err := s.LastFinished(ctx)
	if err != nil {
		return nil, err
	}
	m := &workerMetrics{store: s, log: log, now: time.Now, floor: floor, since: floor, seen: map[string]time.Time{}}
	metrics.BuildInfo(reg)
	m.ops = reg.NewCounter("shipyard_operations_total",
		"Operations finished since the worker started, by kind (deploy, rollback, delete) and result (succeeded, failed, cancelled).",
		"kind", "result")
	m.duration = reg.NewHistogram("shipyard_operation_duration_seconds",
		"Time from an operation's request to its finish, queueing included.", durationBounds, "kind", "result")
	m.queue = reg.NewGauge("shipyard_queue_operations", "Operations queued or running now.", "status")
	m.oldest = reg.NewGauge("shipyard_queue_oldest_wait_seconds",
		"How long the longest-waiting due operation has waited; 0 when none waits.")
	m.dbUp = reg.NewGauge("shipyard_database_up", "Whether the last scrape could read PostgreSQL.")
	m.appUp = reg.NewGauge("shipyard_app_up",
		"Whether the active deployment's container was running at the last reconcile pass.", "app")
	m.appHealthy = reg.NewGauge("shipyard_app_healthy",
		"Whether the active deployment answered one GET of its health path (2xx or 3xx) at the last reconcile pass.", "app")
	m.healthAt = reg.NewGauge("shipyard_app_health_checked_timestamp_seconds",
		"When the reconciler last checked app health (Unix time); 0 before the first check.")
	m.healthAt.Set(0)
	reg.OnScrape(m.collect)
	return m, nil
}

// collect refreshes everything from the database. Concurrent scrapes take
// turns, so an operation is never counted twice.
func (m *workerMetrics) collect(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, scrapeTimeout)
	defer cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	err := m.countFinished(ctx)
	if err == nil {
		err = m.readQueue(ctx)
	}
	if err != nil {
		// No stale queue numbers: the series go away until the next read.
		m.queue.Reset()
		m.oldest.Reset()
		m.dbUp.Set(0)
		if ctx.Err() == nil || errors.Is(context.Cause(ctx), context.DeadlineExceeded) {
			m.log.Warn("metrics: database read failed", slog.Any("err", err))
		}
		return
	}
	m.dbUp.Set(1)
}

func (m *workerMetrics) readQueue(ctx context.Context) error {
	st, err := m.store.QueueStats(ctx)
	if err != nil {
		return err
	}
	m.queue.Set(float64(st.Queued), store.OpQueued)
	m.queue.Set(float64(st.Running), store.OpRunning)
	m.oldest.Set(st.OldestWait.Seconds())
	return nil
}

// countFinished adds the operations finished since the last scrape. Pages
// are bounded per scrape; the rest are counted by the next one.
func (m *workerMetrics) countFinished(ctx context.Context) error {
	after := m.since.Add(-finishOverlap)
	if after.Before(m.floor) {
		after = m.floor
	}
	for range 10 {
		ops, err := m.store.FinishedOperations(ctx, after, finishPage)
		if err != nil {
			return err
		}
		for _, o := range ops {
			after = o.FinishedAt
			if _, ok := m.seen[o.ID]; ok {
				continue
			}
			m.seen[o.ID] = o.FinishedAt
			m.ops.Inc(o.Kind, o.Status)
			m.duration.Observe(max(o.FinishedAt.Sub(o.CreatedAt).Seconds(), 0), o.Kind, o.Status)
			if o.FinishedAt.After(m.since) {
				m.since = o.FinishedAt
			}
		}
		if len(ops) < finishPage {
			break
		}
	}
	for id, t := range m.seen {
		if !t.After(m.since.Add(-finishOverlap)) {
			delete(m.seen, id) // the next query starts after it
		}
	}
	return nil
}

// ReportHealth takes the reconciler's view of the active apps (P5.5b).
// Apps that are no longer active drop out.
func (m *workerMetrics) ReportHealth(apps []reconcile.AppHealth) {
	bit := func(b bool) float64 {
		if b {
			return 1
		}
		return 0
	}
	m.appUp.Replace(func(set func(float64, ...string)) {
		for _, a := range apps {
			set(bit(a.Running), a.App)
		}
	})
	m.appHealthy.Replace(func(set func(float64, ...string)) {
		for _, a := range apps {
			set(bit(a.Healthy), a.App)
		}
	})
	m.healthAt.Set(float64(m.now().UnixMilli()) / 1000)
}
