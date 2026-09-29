package store

import (
	"context"
	"errors"
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

const routeColumns = `id, app_id, hostname, deployment_id, coalesce(upstream, ''), dns_checked_at, created_at, updated_at`

// NewRoute is a hostname an operator adds to an app.
type NewRoute struct {
	AppID        string
	Hostname     string
	DNSCheckedAt *time.Time // nil when the DNS preflight was disabled
}

// CreateRoute adds a hostname to an app. If the app has an active
// deployment, the route targets it at once: with the upstream its other
// routes already use, or else upstream(deploymentID). The app row lock
// serializes this with activation, which moves routes of the superseded
// deployment along (ActivateDeployment), so a new route never stays on a
// drained container. A duplicate hostname is ErrConflict.
func (s *Store) CreateRoute(ctx context.Context, n NewRoute, upstream func(deploymentID string) string) (Route, error) {
	var out Route
	err := s.InTx(ctx, func(tx *Store) error {
		if err := tx.LockApp(ctx, n.AppID); err != nil {
			return err
		}
		var depID, up *string
		active, err := tx.ActiveDeployment(ctx, n.AppID)
		switch {
		case err == nil:
			depID = &active.ID
			var existing string
			err := tx.q.QueryRow(ctx, `SELECT upstream FROM routes WHERE app_id = $1 AND deployment_id = $2 LIMIT 1`,
				n.AppID, active.ID).Scan(&existing)
			switch {
			case err == nil:
				up = &existing
			case errors.Is(mapError(err), ErrNotFound):
				u := upstream(active.ID)
				up = &u
			default:
				return mapError(err)
			}
		case !errors.Is(err, ErrNotFound):
			return err
		}
		out, err = scanRoute(tx.q.QueryRow(ctx, `
			INSERT INTO routes (app_id, hostname, deployment_id, upstream, dns_checked_at)
			VALUES ($1, $2, $3, $4, $5) RETURNING `+routeColumns, n.AppID, n.Hostname, depID, up, n.DNSCheckedAt))
		return err
	})
	return out, err
}

// RoutesByApp returns an app's routes, ordered by hostname.
func (s *Store) RoutesByApp(ctx context.Context, appID string) ([]Route, error) {
	rows, err := s.q.Query(ctx, `SELECT `+routeColumns+` FROM routes WHERE app_id = $1 ORDER BY hostname`, appID)
	if err != nil {
		return nil, mapError(err)
	}
	routes, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Route])
	return routes, mapError(err)
}

// DeleteRoute removes one of an app's hostnames; ErrNotFound if it has none
// such. Caddy drops it at the worker's next sync.
func (s *Store) DeleteRoute(ctx context.Context, appID, hostname string) error {
	tag, err := s.q.Exec(ctx, `DELETE FROM routes WHERE app_id = $1 AND hostname = $2`, appID, hostname)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanRoute(row interface{ Scan(...any) error }) (Route, error) {
	var r Route
	err := row.Scan(&r.ID, &r.AppID, &r.Hostname, &r.DeploymentID, &r.Upstream, &r.DNSCheckedAt, &r.CreatedAt, &r.UpdatedAt)
	return r, mapError(err)
}

// ListRoutes returns every route, ordered by hostname.
func (s *Store) ListRoutes(ctx context.Context) ([]Route, error) {
	rows, err := s.q.Query(ctx, `SELECT `+routeColumns+` FROM routes ORDER BY hostname`)
	if err != nil {
		return nil, mapError(err)
	}
	routes, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Route])
	return routes, mapError(err)
}
