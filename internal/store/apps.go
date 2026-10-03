package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// App is a registered application: a repository, a branch, and how to build
// and run it.
type App struct {
	ID                   string
	OwnerID              string
	Slug                 string
	RepoFullName         string
	GitHubInstallationID *int64
	Branch               string
	DockerfilePath       string
	BuildContext         string
	InternalPort         int
	HealthPath           string
	HealthTimeout        time.Duration
	CPULimit             float64 // Docker --cpus
	MemoryLimit          int64   // bytes
	StopTimeout          time.Duration
	AutoDeploy           bool
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// AppSettings holds the fields that can be set at creation and changed later.
// A nil field keeps its current value, or the database default on create, so
// defaults live in one place: migrations/0002_schema_v1.sql.
type AppSettings struct {
	GitHubInstallationID *int64
	Branch               *string
	DockerfilePath       *string
	BuildContext         *string
	InternalPort         *int
	HealthPath           *string
	HealthTimeout        *time.Duration
	CPULimit             *float64
	MemoryLimit          *int64
	StopTimeout          *time.Duration
	AutoDeploy           *bool
}

// NewApp is the input to CreateApp. Branch and InternalPort are required.
type NewApp struct {
	OwnerID      string
	Slug         string
	RepoFullName string
	AppSettings
}

// Durations are stored as interval and exchanged as microseconds, which
// avoids depending on how a driver maps interval to time.Duration.
const appColumns = `id, owner_id, slug, repo_full_name, github_installation_id, branch,
	dockerfile_path, build_context, internal_port, health_path,
	(extract(epoch FROM health_timeout) * 1000000)::bigint,
	cpu_limit, memory_limit,
	(extract(epoch FROM stop_timeout) * 1000000)::bigint,
	auto_deploy, created_at, updated_at`

func scanApp(row interface{ Scan(...any) error }) (App, error) {
	var a App
	var healthUS, stopUS int64
	err := row.Scan(&a.ID, &a.OwnerID, &a.Slug, &a.RepoFullName, &a.GitHubInstallationID, &a.Branch,
		&a.DockerfilePath, &a.BuildContext, &a.InternalPort, &a.HealthPath, &healthUS,
		&a.CPULimit, &a.MemoryLimit, &stopUS, &a.AutoDeploy, &a.CreatedAt, &a.UpdatedAt)
	a.HealthTimeout = time.Duration(healthUS) * time.Microsecond
	a.StopTimeout = time.Duration(stopUS) * time.Microsecond
	return a, mapError(err)
}

// columnValues collects column = value pairs for the fields that are set.
// Column names are constants from this file, never caller input.
type columnValues struct {
	cols  []string
	exprs []string
	args  []any
}

func (c *columnValues) add(col string, v any) {
	c.args = append(c.args, v)
	c.cols = append(c.cols, col)
	c.exprs = append(c.exprs, fmt.Sprintf("$%d", len(c.args)))
}

func (c *columnValues) addDuration(col string, d time.Duration) {
	c.args = append(c.args, d.Microseconds())
	c.cols = append(c.cols, col)
	c.exprs = append(c.exprs, fmt.Sprintf("$%d * interval '1 microsecond'", len(c.args)))
}

func (c *columnValues) addSettings(s AppSettings) {
	if s.GitHubInstallationID != nil {
		c.add("github_installation_id", *s.GitHubInstallationID)
	}
	if s.Branch != nil {
		c.add("branch", *s.Branch)
	}
	if s.DockerfilePath != nil {
		c.add("dockerfile_path", *s.DockerfilePath)
	}
	if s.BuildContext != nil {
		c.add("build_context", *s.BuildContext)
	}
	if s.InternalPort != nil {
		c.add("internal_port", *s.InternalPort)
	}
	if s.HealthPath != nil {
		c.add("health_path", *s.HealthPath)
	}
	if s.HealthTimeout != nil {
		c.addDuration("health_timeout", *s.HealthTimeout)
	}
	if s.CPULimit != nil {
		c.add("cpu_limit", *s.CPULimit)
	}
	if s.MemoryLimit != nil {
		c.add("memory_limit", *s.MemoryLimit)
	}
	if s.StopTimeout != nil {
		c.addDuration("stop_timeout", *s.StopTimeout)
	}
	if s.AutoDeploy != nil {
		c.add("auto_deploy", *s.AutoDeploy)
	}
}

// CreateApp inserts an app. A duplicate slug returns ErrConflict; values the
// schema rejects return ErrInvalid; an unknown owner returns ErrReference.
func (s *Store) CreateApp(ctx context.Context, n NewApp) (App, error) {
	var c columnValues
	c.add("owner_id", n.OwnerID)
	c.add("slug", n.Slug)
	c.add("repo_full_name", n.RepoFullName)
	c.addSettings(n.AppSettings)
	sql := fmt.Sprintf(`INSERT INTO apps (%s) VALUES (%s) RETURNING %s`,
		strings.Join(c.cols, ", "), strings.Join(c.exprs, ", "), appColumns)
	return scanApp(s.q.QueryRow(ctx, sql, c.args...))
}

// UpdateApp changes the set fields of an app and returns the result.
func (s *Store) UpdateApp(ctx context.Context, id string, set AppSettings) (App, error) {
	var c columnValues
	c.addSettings(set)
	if len(c.cols) == 0 {
		return s.AppByID(ctx, id)
	}
	assignments := make([]string, len(c.cols))
	for i := range c.cols {
		assignments[i] = c.cols[i] + " = " + c.exprs[i]
	}
	c.args = append(c.args, id)
	sql := fmt.Sprintf(`UPDATE apps SET %s WHERE id = $%d RETURNING %s`,
		strings.Join(assignments, ", "), len(c.args), appColumns)
	return scanApp(s.q.QueryRow(ctx, sql, c.args...))
}

// AppByID returns ErrNotFound for an unknown id and ErrInvalid for a
// malformed one.
func (s *Store) AppByID(ctx context.Context, id string) (App, error) {
	return scanApp(s.q.QueryRow(ctx, `SELECT `+appColumns+` FROM apps WHERE id = $1`, id))
}

// AppBySlug returns ErrNotFound when no app has that slug.
func (s *Store) AppBySlug(ctx context.Context, slug string) (App, error) {
	return scanApp(s.q.QueryRow(ctx, `SELECT `+appColumns+` FROM apps WHERE slug = $1`, slug))
}

// ListApps returns every app ordered by slug.
func (s *Store) ListApps(ctx context.Context) ([]App, error) {
	rows, err := s.q.Query(ctx, `SELECT `+appColumns+` FROM apps ORDER BY slug`)
	if err != nil {
		return nil, mapError(err)
	}
	apps, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (App, error) { return scanApp(r) })
	return apps, mapError(err)
}

// The three store steps of the delete operation (P3.8, app.Deleter). Each
// requires the operation's lease, so a worker that lost it changes nothing.

// holdsDelete locks the delete operation's row and checks that owner still
// runs it for this app.
func (s *Store) holdsDelete(ctx context.Context, appID, opID, owner string) error {
	var one int
	err := s.q.QueryRow(ctx, `SELECT 1 FROM operations
		WHERE id = $1 AND app_id = $2 AND kind = 'delete' AND status = 'running' AND lease_owner = $3
		FOR UPDATE`, opID, appID, owner).Scan(&one)
	if errors.Is(mapError(err), ErrNotFound) {
		return ErrLeaseLost
	}
	return mapError(err)
}

// StopServing takes the app out of service in the database: its routes are
// deleted, its active deployment is superseded, and any deployment still in
// progress (left by a cancelled operation) is cancelled. After it, Caddy's
// next sync drops the app, and the reconciler restores nothing of it. It
// returns the hostnames removed. Repeating it changes nothing.
func (s *Store) StopServing(ctx context.Context, appID, opID, owner string) ([]string, error) {
	var hosts []string
	err := s.InTx(ctx, func(tx *Store) error {
		if err := tx.holdsDelete(ctx, appID, opID, owner); err != nil {
			return err
		}
		rows, err := tx.q.Query(ctx, `DELETE FROM routes WHERE app_id = $1 RETURNING hostname`, appID)
		if err != nil {
			return mapError(err)
		}
		if hosts, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
			return mapError(err)
		}
		if _, err := tx.q.Exec(ctx, `UPDATE deployments SET status = 'superseded', ended_at = now()
			WHERE app_id = $1 AND status = 'active'`, appID); err != nil {
			return mapError(err)
		}
		_, err = tx.q.Exec(ctx, `UPDATE deployments SET status = 'cancelled', ended_at = now()
			WHERE app_id = $1 AND status IN ('queued', 'building', 'starting', 'health_checking', 'switching')`, appID)
		return mapError(err)
	})
	slices.Sort(hosts)
	return hosts, err
}

// Artifacts are what an app's deployments left on the host, as recorded.
type Artifacts struct {
	Deployments []string // every deployment ID: the app's containers carry one
	Images      []string // distinct image IDs
}

// AppArtifacts lists the deployments and images recorded for an app. The
// delete operation removes only containers and images found here: anything
// else on the host is not this installation's (P2.6).
func (s *Store) AppArtifacts(ctx context.Context, appID string) (Artifacts, error) {
	var a Artifacts
	rows, err := s.q.Query(ctx, `SELECT id, coalesce(image_id, '') FROM deployments WHERE app_id = $1 ORDER BY created_at, id`, appID)
	if err != nil {
		return a, mapError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, image string
		if err := rows.Scan(&id, &image); err != nil {
			return a, mapError(err)
		}
		a.Deployments = append(a.Deployments, id)
		if image != "" && !slices.Contains(a.Images, image) {
			a.Images = append(a.Images, image)
		}
	}
	return a, mapError(rows.Err())
}

// FinishAppDelete removes the app row and records the audit event, in one
// transaction. The cascade takes the app's environment, secrets,
// deployments, routes, and operations, the delete operation among them: an
// operation that no longer exists is how a finished delete looks. The audit
// event stays (ADR-0007).
func (s *Store) FinishAppDelete(ctx context.Context, appID, opID, owner string, audit AuditEvent) error {
	return s.InTx(ctx, func(tx *Store) error {
		if err := tx.holdsDelete(ctx, appID, opID, owner); err != nil {
			return err
		}
		if _, err := tx.RecordAudit(ctx, audit); err != nil {
			return err
		}
		return tx.DeleteApp(ctx, appID)
	})
}

// DeleteApp removes an app and, by cascade, its history. It returns
// ErrNotFound when the app does not exist. The API never calls it: it queues
// a delete operation, and FinishAppDelete is that operation's last step.
func (s *Store) DeleteApp(ctx context.Context, id string) error {
	tag, err := s.q.Exec(ctx, `DELETE FROM apps WHERE id = $1`, id)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
