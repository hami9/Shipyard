//go:build integration

package store_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hami9/shipyard/internal/store"
)

type queueFixture struct {
	t  *testing.T
	s  *store.Store
	db *pgxpool.Pool
	u  store.User
}

func newQueueFixture(t *testing.T) *queueFixture {
	s, u, db := newStore(t)
	return &queueFixture{t: t, s: s, db: db, u: u}
}

func (f *queueFixture) app() string {
	f.t.Helper()
	a, err := f.s.CreateApp(f.t.Context(), minimalApp(f.u.ID, "a"+uniq()))
	if err != nil {
		f.t.Fatal(err)
	}
	return a.ID
}

func (f *queueFixture) enqueue(app, key string) store.Enqueued {
	f.t.Helper()
	e, err := f.s.EnqueueOperation(f.t.Context(), store.NewOperation{AppID: app, Kind: "deploy", IdempotencyKey: key})
	if err != nil {
		f.t.Fatalf("enqueue %s: %v", key, err)
	}
	return e
}

func (f *queueFixture) op(id string) store.Operation {
	f.t.Helper()
	o, err := f.s.OperationByID(f.t.Context(), id)
	if err != nil {
		f.t.Fatal(err)
	}
	return o
}

func (f *queueFixture) sql(q string, args ...any) {
	f.t.Helper()
	if _, err := f.db.Exec(f.t.Context(), q, args...); err != nil {
		f.t.Fatal(err)
	}
}

func TestEnqueueIdempotent(t *testing.T) {
	f := newQueueFixture(t)
	ctx := t.Context()
	a, b := f.app(), f.app()

	first := f.enqueue(a, "gh:delivery-1")
	if !first.Created || first.Operation.Status != store.OpQueued || string(first.Operation.Payload) != "{}" || first.Operation.MaxAttempts != 3 {
		t.Fatalf("first enqueue = %+v", first)
	}
	again := f.enqueue(a, "gh:delivery-1")
	if again.Created || again.Operation.ID != first.Operation.ID {
		t.Fatalf("redelivery created a second operation: %+v", again)
	}
	for name, n := range map[string]store.NewOperation{
		"other app":  {AppID: b, Kind: "deploy", IdempotencyKey: "gh:delivery-1"},
		"other kind": {AppID: a, Kind: "rollback", IdempotencyKey: "gh:delivery-1"},
	} {
		if _, err := f.s.EnqueueOperation(ctx, n); !errors.Is(err, store.ErrIdempotencyMismatch) {
			t.Errorf("%s: %v, want ErrIdempotencyMismatch", name, err)
		}
	}
	e, err := f.s.EnqueueOperation(ctx, store.NewOperation{AppID: a, Kind: "rollback", IdempotencyKey: "k-" + uniq(),
		Payload: json.RawMessage(`{"deployment_id":"x"}`), MaxAttempts: 1})
	if err != nil || e.Operation.MaxAttempts != 1 || string(e.Operation.Payload) != `{"deployment_id": "x"}` {
		t.Fatalf("custom payload and attempts = %+v, %v", e.Operation, err)
	}
	if _, err := f.s.EnqueueOperation(ctx, store.NewOperation{AppID: a, Kind: "deploy", IdempotencyKey: "k-" + uniq(), Payload: json.RawMessage(`[1]`)}); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("array payload: %v, want ErrInvalid", err)
	}
	if _, err := f.s.EnqueueOperation(ctx, store.NewOperation{AppID: "00000000-0000-4000-8000-000000000000", Kind: "deploy", IdempotencyKey: "k-" + uniq()}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown app: %v, want ErrNotFound", err)
	}
}

func TestEnqueueCoalesces(t *testing.T) {
	f := newQueueFixture(t)
	ctx := t.Context()
	a, other := f.app(), f.app()
	otherOp := f.enqueue(other, "k-other")

	o1 := f.enqueue(a, "k1")
	o2 := f.enqueue(a, "k2")
	if len(o2.Superseded) != 1 || o2.Superseded[0] != o1.Operation.ID {
		t.Fatalf("Superseded = %v, want [%s]", o2.Superseded, o1.Operation.ID)
	}
	if got := f.op(o1.Operation.ID); got.Status != store.OpCancelled || got.FinishedAt == nil || got.LastError != "superseded by "+o2.Operation.ID {
		t.Fatalf("superseded op = %+v", got)
	}
	if got := f.op(otherOp.Operation.ID); got.Status != store.OpQueued {
		t.Fatalf("another app's op was cancelled: %+v", got)
	}

	// A running operation is never interrupted.
	running, err := f.s.ClaimOperation(ctx, "w1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	o3 := f.enqueue(running.AppID, "k3-"+uniq())
	if got := f.op(running.ID); got.Status != store.OpRunning || len(o3.Superseded) != 0 {
		t.Fatalf("running op touched by coalescing: %+v, superseded %v", got, o3.Superseded)
	}
}

// Concurrent admissions for one app leave exactly one queued operation.
func TestEnqueueConcurrentLatestWins(t *testing.T) {
	f := newQueueFixture(t)
	a := f.app()
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.s.EnqueueOperation(t.Context(), store.NewOperation{AppID: a, Kind: "deploy", IdempotencyKey: fmt.Sprintf("c-%d", i)}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var queued, cancelled int
	if err := f.db.QueryRow(t.Context(), `SELECT count(*) FILTER (WHERE status = 'queued'), count(*) FILTER (WHERE status = 'cancelled')
		FROM operations WHERE app_id = $1`, a).Scan(&queued, &cancelled); err != nil {
		t.Fatal(err)
	}
	if queued != 1 || cancelled != 9 {
		t.Fatalf("queued=%d cancelled=%d, want 1 and 9", queued, cancelled)
	}
}

func TestClaim(t *testing.T) {
	f := newQueueFixture(t)
	ctx := t.Context()
	if _, err := f.s.ClaimOperation(ctx, "w1", time.Minute); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("empty queue: %v", err)
	}
	a, b := f.app(), f.app()
	oa := f.enqueue(a, "ka")
	ob := f.enqueue(b, "kb")

	got, err := f.s.ClaimOperation(ctx, "w1", time.Minute)
	if err != nil || got.ID != oa.Operation.ID || got.Status != store.OpRunning || got.LeaseOwner != "w1" || got.Attempt != 1 {
		t.Fatalf("claim oldest = %+v, %v", got, err)
	}
	if got.LeaseExpiresAt == nil || time.Until(*got.LeaseExpiresAt) < 50*time.Second {
		t.Fatalf("lease expiry %v, want about a minute ahead", got.LeaseExpiresAt)
	}

	// App a is busy, so its next queued op waits; app b's op is claimable.
	f.enqueue(a, "ka2")
	got, err = f.s.ClaimOperation(ctx, "w2", time.Minute)
	if err != nil || got.ID != ob.Operation.ID {
		t.Fatalf("second claim = %+v, %v; want app b's op", got, err)
	}
	if _, err := f.s.ClaimOperation(ctx, "w3", time.Minute); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("claim while every app is busy: %v", err)
	}
}

// Race: many workers, one queued op per app; each op is claimed once and an
// app never has two running operations (invariant 4).
func TestClaimRace(t *testing.T) {
	f := newQueueFixture(t)
	ctx := t.Context()
	a := f.app()
	f.enqueue(a, "single")
	// A second queued op for the same app, as a retry could leave behind,
	// bypassing coalescing.
	f.sql(`INSERT INTO operations (app_id, kind, idempotency_key) VALUES ($1, 'deploy', 'bypass')`, a)
	for range 3 {
		f.enqueue(f.app(), "k-"+uniq())
	}

	var mu sync.Mutex
	claimed := map[string]int{}
	var wg sync.WaitGroup
	for w := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			op, err := f.s.ClaimOperation(ctx, fmt.Sprintf("w%d", w), time.Minute)
			if errors.Is(err, store.ErrNotFound) {
				return
			}
			if err != nil {
				t.Errorf("worker %d: %v", w, err)
				return
			}
			mu.Lock()
			claimed[op.ID]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(claimed) != 4 {
		t.Fatalf("claimed %d distinct ops, want 4 (one per app): %v", len(claimed), claimed)
	}
	for id, n := range claimed {
		if n != 1 {
			t.Fatalf("op %s claimed %d times", id, n)
		}
	}
	var running int
	if err := f.db.QueryRow(ctx, `SELECT count(*) FROM operations WHERE app_id = $1 AND status = 'running'`, a).Scan(&running); err != nil || running != 1 {
		t.Fatalf("app a has %d running ops (%v)", running, err)
	}
}

func TestLeaseOwnership(t *testing.T) {
	f := newQueueFixture(t)
	ctx := t.Context()
	f.enqueue(f.app(), "k")
	op, err := f.s.ClaimOperation(ctx, "w1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	if err := f.s.HeartbeatOperation(ctx, op.ID, "w1", 2*time.Minute); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if got := f.op(op.ID); time.Until(*got.LeaseExpiresAt) < 110*time.Second {
		t.Fatalf("heartbeat did not extend the lease: %v", got.LeaseExpiresAt)
	}
	if err := f.s.SetOperationPhase(ctx, op.ID, "w1", "building"); err != nil || f.op(op.ID).Phase != "building" {
		t.Fatalf("set phase: %v", err)
	}
	for name, err := range map[string]error{
		"heartbeat by other": f.s.HeartbeatOperation(ctx, op.ID, "w2", time.Minute),
		"phase by other":     f.s.SetOperationPhase(ctx, op.ID, "w2", "starting"),
		"complete by other":  f.s.CompleteOperation(ctx, op.ID, "w2"),
	} {
		if !errors.Is(err, store.ErrLeaseLost) {
			t.Errorf("%s: %v, want ErrLeaseLost", name, err)
		}
	}
	if err := f.s.CompleteOperation(ctx, op.ID, "w1"); err != nil {
		t.Fatal(err)
	}
	got := f.op(op.ID)
	if got.Status != store.OpSucceeded || got.FinishedAt == nil || got.LeaseOwner != "" || got.LeaseExpiresAt != nil {
		t.Fatalf("completed op = %+v", got)
	}
	if err := f.s.HeartbeatOperation(ctx, op.ID, "w1", time.Minute); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("heartbeat after completion: %v", err)
	}
}

func TestFailAndRetry(t *testing.T) {
	f := newQueueFixture(t)
	ctx := t.Context()
	f.enqueue(f.app(), "k")
	op, _ := f.s.ClaimOperation(ctx, "w1", time.Minute)

	st, err := f.s.FailOperation(ctx, op.ID, "w1", "registry timeout", time.Hour)
	if err != nil || st != store.OpQueued {
		t.Fatalf("retryable failure = %q, %v", st, err)
	}
	if got := f.op(op.ID); got.LastError != "registry timeout" || got.FinishedAt != nil || time.Until(got.RunAfter) < 59*time.Minute {
		t.Fatalf("retry state = %+v", got)
	}
	if _, err := f.s.ClaimOperation(ctx, "w1", time.Minute); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("claimed before run_after: %v", err)
	}
	f.sql(`UPDATE operations SET run_after = now() - interval '1 second' WHERE id = $1`, op.ID)
	op, err = f.s.ClaimOperation(ctx, "w2", time.Minute)
	if err != nil || op.Attempt != 2 {
		t.Fatalf("second attempt = %+v, %v", op, err)
	}

	st, err = f.s.FailOperation(ctx, op.ID, "w2", "bad Dockerfile", 0)
	if err != nil || st != store.OpFailed || f.op(op.ID).FinishedAt == nil {
		t.Fatalf("permanent failure = %q, %v", st, err)
	}
	if _, err := f.s.FailOperation(ctx, op.ID, "w2", "again", 0); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("fail after finish: %v", err)
	}

	// Out of attempts: a retryable failure becomes final.
	e, _ := f.s.EnqueueOperation(ctx, store.NewOperation{AppID: f.app(), Kind: "deploy", IdempotencyKey: "one-shot", MaxAttempts: 1})
	op, _ = f.s.ClaimOperation(ctx, "w1", time.Minute)
	if op.ID != e.Operation.ID {
		t.Fatalf("claimed %s, want %s", op.ID, e.Operation.ID)
	}
	if st, err := f.s.FailOperation(ctx, op.ID, "w1", "flaky", time.Second); err != nil || st != store.OpFailed {
		t.Fatalf("last attempt = %q, %v; want failed", st, err)
	}
}

func TestRequeueExpired(t *testing.T) {
	f := newQueueFixture(t)
	ctx := t.Context()
	f.enqueue(f.app(), "k")
	op, _ := f.s.ClaimOperation(ctx, "crashed", time.Minute)
	f.sql(`UPDATE operations SET lease_expires_at = now() - interval '1 second' WHERE id = $1`, op.ID)

	requeued, failed, err := f.s.RequeueExpired(ctx)
	if err != nil || requeued != 1 || failed != 0 {
		t.Fatalf("RequeueExpired = %d, %d, %v", requeued, failed, err)
	}
	if err := f.s.HeartbeatOperation(ctx, op.ID, "crashed", time.Minute); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("old owner kept the lease: %v", err)
	}
	op, err = f.s.ClaimOperation(ctx, "w2", time.Minute)
	if err != nil || op.Attempt != 2 || op.LeaseOwner != "w2" {
		t.Fatalf("re-claim = %+v, %v", op, err)
	}

	// The last allowed attempt expiring fails the operation.
	f.sql(`UPDATE operations SET attempt = max_attempts, lease_expires_at = now() - interval '1 second' WHERE id = $1`, op.ID)
	if requeued, failed, err = f.s.RequeueExpired(ctx); err != nil || requeued != 0 || failed != 1 {
		t.Fatalf("exhausted RequeueExpired = %d, %d, %v", requeued, failed, err)
	}
	if got := f.op(op.ID); got.Status != store.OpFailed || got.FinishedAt == nil {
		t.Fatalf("exhausted op = %+v", got)
	}
	if requeued, failed, _ = f.s.RequeueExpired(ctx); requeued+failed != 0 {
		t.Fatal("RequeueExpired touched a live or finished op")
	}
}
