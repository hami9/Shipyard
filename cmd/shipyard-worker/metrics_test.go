package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hami9/shipyard/internal/metrics"
	"github.com/hami9/shipyard/internal/store"
)

type fakeMetricsStore struct {
	ops   []store.FinishedOperation // any order
	queue store.QueueStats
	err   error
}

func (f *fakeMetricsStore) QueueStats(context.Context) (store.QueueStats, error) {
	return f.queue, f.err
}

func (f *fakeMetricsStore) FinishedOperations(_ context.Context, after time.Time, limit int) ([]store.FinishedOperation, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []store.FinishedOperation
	for _, o := range f.ops {
		if o.FinishedAt.After(after) {
			out = append(out, o)
		}
	}
	slices.SortFunc(out, func(a, b store.FinishedOperation) int { return a.FinishedAt.Compare(b.FinishedAt) })
	return out[:min(len(out), limit)], nil
}

func (f *fakeMetricsStore) LastFinished(context.Context) (time.Time, error) {
	var last time.Time
	for _, o := range f.ops {
		if o.FinishedAt.After(last) {
			last = o.FinishedAt
		}
	}
	return last, f.err
}

func scrape(t *testing.T, reg *metrics.Registry) string {
	t.Helper()
	var b strings.Builder
	if err := reg.Write(context.Background(), bufio.NewWriter(&b)); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestWorkerMetrics(t *testing.T) {
	t0 := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	op := func(id, kind, status string, took, finish time.Duration) store.FinishedOperation {
		return store.FinishedOperation{ID: id, Kind: kind, Status: status,
			CreatedAt: t0.Add(finish - took), FinishedAt: t0.Add(finish)}
	}
	f := &fakeMetricsStore{ops: []store.FinishedOperation{op("before", "deploy", "failed", time.Second, 0)}}
	reg := &metrics.Registry{}
	if _, err := newWorkerMetrics(context.Background(), f, reg, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}

	// Nothing finished since the start: an operation from before is not counted.
	if got := scrape(t, reg); strings.Contains(got, "shipyard_operations_total{") || !strings.Contains(got, "shipyard_database_up 1") {
		t.Fatalf("first scrape:\n%s", got)
	}

	f.ops = append(f.ops, op("a", "deploy", "succeeded", 40*time.Second, 10*time.Second),
		op("b", "deploy", "failed", 3*time.Second, 20*time.Second))
	f.queue = store.QueueStats{Queued: 2, Running: 1, OldestWait: 7 * time.Second}
	scrape(t, reg)
	// Committed late: finished before b, after the scrape that counted b.
	f.ops = append(f.ops, op("late", "rollback", "succeeded", time.Second, 15*time.Second),
		op("c", "delete", "cancelled", 0, 30*time.Second))
	got := scrape(t, reg)
	for _, want := range []string{
		`shipyard_operations_total{kind="delete",result="cancelled"} 1`,
		`shipyard_operations_total{kind="deploy",result="failed"} 1`,
		`shipyard_operations_total{kind="deploy",result="succeeded"} 1`,
		`shipyard_operations_total{kind="rollback",result="succeeded"} 1`,
		`shipyard_operation_duration_seconds_bucket{kind="deploy",result="succeeded",le="30"} 0`,
		`shipyard_operation_duration_seconds_bucket{kind="deploy",result="succeeded",le="60"} 1`,
		`shipyard_operation_duration_seconds_sum{kind="deploy",result="succeeded"} 40`,
		`shipyard_queue_operations{status="queued"} 2`,
		`shipyard_queue_operations{status="running"} 1`,
		`shipyard_queue_oldest_wait_seconds 7`,
		`shipyard_database_up 1`,
	} {
		if !strings.Contains(got, want+"\n") {
			t.Errorf("missing %s in:\n%s", want, got)
		}
	}

	// The database is gone: no queue numbers, database_up 0, counters kept.
	f.err = errors.New("connection refused")
	got = scrape(t, reg)
	if strings.Contains(got, "shipyard_queue_operations{") || !strings.Contains(got, "shipyard_database_up 0\n") ||
		!strings.Contains(got, `shipyard_operations_total{kind="deploy",result="failed"} 1`) {
		t.Errorf("database down:\n%s", got)
	}
}

func TestWorkerMetricsPages(t *testing.T) {
	t0 := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	f := &fakeMetricsStore{}
	reg := &metrics.Registry{}
	if _, err := newWorkerMetrics(context.Background(), f, reg, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	for i := range 2*finishPage + 5 {
		f.ops = append(f.ops, store.FinishedOperation{ID: "op" + string(rune('a'+i%26)) + time.Duration(i).String(),
			Kind: "deploy", Status: "succeeded", CreatedAt: t0, FinishedAt: t0.Add(time.Duration(i+1) * time.Hour)})
	}
	scrape(t, reg)
	got := scrape(t, reg) // a repeated scrape counts nothing again
	if want := `shipyard_operations_total{kind="deploy",result="succeeded"} 2005` + "\n"; !strings.Contains(got, want) {
		t.Errorf("want %s in:\n%s", want, got)
	}
}
