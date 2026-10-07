package queue

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/hami9/shipyard/internal/store"
)

// fakeStore returns scripted results. Time inside synctest bubbles is
// virtual, so these tests are exact and fast.
type fakeStore struct {
	mu         sync.Mutex
	claims     []error // results for successive claims; nil means success
	claimCalls int
	beats      []time.Time
	beatErr    func(n int) error // result for the nth heartbeat (1-based)
	hangFrom   int               // from this heartbeat on, block until ctx ends (0: never)
}

func (f *fakeStore) ClaimOperation(context.Context, string, time.Duration) (store.Operation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claimCalls++
	if len(f.claims) == 0 {
		return store.Operation{}, store.ErrNotFound
	}
	err := f.claims[0]
	f.claims = f.claims[1:]
	if err != nil {
		return store.Operation{}, err
	}
	return store.Operation{ID: "op-1"}, nil
}

func (f *fakeStore) HeartbeatOperation(ctx context.Context, _, _ string, _ time.Duration) error {
	f.mu.Lock()
	f.beats = append(f.beats, time.Now())
	n := len(f.beats)
	f.mu.Unlock()
	if f.hangFrom > 0 && n >= f.hangFrom { // a query stuck on a dead connection
		<-ctx.Done()
		return ctx.Err()
	}
	if f.beatErr != nil {
		return f.beatErr(n)
	}
	return nil
}

func newQueue(f *fakeStore) (*Queue, *bytes.Buffer) {
	var logs bytes.Buffer
	return New(f, slog.New(slog.NewTextHandler(&logs, nil)), "w1", time.Minute, 2*time.Second), &logs
}

func TestNextPollsUntilClaimed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeStore{claims: []error{store.ErrNotFound, errors.New("connection refused"), store.ErrNotFound, nil}}
		q, logs := newQueue(f)
		start := time.Now()
		op, err := q.Next(t.Context())
		if err != nil || op.ID != "op-1" {
			t.Fatalf("Next = %+v, %v", op, err)
		}
		if f.claimCalls != 4 || time.Since(start) != 6*time.Second {
			t.Fatalf("claims=%d after %v; want 4 claims, 3 poll intervals", f.claimCalls, time.Since(start))
		}
		if !bytes.Contains(logs.Bytes(), []byte("claim failed")) || bytes.Count(logs.Bytes(), []byte("claim failed")) != 1 {
			t.Fatalf("only the database error should be logged:\n%s", logs.String())
		}
	})
}

func TestNextStopsOnCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q, _ := newQueue(&fakeStore{})
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		if _, err := q.Next(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Next = %v, want deadline exceeded", err)
		}
	})
}

func TestHoldRenewsEveryThirdOfLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeStore{}
		q, _ := newQueue(f)
		start := time.Now()
		held, release := q.Hold(t.Context(), store.Operation{ID: "op-1"})
		time.Sleep(2*time.Minute + time.Second)
		release()
		if held.Err() == nil {
			t.Fatal("release did not cancel the held context")
		}
		if len(f.beats) != 6 || f.beats[0].Sub(start) != 20*time.Second {
			t.Fatalf("beats = %d, first after %v; want 6, every 20s", len(f.beats), f.beats[0].Sub(start))
		}
		if cause := context.Cause(held); !errors.Is(cause, context.Canceled) {
			t.Fatalf("cause after release = %v", cause)
		}
	})
}

func TestHoldCancelsWhenLeaseLost(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeStore{beatErr: func(n int) error {
			if n == 2 {
				return store.ErrLeaseLost
			}
			return nil
		}}
		q, _ := newQueue(f)
		held, release := q.Hold(t.Context(), store.Operation{ID: "op-1"})
		defer release()
		<-held.Done()
		if cause := context.Cause(held); !errors.Is(cause, store.ErrLeaseLost) {
			t.Fatalf("cause = %v, want ErrLeaseLost", cause)
		}
		time.Sleep(time.Minute)
		if len(f.beats) != 2 {
			t.Fatalf("heartbeats continued after the lease was lost: %d", len(f.beats))
		}
	})
}

// Transient failures are tolerated until a whole lease passes without a
// successful renewal; then the work is stopped.
func TestHoldGivesUpAfterLeaseWithoutRenewal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeStore{beatErr: func(n int) error {
			if n >= 2 {
				return errors.New("connection refused")
			}
			return nil
		}}
		q, logs := newQueue(f)
		start := time.Now()
		held, release := q.Hold(t.Context(), store.Operation{ID: "op-1"})
		defer release()
		<-held.Done()
		// Last success at 20s; failures at 40s, 60s, 80s; 80s-20s = one lease.
		if elapsed := time.Since(start); elapsed != 80*time.Second {
			t.Fatalf("gave up after %v, want 80s", elapsed)
		}
		if cause := context.Cause(held); !errors.Is(cause, store.ErrLeaseLost) {
			t.Fatalf("cause = %v", cause)
		}
		if bytes.Count(logs.Bytes(), []byte("lease heartbeat failed")) != 3 {
			t.Fatalf("logs:\n%s", logs.String())
		}
	})
}

// A heartbeat that hangs (a partitioned database) must not keep the work
// running past the lease: the call's deadline is the lease's expiry.
func TestHoldGivesUpWhenHeartbeatHangs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q, _ := newQueue(&fakeStore{hangFrom: 2})
		start := time.Now()
		held, release := q.Hold(t.Context(), store.Operation{ID: "op-1"})
		defer release()
		<-held.Done()
		// Last success at 20s; the second beat hangs until 20s + one lease.
		if elapsed := time.Since(start); elapsed != 80*time.Second {
			t.Fatalf("gave up after %v, want 80s", elapsed)
		}
		if cause := context.Cause(held); !errors.Is(cause, store.ErrLeaseLost) {
			t.Fatalf("cause = %v", cause)
		}
	})
}

func TestHoldFollowsParentContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fakeStore{}
		q, _ := newQueue(f)
		parent, cancel := context.WithCancel(t.Context())
		held, release := q.Hold(parent, store.Operation{ID: "op-1"})
		cancel()
		<-held.Done()
		release() // must not block after the parent ended
		if len(f.beats) != 0 {
			t.Fatalf("heartbeats after parent cancel: %d", len(f.beats))
		}
	})
}

func TestNewDefaults(t *testing.T) {
	q := New(&fakeStore{}, slog.Default(), "w1", 0, 0)
	if q.lease != DefaultLease || q.poll != DefaultPollInterval || q.Owner() != "w1" {
		t.Fatalf("defaults: lease %v, poll %v", q.lease, q.poll)
	}
}
