//go:build integration

package queue_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/hami9/shipyard/internal/queue"
	"github.com/hami9/shipyard/internal/store"
	"github.com/hami9/shipyard/internal/store/storetest"
	"github.com/hami9/shipyard/migrations"
)

func ptr[T any](v T) *T { return &v }

func newStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	ctx := t.Context()
	pool, err := store.Open(ctx, storetest.NewDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ms, _ := store.LoadMigrations(migrations.FS)
	if _, err := store.Migrate(ctx, pool, ms); err != nil {
		t.Fatal(err)
	}
	s := store.New(pool)
	u, err := s.CreateUser(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, store.NewApp{OwnerID: u.ID, Slug: "web", RepoFullName: "hami9/demo",
		AppSettings: store.AppSettings{Branch: ptr("main"), InternalPort: ptr(3000)}})
	if err != nil {
		t.Fatal(err)
	}
	return s, app.ID
}

// stalled lets heartbeats through until stall is closed, then fails them as
// a network partition would.
type stalled struct {
	*store.Store
	stall chan struct{}
}

func (s stalled) HeartbeatOperation(ctx context.Context, id, owner string, lease time.Duration) error {
	select {
	case <-s.stall:
		return errors.New("connection timed out")
	default:
		return s.Store.HeartbeatOperation(ctx, id, owner, lease)
	}
}

func TestLeaseHandover(t *testing.T) {
	s, app := newStore(t)
	ctx := t.Context()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	const lease = 600 * time.Millisecond

	if _, err := s.EnqueueOperation(ctx, store.NewOperation{AppID: app, Kind: "deploy", IdempotencyKey: "k1"}); err != nil {
		t.Fatal(err)
	}
	a := stalled{Store: s, stall: make(chan struct{})}
	workerA := queue.New(a, log, "worker-a", lease, 50*time.Millisecond)
	workerB := queue.New(s, log, "worker-b", lease, 50*time.Millisecond)

	op, err := workerA.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	held, release := workerA.Hold(ctx, op)
	defer release()

	// While A heartbeats, the lease never expires and B finds nothing.
	time.Sleep(2 * lease)
	if n, _, err := s.RequeueExpired(ctx); err != nil || n != 0 {
		t.Fatalf("RequeueExpired while held = %d, %v", n, err)
	}
	short, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	if _, err := workerB.Next(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("worker B claimed a held op: %v", err)
	}
	cancel()

	// A loses the database: its lease runs out, it stops itself, and the
	// reconciler hands the operation to B.
	close(a.stall)
	select {
	case <-held.Done():
	case <-time.After(3 * lease):
		t.Fatal("worker A kept working without a lease")
	}
	if !errors.Is(context.Cause(held), store.ErrLeaseLost) {
		t.Fatalf("cause = %v", context.Cause(held))
	}
	time.Sleep(lease / 3) // let the last renewed lease run out
	if n, _, err := s.RequeueExpired(ctx); err != nil || n != 1 {
		t.Fatalf("RequeueExpired = %d, %v; want 1", n, err)
	}
	got, err := workerB.Next(ctx)
	if err != nil || got.ID != op.ID || got.Attempt != 2 || got.LeaseOwner != "worker-b" {
		t.Fatalf("handover = %+v, %v", got, err)
	}
	if err := s.CompleteOperation(ctx, op.ID, "worker-a"); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("stale worker completed the op: %v", err)
	}
}

func TestEvents(t *testing.T) {
	s, app := newStore(t)
	ctx := t.Context()
	e, _ := s.EnqueueOperation(ctx, store.NewOperation{AppID: app, Kind: "deploy", IdempotencyKey: "k"})
	op := e.Operation.ID

	for i, msg := range []string{"queued", "building", "step 1/3"} {
		ev, err := s.AppendOperationEvent(ctx, op, store.LevelInfo, msg)
		if err != nil || ev.Seq != int64(i+1) {
			t.Fatalf("append %q = %+v, %v", msg, ev, err)
		}
	}
	// Oversized binary output (NUL bytes) is sanitized and truncated, not rejected.
	long, err := s.AppendOperationEvent(ctx, op, store.LevelWarn, string(make([]byte, store.MaxEventMessage+10)))
	if err != nil || len([]rune(long.Message)) != store.MaxEventMessage {
		t.Fatalf("long message: %d chars, %v", len([]rune(long.Message)), err)
	}
	bin, err := s.AppendOperationEvent(ctx, op, store.LevelInfo, "tar: \xff\xfe\x00 garbage")
	if err != nil || bin.Message != "tar: �� garbage" {
		t.Fatalf("invalid UTF-8 line = %q, %v", bin.Message, err)
	}
	if _, err := s.AppendOperationEvent(ctx, op, "fatal", "x"); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("bad level: %v", err)
	}

	resume, err := s.OperationEvents(ctx, op, 2, 10)
	if err != nil || len(resume) != 3 || resume[0].Seq != 3 || resume[0].Message != "step 1/3" {
		t.Fatalf("resume after seq 2 = %+v, %v", resume, err)
	}
	if page, _ := s.OperationEvents(ctx, op, 0, 2); len(page) != 2 || page[1].Seq != 2 {
		t.Fatalf("limit: %+v", page)
	}

	// Concurrent writers still get distinct, gapless seqs.
	done := make(chan error, 20)
	for range 20 {
		go func() { _, err := s.AppendOperationEvent(ctx, op, store.LevelDebug, "line"); done <- err }()
	}
	for range 20 {
		if err := <-done; err != nil {
			t.Fatalf("concurrent append: %v", err)
		}
	}
	all, _ := s.OperationEvents(ctx, op, 0, 100)
	for i, ev := range all {
		if ev.Seq != int64(i+1) {
			t.Fatalf("seq gap at %d: %d", i, ev.Seq)
		}
	}
	if len(all) != 25 {
		t.Fatalf("events = %d, want 25", len(all))
	}
}
