package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// Route maps a hostname to an app and, once one is active, to the dial
// address of its deployment. The Caddy config is rendered from these rows
// alone (ADR-0003).
type Route struct {
	ID           string
	AppID        string
	Hostname     string
	DeploymentID *string
	Upstream     string // empty while the app has no routed deployment
	DNSCheckedAt *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ListRoutes returns every route, ordered by hostname.
func (s *Store) ListRoutes(ctx context.Context) ([]Route, error) {
	rows, err := s.q.Query(ctx, `
		SELECT id, app_id, hostname, deployment_id, coalesce(upstream, ''), dns_checked_at, created_at, updated_at
		FROM routes ORDER BY hostname`)
	if err != nil {
		return nil, mapError(err)
	}
	routes, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Route])
	return routes, mapError(err)
}
