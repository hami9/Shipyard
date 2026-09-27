// Package queue is the worker's side of the PostgreSQL operation queue
// (ADR-0002): it polls for claimable operations and keeps a claimed
// operation's lease alive while work runs.
package queue

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/hami9/shipyard/internal/store"
)

// Store is what the queue needs from persistence.
type Store interface {
	ClaimOperation(ctx context.Context, owner string, lease time.Duration) (store.Operation, error)
	HeartbeatOperation(ctx context.Context, id, owner string, lease time.Duration) error
}

// Defaults from ADR-0002.
const (
	DefaultLease        = time.Minute
	DefaultPollInterval = 2 * time.Second
)

// Queue claims and holds operations for one worker.
type Queue struct {
	store Store
	log   *slog.Logger
	owner string
	lease time.Duration
	poll  time.Duration
}

// New returns a Queue for the worker named owner. Zero durations take the
// defaults.
func New(s Store, log *slog.Logger, owner string, lease, poll time.Duration) *Queue {
	if lease <= 0 {
		lease = DefaultLease
	}
	if poll <= 0 {
		poll = DefaultPollInterval
	}
	return &Queue{store: s, log: log, owner: owner, lease: lease, poll: poll}
}

// Owner is the lease owner name this queue claims under.
func (q *Queue) Owner() string { return q.owner }

// Next blocks until it claims an operation or ctx ends. Database errors are
// logged and retried at the poll interval, so a restart of PostgreSQL does not
// stop the worker. Polling is the only wake-up for now; LISTEN/NOTIFY is an
// optional hint for later (ADR-0002).
func (q *Queue) Next(ctx context.Context) (store.Operation, error) {
	for {
		op, err := q.store.ClaimOperation(ctx, q.owner, q.lease)
		if err == nil {
			return op, nil
		}
		if ctx.Err() != nil {
			return store.Operation{}, ctx.Err()
		}
		if !errors.Is(err, store.ErrNotFound) {
			q.log.WarnContext(ctx, "claim failed; retrying", slog.Any("err", err))
		}
		t := time.NewTimer(q.poll)
		select {
		case <-ctx.Done():
			t.Stop()
			return store.Operation{}, ctx.Err()
		case <-t.C:
		}
	}
}

// Hold renews op's lease every lease/3 until release is called. The returned
// context is cancelled, with cause store.ErrLeaseLost, when the lease is lost
// or cannot be renewed before it expires. The work must then stop: another
// worker may already own the operation (invariant 3). release waits for the
// renewer to exit.
func (q *Queue) Hold(ctx context.Context, op store.Operation) (held context.Context, release func()) {
	held, cancel := context.WithCancelCause(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(q.lease / 3)
		defer tick.Stop()
		lastOK := time.Now()
		for {
			select {
			case <-held.Done():
				return
			case <-tick.C:
			}
			err := q.store.HeartbeatOperation(held, op.ID, q.owner, q.lease)
			switch {
			case err == nil:
				lastOK = time.Now()
			case errors.Is(err, store.ErrLeaseLost):
				q.log.WarnContext(ctx, "operation lease lost", slog.String("operation", op.ID))
				cancel(store.ErrLeaseLost)
				return
			case held.Err() != nil:
				return
			default:
				q.log.WarnContext(ctx, "lease heartbeat failed", slog.String("operation", op.ID), slog.Any("err", err))
				// Past the lease, the reconciler may have handed the op to
				// another worker; stop rather than risk two owners.
				if time.Since(lastOK) >= q.lease {
					cancel(store.ErrLeaseLost)
					return
				}
			}
		}
	}()
	return held, func() {
		cancel(context.Canceled)
		<-done
	}
}
