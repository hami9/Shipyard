package store

import (
	"context"
	"time"
)

// Audit results.
const (
	AuditSuccess = "success"
	AuditFailure = "failure"
	AuditDenied  = "denied"
)

// AuditEvent is one append-only audit record (ADR-0007). It names who did
// what to which target, never secret values.
type AuditEvent struct {
	ID        string
	TS        time.Time
	Actor     string // token:<prefix>, cli:<command>, webhook, or system
	Action    string
	Target    string
	Result    string
	RequestID string
}

// RecordAudit appends an audit event and fills in its ID and timestamp.
func (s *Store) RecordAudit(ctx context.Context, e AuditEvent) (AuditEvent, error) {
	err := s.q.QueryRow(ctx, `
		INSERT INTO audit_events (actor, action, target, result, request_id)
		VALUES ($1, $2, NULLIF($3, ''), $4, NULLIF($5, '')) RETURNING id, ts`,
		e.Actor, e.Action, e.Target, e.Result, e.RequestID).Scan(&e.ID, &e.TS)
	return e, mapError(err)
}

// AuditEvents returns the newest events first, at most limit of them.
func (s *Store) AuditEvents(ctx context.Context, limit int) ([]AuditEvent, error) {
	rows, err := s.q.Query(ctx, `
		SELECT id, ts, actor, action, coalesce(target, ''), result, coalesce(request_id, '')
		FROM audit_events ORDER BY ts DESC, id LIMIT $1`, limit)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()
	var events []AuditEvent
	for rows.Next() {
		var e AuditEvent
		if err := rows.Scan(&e.ID, &e.TS, &e.Actor, &e.Action, &e.Target, &e.Result, &e.RequestID); err != nil {
			return nil, mapError(err)
		}
		events = append(events, e)
	}
	return events, mapError(rows.Err())
}
