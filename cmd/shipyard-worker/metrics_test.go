package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hami9/shipyard/internal/api"
	"github.com/hami9/shipyard/internal/metrics"
	"github.com/hami9/shipyard/internal/monitor"
	"github.com/hami9/shipyard/internal/reconcile"
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

// P5.5b: the reconciler's report replaces the app series; an app that is
// no longer active drops out.
func TestWorkerMetricsHealth(t *testing.T) {
	reg := &metrics.Registry{}
	m, err := newWorkerMetrics(context.Background(), &fakeMetricsStore{}, reg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	m.now = func() time.Time { return time.Unix(1_790_000_000, 500e6) }
	if got := scrape(t, reg); !strings.Contains(got, "shipyard_app_health_checked_timestamp_seconds 0\n") {
		t.Fatalf("before a check:\n%s", got)
	}
	m.ReportHealth([]reconcile.AppHealth{{App: "old", Running: true, Healthy: true}})
	m.ReportHealth([]reconcile.AppHealth{{App: "web", Running: true, Healthy: false}, {App: "api", Running: false}})
	got := scrape(t, reg)
	for _, want := range []string{
		`shipyard_app_up{app="api"} 0`, `shipyard_app_up{app="web"} 1`,
		`shipyard_app_healthy{app="api"} 0`, `shipyard_app_healthy{app="web"} 0`,
		`shipyard_app_health_checked_timestamp_seconds 1.7900000005e+09`,
	} {
		if !strings.Contains(got, want+"\n") {
			t.Errorf("missing %s in:\n%s", want, got)
		}
	}
	if strings.Contains(got, `app="old"`) {
		t.Errorf("an app no longer active is still exposed:\n%s", got)
	}
}

// P5.6: a check replaces the disk and certificate series; a certificate
// that could not be read has only shipyard_certificate_ok, at 0.
func TestWorkerMetricsChecks(t *testing.T) {
	reg := &metrics.Registry{}
	m, err := newWorkerMetrics(context.Background(), &fakeMetricsStore{}, reg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Unix(1_790_000_000, 0)
	m.ReportChecks(monitor.Report{At: t0, Certs: []monitor.Cert{{Hostname: "gone.example", NotAfter: t0}}})
	m.ReportChecks(monitor.Report{At: t0,
		Disks: []monitor.Disk{{Path: "/var/lib/docker", Size: 100, Used: 85, Avail: 15}},
		Certs: []monitor.Cert{
			{Hostname: "app.example", NotBefore: t0.Add(-80 * time.Hour), NotAfter: t0.Add(20 * time.Hour)},
			{Hostname: "new.example", Err: errors.New("tls: internal error")},
		}})
	got := scrape(t, reg)
	for _, want := range []string{
		`shipyard_filesystem_size_bytes{path="/var/lib/docker"} 100`,
		`shipyard_filesystem_avail_bytes{path="/var/lib/docker"} 15`,
		`shipyard_filesystem_used_ratio{path="/var/lib/docker"} 0.85`,
		`shipyard_certificate_ok{hostname="app.example"} 1`,
		`shipyard_certificate_ok{hostname="new.example"} 0`,
		`shipyard_certificate_lifetime_used_ratio{hostname="app.example"} 0.8`,
		`shipyard_certificate_not_after_timestamp_seconds{hostname="app.example"} 1.790072e+09`,
		`shipyard_checks_timestamp_seconds 1.79e+09`,
	} {
		if !strings.Contains(got, want+"\n") {
			t.Errorf("missing %s in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "gone.example") || strings.Contains(got, `_used_ratio{hostname="new.example"}`) ||
		strings.Contains(got, `not_after_timestamp_seconds{hostname="new.example"}`) {
		t.Errorf("stale or unreadable series exposed:\n%s", got)
	}
}

// Every metric the shipped alert rules use exists, in the worker's or the
// API's registry, so a rename cannot silently break an alert.
func TestAlertRulesUseRealMetrics(t *testing.T) {
	rules, err := os.ReadFile("../../deploy/prometheus/shipyard-alerts.yml")
	if err != nil {
		t.Fatal(err)
	}
	reg := &metrics.Registry{}
	if _, err := newWorkerMetrics(context.Background(), &fakeMetricsStore{}, reg, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	apiReg := &metrics.Registry{}
	api.NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), api.Deps{Metrics: apiReg})
	known := map[string]bool{}
	for _, exp := range []string{scrape(t, reg), scrape(t, apiReg)} {
		for _, m := range regexp.MustCompile(`(?m)^# TYPE (\S+) (\S+)$`).FindAllStringSubmatch(exp, -1) {
			known[m[1]] = true
			if m[2] == "histogram" {
				known[m[1]+"_bucket"], known[m[1]+"_sum"], known[m[1]+"_count"] = true, true, true
			}
		}
	}
	used := regexp.MustCompile(`\bshipyard_[a-z0-9_]+`).FindAllString(string(rules), -1)
	if len(used) < 8 {
		t.Fatalf("only %d metric references in the rules", len(used))
	}
	for _, name := range used {
		if !known[name] {
			t.Errorf("the alert rules use %s, which no registry exposes", name)
		}
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
