//go:build integration

package store_test

import (
	"testing"
	"time"
)

func TestQueueStatsAndFinished(t *testing.T) {
	f := newQueueFixture(t)
	ctx := t.Context()

	if last, err := f.s.LastFinished(ctx); err != nil || !last.IsZero() {
		t.Fatalf("LastFinished on an empty table = %v, %v", last, err)
	}
	st, err := f.s.QueueStats(ctx)
	if err != nil || st.Queued != 0 || st.Running != 0 || st.OldestWait != 0 {
		t.Fatalf("empty QueueStats = %+v, %v", st, err)
	}

	a, b, c := f.app(), f.app(), f.app()
	waiting := f.enqueue(a, "k-a").Operation
	later := f.enqueue(b, "k-b").Operation
	done := f.enqueue(c, "k-c").Operation
	f.sql(`UPDATE operations SET run_after = now() - interval '90 seconds' WHERE id = $1`, waiting.ID)
	f.sql(`UPDATE operations SET run_after = now() + interval '1 hour' WHERE id = $1`, later.ID) // not due
	f.sql(`UPDATE operations SET status = 'running', lease_owner = 'w', lease_expires_at = now() + interval '1 minute',
		attempt = 1 WHERE id = $1`, done.ID)

	st, err = f.s.QueueStats(ctx)
	if err != nil || st.Queued != 2 || st.Running != 1 || st.OldestWait < 90*time.Second || st.OldestWait > 10*time.Minute {
		t.Fatalf("QueueStats = %+v, %v", st, err)
	}

	f.sql(`UPDATE operations SET status = 'failed', finished_at = now(), lease_owner = NULL, lease_expires_at = NULL
		WHERE id = $1`, done.ID)
	last, err := f.s.LastFinished(ctx)
	if err != nil || last.IsZero() {
		t.Fatalf("LastFinished = %v, %v", last, err)
	}
	ops, err := f.s.FinishedOperations(ctx, time.Time{}, 10)
	if err != nil || len(ops) != 1 || ops[0].ID != done.ID || ops[0].Kind != "deploy" || ops[0].Status != "failed" ||
		!ops[0].FinishedAt.Equal(last) || ops[0].CreatedAt.After(ops[0].FinishedAt) {
		t.Fatalf("FinishedOperations = %+v, %v", ops, err)
	}
	if ops, err := f.s.FinishedOperations(ctx, last, 10); err != nil || len(ops) != 0 {
		t.Fatalf("FinishedOperations after the last = %+v, %v", ops, err)
	}
}
