package store

import (
	"context"
	"fmt"
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

// DeleteApp removes an app and, by cascade, its history. It returns
// ErrNotFound when the app does not exist.
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
