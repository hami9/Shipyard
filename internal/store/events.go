package store

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// MaxEventMessage is the longest stored event message, in characters. It
// mirrors the CHECK on operation_events.message.
const MaxEventMessage = 16384

// Event levels.
const (
	LevelDebug = "debug"
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
)

// OperationEvent is one append-only log line of an operation. Seq is the
// SSE event ID for Last-Event-ID resume [WHATWG-SSE].
type OperationEvent struct {
	OperationID string
	Seq         int64
	TS          time.Time
	Level       string
	Message     string
}

// AppendOperationEvent adds an event with the next seq, gapless per
// operation. A message over MaxEventMessage characters is truncated and
// marked rather than rejected, so a long log line never fails a deploy.
//
// Writers of one operation are serialized by a row lock on it. The next seq
// is read in a second statement, whose fresh snapshot sees the previous
// writer's commit (READ COMMITTED takes one snapshot per statement).
func (s *Store) AppendOperationEvent(ctx context.Context, opID, level, message string) (OperationEvent, error) {
	message = truncate(sanitize(message), MaxEventMessage)
	e := OperationEvent{OperationID: opID, Level: level, Message: message}
	err := s.InTx(ctx, func(tx *Store) error {
		var one int
		if err := tx.q.QueryRow(ctx, `SELECT 1 FROM operations WHERE id = $1 FOR NO KEY UPDATE`, opID).Scan(&one); err != nil {
			return mapError(err)
		}
		return mapError(tx.q.QueryRow(ctx, `
			INSERT INTO operation_events (operation_id, seq, level, message)
			SELECT $1, coalesce(max(seq), 0) + 1, $2, $3 FROM operation_events WHERE operation_id = $1
			RETURNING seq, ts`, opID, level, message).Scan(&e.Seq, &e.TS))
	})
	return e, err
}

// OperationEvents returns up to limit events with seq > afterSeq, oldest
// first. Pass the last seen seq to resume a stream.
func (s *Store) OperationEvents(ctx context.Context, opID string, afterSeq int64, limit int) ([]OperationEvent, error) {
	rows, err := s.q.Query(ctx, `
		SELECT operation_id, seq, ts, level, message FROM operation_events
		WHERE operation_id = $1 AND seq > $2 ORDER BY seq LIMIT $3`, opID, afterSeq, limit)
	if err != nil {
		return nil, mapError(err)
	}
	events, err := pgx.CollectRows(rows, pgx.RowToStructByPos[OperationEvent])
	return events, mapError(err)
}

// Trimmed reports what TrimOperationEvents removed.
type Trimmed struct {
	Operations int   // operations that lost events
	Events     int64 // events removed
}

// TrimOperationEvents caps the stored build and deploy logs (ADR-0006). It
// changes finished operations only, so a live stream never loses events:
//   - operations beyond each app's newest keep lose all their events (the
//     operation rows stay);
//   - an operation whose events exceed maxBytes keeps its first and last
//     maxBytes/2, plus every warning and error, and the gap becomes one
//     warning event at the first removed seq that says what was removed.
//
// Seqs stay unique and ordered, so Last-Event-ID resume still works.
func (s *Store) TrimOperationEvents(ctx context.Context, keep int, maxBytes int64) (Trimmed, error) {
	var t Trimmed
	err := s.InTx(ctx, func(tx *Store) error {
		var ops int
		err := tx.q.QueryRow(ctx, `
			WITH old AS (
				SELECT id FROM (
					SELECT id, finished_at,
						row_number() OVER (PARTITION BY app_id ORDER BY created_at DESC, id DESC) AS n
					FROM operations
				) o WHERE n > $1 AND finished_at IS NOT NULL
			), gone AS (
				DELETE FROM operation_events e USING old WHERE e.operation_id = old.id
				RETURNING e.operation_id
			)
			SELECT count(DISTINCT operation_id), count(*) FROM gone`, keep).Scan(&ops, &t.Events)
		if err != nil {
			return mapError(err)
		}
		t.Operations = ops

		rows, err := tx.q.Query(ctx, `
			WITH big AS (
				SELECT e.operation_id FROM operation_events e JOIN operations o ON o.id = e.operation_id
				WHERE o.finished_at IS NOT NULL
				GROUP BY e.operation_id HAVING sum(octet_length(e.message)) > $1
			), ranked AS (
				SELECT operation_id, seq, level,
					sum(octet_length(message)) OVER (PARTITION BY operation_id ORDER BY seq) AS head,
					sum(octet_length(message)) OVER (PARTITION BY operation_id ORDER BY seq DESC) AS tail
				FROM operation_events WHERE operation_id IN (SELECT operation_id FROM big)
			), gone AS (
				DELETE FROM operation_events e USING ranked r
				WHERE e.operation_id = r.operation_id AND e.seq = r.seq
					AND r.head > $1 / 2 AND r.tail > $1 / 2 AND r.level NOT IN ('warn', 'error')
				RETURNING e.operation_id, e.seq, octet_length(e.message) AS bytes
			)
			SELECT operation_id, min(seq), count(*), sum(bytes) FROM gone GROUP BY operation_id`, maxBytes)
		if err != nil {
			return mapError(err)
		}
		type gap struct {
			op           string
			seq, n, size int64
		}
		gaps, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (gap, error) {
			var g gap
			return g, row.Scan(&g.op, &g.seq, &g.n, &g.size)
		})
		if err != nil {
			return mapError(err)
		}
		for _, g := range gaps {
			msg := fmt.Sprintf("… %d log lines (%d bytes) removed by retention: this operation's log is capped at %d bytes", g.n, g.size, maxBytes)
			if _, err := tx.q.Exec(ctx, `INSERT INTO operation_events (operation_id, seq, level, message) VALUES ($1, $2, 'warn', $3)`,
				g.op, g.seq, msg); err != nil {
				return mapError(err)
			}
			t.Operations++
			t.Events += g.n
		}
		return nil
	})
	return t, err
}

// sanitize makes arbitrary process output storable as PostgreSQL text, which
// rejects NUL and invalid UTF-8: build logs can contain both.
func sanitize(s string) string {
	return strings.ReplaceAll(strings.ToValidUTF8(s, "�"), "\x00", "�")
}

const truncatedMark = " …[truncated]"

// truncate shortens s to at most n characters, on a character boundary.
func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	keep := n - utf8.RuneCountInString(truncatedMark)
	i := 0
	for pos := range s {
		if i == keep {
			return s[:pos] + truncatedMark
		}
		i++
	}
	return s
}
