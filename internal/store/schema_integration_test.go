//go:build integration

package store_test

import (
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hami9/shipyard/internal/store"
	"github.com/hami9/shipyard/migrations"
)

// SQLSTATE codes the schema is expected to raise.
const (
	uniqueViolation   = "23505"
	checkViolation    = "23514"
	foreignKeyViolate = "23503"
	immutableRow      = "SY001" // shipyard_reject_change()
)

var (
	sha1       = strings.Repeat("0123456789", 4)
	imageID    = "sha256:" + strings.Repeat("ab", 32)
	containerA = strings.Repeat("cd", 32)
)

// schemaDB returns a fresh database with every embedded migration applied.
func schemaDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	db := openTestDB(t)
	ms, err := store.LoadMigrations(migrations.FS)
	if err != nil {
		t.Fatalf("LoadMigrations: %v", err)
	}
	if _, err := store.Migrate(t.Context(), db, ms); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return db
}

func sqlState(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func exec(t *testing.T, db *pgxpool.Pool, sql string, args ...any) error {
	t.Helper()
	_, err := db.Exec(t.Context(), sql, args...)
	return err
}

func mustExec(t *testing.T, db *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if err := exec(t, db, sql, args...); err != nil {
		t.Fatalf("%s: %v", strings.TrimSpace(sql), err)
	}
}

func wantState(t *testing.T, err error, state string) {
	t.Helper()
	if got := sqlState(err); got != state {
		t.Fatalf("got error %v (SQLSTATE %q), want SQLSTATE %s", err, got, state)
	}
}

func insertID(t *testing.T, db *pgxpool.Pool, sql string, args ...any) string {
	t.Helper()
	var id string
	if err := db.QueryRow(t.Context(), sql, args...).Scan(&id); err != nil {
		t.Fatalf("%s: %v", strings.TrimSpace(sql), err)
	}
	return id
}

func uniq() string { return strings.ToLower(rand.Text()[:10]) }

// fixture creates rows with valid defaults, so each subtest states only what
// it is checking.
type fixture struct {
	t    *testing.T
	db   *pgxpool.Pool
	user string
}

func newFixture(t *testing.T, db *pgxpool.Pool) *fixture {
	user := insertID(t, db, `INSERT INTO users (name) VALUES ($1) RETURNING id::text`, "u"+uniq())
	return &fixture{t: t, db: db, user: user}
}

func (f *fixture) app() string {
	return insertID(f.t, f.db, `
		INSERT INTO apps (owner_id, slug, repo_full_name, branch, internal_port)
		VALUES ($1, $2, 'hami9/demo', 'main', 3000) RETURNING id::text`, f.user, "a"+uniq())
}

func (f *fixture) operation(app, status string) string {
	lease, expires := "", "NULL"
	if status == "running" {
		lease, expires = "worker-1", "now() + interval '1 minute'"
	}
	return insertID(f.t, f.db, `
		INSERT INTO operations (app_id, kind, idempotency_key, status, lease_owner, lease_expires_at)
		VALUES ($1, 'deploy', $2, $3, NULLIF($4, ''), `+expires+`) RETURNING id::text`,
		app, "k-"+uniq(), status, lease)
}

func (f *fixture) deployment(app, status string) string {
	op := f.operation(app, "queued")
	var img, ctr, reason any
	switch status {
	case "health_checking", "switching", "active", "superseded":
		img, ctr = imageID, containerA
	case "failed":
		reason = "health check failed"
	}
	return insertID(f.t, f.db, `
		INSERT INTO deployments (app_id, operation_id, kind, source_commit_sha, status, image_id, container_id, failure_reason)
		VALUES ($1, $2, 'build', $3, $4, $5, $6, $7) RETURNING id::text`,
		app, op, sha1, status, img, ctr, reason)
}

func (f *fixture) secret(app, key string) string {
	return insertID(f.t, f.db, `
		INSERT INTO secret_values (app_id, key, ciphertext, wrapped_dek, kek_id)
		VALUES ($1, $2, '\x01', '\x02', 'kek1') RETURNING id::text`, app, key)
}

func (f *fixture) revision(app string, number int) string {
	return insertID(f.t, f.db, `
		INSERT INTO env_revisions (app_id, number) VALUES ($1, $2) RETURNING id::text`, app, number)
}

func TestSchemaOperations(t *testing.T) {
	db := schemaDB(t)
	f := newFixture(t, db)

	t.Run("idempotency key is globally unique", func(t *testing.T) {
		a, b := f.app(), f.app()
		mustExec(t, db, `INSERT INTO operations (app_id, kind, idempotency_key) VALUES ($1, 'deploy', 'gh:dup')`, a)
		wantState(t, exec(t, db, `INSERT INTO operations (app_id, kind, idempotency_key) VALUES ($1, 'deploy', 'gh:dup')`, b), uniqueViolation)
	})

	t.Run("one running operation per app", func(t *testing.T) {
		a, b := f.app(), f.app()
		f.operation(a, "running")
		f.operation(a, "queued")  // queued ones may pile up
		f.operation(b, "running") // other apps are independent
		wantState(t, exec(t, db, `
			INSERT INTO operations (app_id, kind, idempotency_key, status, lease_owner, lease_expires_at)
			VALUES ($1, 'deploy', $2, 'running', 'worker-2', now())`, a, "k-"+uniq()), uniqueViolation)
	})

	t.Run("running operation needs a lease", func(t *testing.T) {
		wantState(t, exec(t, db, `
			INSERT INTO operations (app_id, kind, idempotency_key, status) VALUES ($1, 'deploy', $2, 'running')`,
			f.app(), "k-"+uniq()), checkViolation)
	})

	t.Run("finished_at matches terminal status", func(t *testing.T) {
		op := f.operation(f.app(), "queued")
		wantState(t, exec(t, db, `UPDATE operations SET status = 'succeeded' WHERE id = $1`, op), checkViolation)
		mustExec(t, db, `UPDATE operations SET status = 'succeeded', finished_at = now() WHERE id = $1`, op)
	})

	t.Run("operation events are append-only", func(t *testing.T) {
		op := f.operation(f.app(), "queued")
		mustExec(t, db, `INSERT INTO operation_events (operation_id, seq, level, message) VALUES ($1, 1, 'info', 'building')`, op)
		wantState(t, exec(t, db, `INSERT INTO operation_events (operation_id, seq, level, message) VALUES ($1, 1, 'info', 'again')`, op), uniqueViolation)
		wantState(t, exec(t, db, `UPDATE operation_events SET message = 'x' WHERE operation_id = $1`, op), immutableRow)
	})
}

func TestSchemaDeploymentsAndRoutes(t *testing.T) {
	db := schemaDB(t)
	f := newFixture(t, db)

	t.Run("one active deployment per app", func(t *testing.T) {
		a := f.app()
		f.deployment(a, "active")
		f.deployment(a, "superseded")
		f.deployment(f.app(), "active")
		wantState(t, exec(t, db, `
			INSERT INTO deployments (app_id, operation_id, kind, source_commit_sha, status, image_id, container_id)
			VALUES ($1, $2, 'build', $3, 'active', $4, $5)`, a, f.operation(a, "queued"), sha1, imageID, containerA), uniqueViolation)
	})

	t.Run("serving states need image and container", func(t *testing.T) {
		d := f.deployment(f.app(), "starting")
		wantState(t, exec(t, db, `UPDATE deployments SET status = 'active' WHERE id = $1`, d), checkViolation)
	})

	t.Run("failed needs a reason", func(t *testing.T) {
		d := f.deployment(f.app(), "building")
		wantState(t, exec(t, db, `UPDATE deployments SET status = 'failed' WHERE id = $1`, d), checkViolation)
	})

	t.Run("rollback needs a source deployment of the same app", func(t *testing.T) {
		a := f.app()
		other := f.deployment(f.app(), "superseded")
		insert := `INSERT INTO deployments (app_id, operation_id, kind, source_commit_sha, source_deployment_id)
			VALUES ($1, $2, 'rollback', $3, $4)`
		wantState(t, exec(t, db, insert, a, f.operation(a, "queued"), sha1, nil), checkViolation)
		wantState(t, exec(t, db, insert, a, f.operation(a, "queued"), sha1, other), foreignKeyViolate)
		mustExec(t, db, insert, a, f.operation(a, "queued"), sha1, f.deployment(a, "superseded"))
	})

	t.Run("deployment and operation must share the app", func(t *testing.T) {
		wantState(t, exec(t, db, `
			INSERT INTO deployments (app_id, operation_id, kind, source_commit_sha) VALUES ($1, $2, 'build', $3)`,
			f.app(), f.operation(f.app(), "queued"), sha1), foreignKeyViolate)
	})

	t.Run("deployment cannot use another app's env revision", func(t *testing.T) {
		a := f.app()
		wantState(t, exec(t, db, `
			INSERT INTO deployments (app_id, operation_id, kind, source_commit_sha, env_revision_id)
			VALUES ($1, $2, 'build', $3, $4)`, a, f.operation(a, "queued"), sha1, f.revision(f.app(), 1)), foreignKeyViolate)
	})

	t.Run("hostname is unique and lowercase", func(t *testing.T) {
		mustExec(t, db, `INSERT INTO routes (hostname, app_id) VALUES ('app.example.com', $1)`, f.app())
		wantState(t, exec(t, db, `INSERT INTO routes (hostname, app_id) VALUES ('app.example.com', $1)`, f.app()), uniqueViolation)
		for _, h := range []string{"App.Example.com", "localhost", "-a.example.com", "a..example.com", "a.example.com."} {
			wantState(t, exec(t, db, `INSERT INTO routes (hostname, app_id) VALUES ($1, $2)`, h, f.app()), checkViolation)
		}
	})

	t.Run("route points only at its own app's deployment", func(t *testing.T) {
		a := f.app()
		wantState(t, exec(t, db, `
			INSERT INTO routes (hostname, app_id, deployment_id, upstream) VALUES ($1, $2, $3, '172.18.0.2:3000')`,
			uniq()+".example.com", a, f.deployment(f.app(), "active")), foreignKeyViolate)
		mustExec(t, db, `
			INSERT INTO routes (hostname, app_id, deployment_id, upstream) VALUES ($1, $2, $3, '172.18.0.2:3000')`,
			uniq()+".example.com", a, f.deployment(a, "active"))
	})
}

func TestSchemaApps(t *testing.T) {
	db := schemaDB(t)
	f := newFixture(t, db)
	insert := `INSERT INTO apps (owner_id, slug, repo_full_name, branch, internal_port, dockerfile_path, build_context)
		VALUES ($1, $2, 'hami9/demo', 'main', 3000, $3, $4)`

	valid := []struct{ slug, dockerfile, context string }{
		{"web", "Dockerfile", "."},
		{"api-" + uniq(), "docker/prod.Dockerfile", "services/api"},
		{"x" + uniq(), "..hidden/Dockerfile", "./app"},
	}
	for _, c := range valid {
		mustExec(t, db, insert, f.user, c.slug, c.dockerfile, c.context)
	}

	invalid := map[string]struct{ slug, dockerfile, context string }{
		"uppercase slug":      {"Web", "Dockerfile", "."},
		"slug with _":         {"my_app", "Dockerfile", "."},
		"slug leading dash":   {"-web", "Dockerfile", "."},
		"slug trailing dash":  {"web-", "Dockerfile", "."},
		"slug over 40":        {"a" + strings.Repeat("b", 40), "Dockerfile", "."},
		"absolute dockerfile": {"a" + uniq(), "/etc/passwd", "."},
		"dockerfile escapes":  {"a" + uniq(), "../Dockerfile", "."},
		"nested escape":       {"a" + uniq(), "a/../../Dockerfile", "."},
		"context escapes":     {"a" + uniq(), "Dockerfile", ".."},
		"empty context":       {"a" + uniq(), "Dockerfile", ""},
	}
	for name, c := range invalid {
		t.Run(name, func(t *testing.T) {
			wantState(t, exec(t, db, insert, f.user, c.slug, c.dockerfile, c.context), checkViolation)
		})
	}

	t.Run("slug is unique", func(t *testing.T) {
		wantState(t, exec(t, db, insert, f.user, "web", "Dockerfile", "."), uniqueViolation)
	})

	t.Run("resource limits", func(t *testing.T) {
		a := f.app()
		wantState(t, exec(t, db, `UPDATE apps SET memory_limit = 1048576 WHERE id = $1`, a), checkViolation)
		wantState(t, exec(t, db, `UPDATE apps SET internal_port = 0 WHERE id = $1`, a), checkViolation)
		wantState(t, exec(t, db, `UPDATE apps SET health_path = 'health' WHERE id = $1`, a), checkViolation)
		mustExec(t, db, `UPDATE apps SET cpu_limit = 0.5, memory_limit = 6291456 WHERE id = $1`, a)
	})

	t.Run("updated_at follows updates", func(t *testing.T) {
		a := f.app()
		mustExec(t, db, `UPDATE apps SET branch = 'release' WHERE id = $1`, a)
		var bumped bool
		if err := db.QueryRow(t.Context(), `SELECT updated_at > created_at FROM apps WHERE id = $1`, a).Scan(&bumped); err != nil || !bumped {
			t.Fatalf("updated_at not bumped (bumped=%v, err=%v)", bumped, err)
		}
	})
}

func TestSchemaSecretsAndAudit(t *testing.T) {
	db := schemaDB(t)
	f := newFixture(t, db)
	entry := `INSERT INTO env_revision_entries (revision_id, app_id, key, secret_value_id, plain_value) VALUES ($1, $2, $3, $4, $5)`

	t.Run("entry holds exactly one value", func(t *testing.T) {
		a := f.app()
		rev := f.revision(a, 1)
		wantState(t, exec(t, db, entry, rev, a, "EMPTY", nil, nil), checkViolation)
		wantState(t, exec(t, db, entry, rev, a, "DB_URL", f.secret(a, "DB_URL"), "plain"), checkViolation)
		mustExec(t, db, entry, rev, a, "DB_URL", f.secret(a, "DB_URL"), nil)
		mustExec(t, db, entry, rev, a, "LOG_LEVEL", nil, "info")
	})

	t.Run("entry cannot use another app's secret or another key's secret", func(t *testing.T) {
		a := f.app()
		rev := f.revision(a, 1)
		wantState(t, exec(t, db, entry, rev, a, "TOKEN", f.secret(f.app(), "TOKEN"), nil), foreignKeyViolate)
		wantState(t, exec(t, db, entry, rev, a, "TOKEN", f.secret(a, "OTHER"), nil), foreignKeyViolate)
	})

	t.Run("revision numbers are unique per app", func(t *testing.T) {
		a := f.app()
		f.revision(a, 1)
		f.revision(f.app(), 1)
		wantState(t, exec(t, db, `INSERT INTO env_revisions (app_id, number) VALUES ($1, 1)`, a), uniqueViolation)
	})

	t.Run("secrets and revisions are immutable", func(t *testing.T) {
		a := f.app()
		s := f.secret(a, "KEY")
		rev := f.revision(a, 1)
		mustExec(t, db, entry, rev, a, "KEY", s, nil)
		wantState(t, exec(t, db, `UPDATE secret_values SET kek_id = 'kek2' WHERE id = $1`, s), immutableRow)
		wantState(t, exec(t, db, `UPDATE env_revisions SET number = 2 WHERE id = $1`, rev), immutableRow)
		wantState(t, exec(t, db, `UPDATE env_revision_entries SET key = 'KEY2' WHERE revision_id = $1`, rev), immutableRow)
	})

	t.Run("deleting an app cascades through immutable rows", func(t *testing.T) {
		a := f.app()
		rev := f.revision(a, 1)
		mustExec(t, db, entry, rev, a, "KEY", f.secret(a, "KEY"), nil)
		f.deployment(a, "failed")
		mustExec(t, db, `DELETE FROM apps WHERE id = $1`, a)
		var n int
		if err := db.QueryRow(t.Context(), `SELECT count(*) FROM secret_values WHERE app_id = $1`, a).Scan(&n); err != nil || n != 0 {
			t.Fatalf("secret rows left after app delete: %d (err=%v)", n, err)
		}
	})

	t.Run("token hash is 32 bytes and unique", func(t *testing.T) {
		insert := `INSERT INTO api_tokens (user_id, name, prefix, sha256_hash, scopes) VALUES ($1, 'cli', 'shp_abcd', $2, '{admin}')`
		hash := make([]byte, 32)
		rand.Read(hash)
		mustExec(t, db, insert, f.user, hash)
		wantState(t, exec(t, db, insert, f.user, hash), uniqueViolation)
		wantState(t, exec(t, db, insert, f.user, hash[:16]), checkViolation)
	})

	t.Run("audit events are append-only and never deleted", func(t *testing.T) {
		id := insertID(t, db, `INSERT INTO audit_events (actor, action, target, result) VALUES ('token:shp_abcd', 'app.create', 'web', 'success') RETURNING id::text`)
		wantState(t, exec(t, db, `UPDATE audit_events SET result = 'denied' WHERE id = $1`, id), immutableRow)
		wantState(t, exec(t, db, `DELETE FROM audit_events WHERE id = $1`, id), immutableRow)
	})
}
