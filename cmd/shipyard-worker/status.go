package main

import (
	"sync"
	"time"

	"github.com/hami9/shipyard/internal/applogs"
	"github.com/hami9/shipyard/internal/reconcile"
)

// healthBoard keeps the reconciler's latest health check for GET /status on
// the worker's socket (P6.7d), and passes it on to the metrics, when they
// are on (ADR-0013).
type healthBoard struct {
	next reconcile.HealthReporter // nil: no metrics
	now  func() time.Time

	mu   sync.Mutex
	last applogs.Status
}

func newHealthBoard(next reconcile.HealthReporter) *healthBoard {
	return &healthBoard{next: next, now: time.Now, last: applogs.Status{Apps: []applogs.AppHealth{}}}
}

func (b *healthBoard) ReportHealth(apps []reconcile.AppHealth) {
	st := applogs.Status{CheckedAt: b.now().UTC(), Apps: make([]applogs.AppHealth, 0, len(apps))}
	for _, a := range apps {
		st.Apps = append(st.Apps, applogs.AppHealth{App: a.App, Running: a.Running, Healthy: a.Healthy})
	}
	b.mu.Lock()
	b.last = st
	b.mu.Unlock()
	if b.next != nil {
		b.next.ReportHealth(apps)
	}
}

func (b *healthBoard) Status() applogs.Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.last // ReportHealth replaces the slice, never changes it
}
