package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Operation statuses.
const (
	OpQueued    = "queued"
	OpRunning   = "running"
	OpSucceeded = "succeeded"
	OpFailed    = "failed"
	OpCancelled = "cancelled"
)

var (
	// ErrLeaseLost means the caller no longer holds the operation's lease:
	// it expired and was re-queued, or the operation finished. The caller
	// must stop working on it (ADR-0002).
	ErrLeaseLost = errors.New("operation lease lost")
	// ErrIdempotencyMismatch means an idempotency key was reused for a
	// different app or kind.
	ErrIdempotencyMismatch = errors.New("idempotency key already used for a different request")
)

// Operation is one unit of queued work, such as a deploy (ADR-0002).
type Operation struct {
	ID             string
	AppID          string
	Kind           string
	IdempotencyKey string
	Status         string
	Phase          string
	Payload        json.RawMessage
	LeaseOwner     string
	LeaseExpiresAt *time.Time
	Attempt        int
	MaxAttempts    int
	RunAfter       time.Time
	LastError      string
	FinishedAt     *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// NewOperation is the input to EnqueueOperation.
type NewOperation struct {
	AppID          string
	Kind           string
	IdempotencyKey string
	Payload        json.RawMessage // a JSON object; nil means {}
	MaxAttempts    int             // 0 means the database default
}

// Enqueued reports what EnqueueOperation did.
type Enqueued struct {
	Operation  Operation
	Created    bool     // false: an earlier request with the same key
	Superseded []string // queued operations of the app it cancelled
}

const opColumns = `id, app_id, kind, idempotency_key, status, coalesce(phase, ''), payload,
	coalesce(lease_owner, ''), lease_expires_at, attempt, max_attempts, run_after,
	coalesce(last_error, ''), finished_at, created_at, updated_at`

func scanOperation(row interface{ Scan(...any) error }) (Operation, error) {
	var o Operation
	err := row.Scan(&o.ID, &o.AppID, &o.Kind, &o.IdempotencyKey, &o.Status, &o.Phase, &o.Payload,
		&o.LeaseOwner, &o.LeaseExpiresAt, &o.Attempt, &o.MaxAttempts, &o.RunAfter,
		&o.LastError, &o.FinishedAt, &o.CreatedAt, &o.UpdatedAt)
	return o, mapError(err)
}

// micros converts a duration for "$n * interval '1 microsecond'".
func micros(d time.Duration) int64 { return d.Microseconds() }

// EnqueueOperation admits a request (ARCHITECTURE §5, step 1).
//
//   - Idempotent: a repeated key returns the first operation, unchanged
//     [GH-BP]. The same key for another app or kind is ErrIdempotencyMismatch.
//   - Latest wins: a new operation cancels the app's still-queued ones. A
//     running operation is never interrupted.
//
// The app row lock serializes admissions per app, so two concurrent requests
// cannot both stay queued.
func (s *Store) EnqueueOperation(ctx context.Context, n NewOperation) (Enqueued, error) {
	payload := n.Payload
	if payload == nil {
		payload = json.RawMessage(`{}`)
	}
	var out Enqueued
	err := s.InTx(ctx, func(tx *Store) error {
		if err := tx.LockApp(ctx, n.AppID); err != nil {
			return err
		}
		op, err := scanOperation(tx.q.QueryRow(ctx, `
			INSERT INTO operations (app_id, kind, idempotency_key, payload, max_attempts)
			VALUES ($1, $2, $3, $4, coalesce(nullif($5, 0), 3))
			ON CONFLICT (idempotency_key) DO NOTHING
			RETURNING `+opColumns, n.AppID, n.Kind, n.IdempotencyKey, payload, n.MaxAttempts))
		if errors.Is(err, ErrNotFound) { // the key exists
			op, err = scanOperation(tx.q.QueryRow(ctx,
				`SELECT `+opColumns+` FROM operations WHERE idempotency_key = $1`, n.IdempotencyKey))
			if err != nil {
				return err
			}
			if op.AppID != n.AppID || op.Kind != n.Kind {
				return ErrIdempotencyMismatch
			}
			out = Enqueued{Operation: op}
			return nil
		}
		if err != nil {
			return err
		}
		out = Enqueued{Operation: op, Created: true}
		rows, err := tx.q.Query(ctx, `
			UPDATE operations SET status = 'cancelled', finished_at = now(), last_error = 'superseded by ' || $2::text
			WHERE app_id = $1 AND status = 'queued' AND id <> $2::uuid
			RETURNING id`, n.AppID, op.ID)
		if err != nil {
			return mapError(err)
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return mapError(err)
			}
			out.Superseded = append(out.Superseded, id)
		}
		return mapError(rows.Err())
	})
	return out, err
}

// claimRetries bounds how often ClaimOperation retries after losing a race
// for an app to another worker.
const claimRetries = 3

// ClaimOperation leases the oldest eligible queued operation to owner
// (ARCHITECTURE §5, step 2). It skips rows other workers are claiming
// [PG-SELECT] and apps that already have a running operation. It returns
// ErrNotFound when nothing is claimable right now.
func (s *Store) ClaimOperation(ctx context.Context, owner string, lease time.Duration) (Operation, error) {
	for range claimRetries {
		op, err := s.claimOnce(ctx, owner, lease)
		// Two workers can pick queued operations of the same app at once;
		// the one-running-per-app index rejects the second (invariant 4).
		// That app is now busy, so the next try looks at other apps.
		if !errors.Is(err, ErrConflict) {
			return op, err
		}
	}
	return Operation{}, ErrNotFound
}

func (s *Store) claimOnce(ctx context.Context, owner string, lease time.Duration) (Operation, error) {
	return scanOperation(s.q.QueryRow(ctx, `
		WITH next AS (
			SELECT o.id AS next_id FROM operations o
			WHERE o.status = 'queued' AND o.run_after <= now() AND o.attempt < o.max_attempts
			  AND NOT EXISTS (SELECT 1 FROM operations r WHERE r.app_id = o.app_id AND r.status = 'running')
			ORDER BY o.run_after, o.created_at
			LIMIT 1
			FOR UPDATE OF o SKIP LOCKED
		)
		UPDATE operations o SET status = 'running', lease_owner = $1,
			lease_expires_at = now() + $2 * interval '1 microsecond', attempt = o.attempt + 1
		FROM next WHERE o.id = next.next_id
		RETURNING `+opColumns, owner, micros(lease)))
}

// ownedUpdate runs an UPDATE guarded by "id = $1 AND lease_owner = $2 AND
// status = 'running'" and maps zero rows to ErrLeaseLost.
func (s *Store) ownedUpdate(ctx context.Context, set, id, owner string, args ...any) error {
	tag, err := s.q.Exec(ctx, `UPDATE operations SET `+set+`
		WHERE id = $1 AND lease_owner = $2 AND status = 'running'`, append([]any{id, owner}, args...)...)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

// HeartbeatOperation extends the lease. ErrLeaseLost means stop working.
func (s *Store) HeartbeatOperation(ctx context.Context, id, owner string, lease time.Duration) error {
	return s.ownedUpdate(ctx, `lease_expires_at = now() + $3 * interval '1 microsecond'`, id, owner, micros(lease))
}

// SetOperationPhase persists the phase before its side effect (invariant 3).
func (s *Store) SetOperationPhase(ctx context.Context, id, owner, phase string) error {
	return s.ownedUpdate(ctx, `phase = $3`, id, owner, phase)
}

// CompleteOperation marks the operation succeeded and releases the lease.
func (s *Store) CompleteOperation(ctx context.Context, id, owner string) error {
	return s.ownedUpdate(ctx, `status = 'succeeded', finished_at = now(), lease_owner = NULL, lease_expires_at = NULL`, id, owner)
}

// FailOperation records a failure. With retryAfter > 0 and attempts left, the
// operation is queued again after that delay; otherwise it is failed for good.
// It returns the resulting status.
func (s *Store) FailOperation(ctx context.Context, id, owner, reason string, retryAfter time.Duration) (string, error) {
	var status string
	err := s.q.QueryRow(ctx, `
		UPDATE operations SET
			status = CASE WHEN $4::float8 > 0 AND attempt < max_attempts THEN 'queued' ELSE 'failed' END,
			run_after = CASE WHEN $4::float8 > 0 AND attempt < max_attempts THEN now() + $4::float8 * interval '1 microsecond' ELSE run_after END,
			finished_at = CASE WHEN $4::float8 > 0 AND attempt < max_attempts THEN NULL ELSE now() END,
			last_error = $3, lease_owner = NULL, lease_expires_at = NULL
		WHERE id = $1 AND lease_owner = $2 AND status = 'running'
		RETURNING status`, id, owner, reason, micros(retryAfter)).Scan(&status)
	if errors.Is(mapError(err), ErrNotFound) {
		return "", ErrLeaseLost
	}
	return status, mapError(err)
}

// RequeueExpired returns operations whose lease expired to the queue, or
// fails them once they have used all attempts. The reconciler calls it
// (ARCHITECTURE §5, Reconciler step 1).
func (s *Store) RequeueExpired(ctx context.Context) (requeued, failed int, err error) {
	rows, err := s.q.Query(ctx, `
		UPDATE operations SET
			status = CASE WHEN attempt < max_attempts THEN 'queued' ELSE 'failed' END,
			finished_at = CASE WHEN attempt < max_attempts THEN NULL ELSE now() END,
			last_error = 'lease expired (worker stopped or lost the database)',
			lease_owner = NULL, lease_expires_at = NULL
		WHERE status = 'running' AND lease_expires_at < now()
		RETURNING status`)
	if err != nil {
		return 0, 0, mapError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var st string
		if err := rows.Scan(&st); err != nil {
			return 0, 0, mapError(err)
		}
		if st == OpQueued {
			requeued++
		} else {
			failed++
		}
	}
	return requeued, failed, mapError(rows.Err())
}

// OperationByID returns one operation.
func (s *Store) OperationByID(ctx context.Context, id string) (Operation, error) {
	return scanOperation(s.q.QueryRow(ctx, `SELECT `+opColumns+` FROM operations WHERE id = $1`, id))
}
