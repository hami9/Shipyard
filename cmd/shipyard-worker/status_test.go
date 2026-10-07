package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/hami9/shipyard/internal/reconcile"
)

type reports [][]reconcile.AppHealth

func (r *reports) ReportHealth(apps []reconcile.AppHealth) { *r = append(*r, apps) }

// The board answers the latest check, stamped, and still feeds the metrics.
func TestHealthBoard(t *testing.T) {
	var next reports
	b := newHealthBoard(&next)
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	b.now = func() time.Time { return at }
	if st := b.Status(); !st.CheckedAt.IsZero() || st.Apps == nil || len(st.Apps) != 0 {
		t.Fatalf("before a check: %+v", st)
	}
	b.ReportHealth([]reconcile.AppHealth{{App: "web", Running: true, Healthy: true}, {App: "docs", Running: true}})
	b.ReportHealth([]reconcile.AppHealth{{App: "web", Running: true}})
	st := b.Status()
	if !st.CheckedAt.Equal(at) || fmt.Sprint(st.Apps) != "[{web true false}]" {
		t.Fatalf("status %+v", st)
	}
	if len(next) != 2 {
		t.Fatalf("metrics got %d reports", len(next))
	}
	// Without metrics, nothing to pass on.
	newHealthBoard(nil).ReportHealth([]reconcile.AppHealth{{App: "web"}})
}
