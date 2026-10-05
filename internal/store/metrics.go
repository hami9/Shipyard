package store

import (
	"context"
	"time"
)

// QueueStats is the queue at one moment, for metrics (ADR-0013).
type QueueStats struct {
	Queued, Running int
	// OldestWait is how long the longest-waiting queued operation has been
	// due (since its run_after); 0 when none is due.
	OldestWait time.Duration
}

// QueueStats counts queued and running operations.
func (s *Store) QueueStats(ctx context.Context) (QueueStats, error) {
	var st QueueStats
	var wait float64
	err := s.q.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status = 'queued'),
		       count(*) FILTER (WHERE status = 'running'),
		       coalesce(extract(epoch FROM now() - min(run_after) FILTER (WHERE status = 'queued' AND run_after <= now())), 0)::float8
		FROM operations WHERE status IN ('queued', 'running')`).Scan(&st.Queued, &st.Running, &wait)
	st.OldestWait = time.Duration(wait * float64(time.Second))
	return st, mapError(err)
}

// FinishedOperation is an operation that succeeded, failed, or was
// cancelled.
type FinishedOperation struct {
	ID, Kind, Status      string
	CreatedAt, FinishedAt time.Time
}

// FinishedOperations returns up to limit operations finished after the
// given time, oldest finish first.
func (s *Store) FinishedOperations(ctx context.Context, after time.Time, limit int) ([]FinishedOperation, error) {
	rows, err := s.q.Query(ctx, `
		SELECT id, kind, status, created_at, finished_at FROM operations
		WHERE finished_at > $1 ORDER BY finished_at, id LIMIT $2`, after, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var out []FinishedOperation
	for rows.Next() {
		var o FinishedOperation
		if err := rows.Scan(&o.ID, &o.Kind, &o.Status, &o.CreatedAt, &o.FinishedAt); err != nil {
			return nil, mapError(err)
		}
		out = append(out, o)
	}
	return out, mapError(rows.Err())
}

// LastFinished is the newest finished_at of any operation; the zero time
// when none has finished.
func (s *Store) LastFinished(ctx context.Context) (time.Time, error) {
	var t *time.Time
	if err := s.q.QueryRow(ctx, `SELECT max(finished_at) FROM operations`).Scan(&t); err != nil {
		return time.Time{}, mapError(err)
	}
	if t == nil {
		return time.Time{}, nil
	}
	return *t, nil
}
