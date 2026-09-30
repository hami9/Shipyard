//go:build integration

package store_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hami9/shipyard/internal/store"
)

// P3.4b: the event cap (ADR-0006). Finished operations beyond each app's
// newest keep lose their events; an unfinished one never does. An oversized
// log keeps its head, its tail, and its warnings and errors, with one marker
// in the gap. A second pass changes nothing.
func TestTrimOperationEvents(t *testing.T) {
	f := newQueueFixture(t)
	ctx := t.Context()
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	op := func(app, status string, minute int) string {
		t.Helper()
		var id string
		err := f.db.QueryRow(ctx, `INSERT INTO operations (app_id, kind, idempotency_key, status, created_at,
				finished_at, lease_owner, lease_expires_at)
			VALUES ($1, 'deploy', $2, $3, $4,
				CASE WHEN $3 IN ('succeeded', 'failed') THEN now() END,
				CASE WHEN $3 = 'running' THEN 'w1' END, CASE WHEN $3 = 'running' THEN now() + interval '1 minute' END)
			RETURNING id`, app, "k-"+uniq(), status, base.Add(time.Duration(minute)*time.Minute)).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	appendN := func(opID string, n int) {
		t.Helper()
		for range n {
			if _, err := f.s.AppendOperationEvent(ctx, opID, store.LevelInfo, "line"); err != nil {
				t.Fatal(err)
			}
		}
	}
	count := func(opID string) int {
		t.Helper()
		events, err := f.s.OperationEvents(ctx, opID, 0, 1000)
		if err != nil {
			t.Fatal(err)
		}
		return len(events)
	}

	a, b := f.app(), f.app()
	queued := op(a, "queued", 1) // the oldest, but not finished
	old := op(a, "failed", 2)
	kept := op(a, "succeeded", 3)
	running := op(a, "running", 4)
	for _, id := range []string{queued, old, kept, running} {
		appendN(id, 3)
	}
	// b's only operation: 11 events of 100 bytes, an error at seq 6.
	big := op(b, "succeeded", 1)
	for i := 1; i <= 11; i++ {
		level := store.LevelInfo
		if i == 6 {
			level = store.LevelError
		}
		if _, err := f.s.AppendOperationEvent(ctx, big, level, fmt.Sprintf("%03d%s", i, strings.Repeat("x", 97))); err != nil {
			t.Fatal(err)
		}
	}

	got, err := f.s.TrimOperationEvents(ctx, 2, 400)
	if err != nil || got != (store.Trimmed{Operations: 2, Events: 3 + 6}) {
		t.Fatalf("trim = %+v, %v; want 2 operations, 9 events", got, err)
	}
	for id, want := range map[string]int{queued: 3, old: 0, kept: 3, running: 3} {
		if n := count(id); n != want {
			t.Errorf("operation %s: %d events, want %d", id[:8], n, want)
		}
	}
	// Head (seq 1–2, 200 bytes), the marker at seq 3, the error, the tail
	// (seq 10–11).
	events, _ := f.s.OperationEvents(ctx, big, 0, 100)
	var seqs []string
	for _, e := range events {
		seqs = append(seqs, fmt.Sprintf("%d:%s", e.Seq, e.Level))
	}
	if strings.Join(seqs, " ") != "1:info 2:info 3:warn 6:error 10:info 11:info" ||
		!strings.Contains(events[2].Message, "6 log lines (600 bytes) removed by retention") {
		t.Fatalf("trimmed log = %v\nmarker %q", seqs, events[2].Message)
	}
	// Resume after the marker still works, and a second pass is a no-op.
	if after, _ := f.s.OperationEvents(ctx, big, 3, 100); len(after) != 3 || after[0].Seq != 6 {
		t.Fatalf("resume after seq 3 = %+v", after)
	}
	if got, err := f.s.TrimOperationEvents(ctx, 2, 400); err != nil || got != (store.Trimmed{}) {
		t.Fatalf("second trim = %+v, %v", got, err)
	}
}
