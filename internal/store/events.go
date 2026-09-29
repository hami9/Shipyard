package store

import (
	"context"
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
