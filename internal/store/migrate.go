package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Migration is one forward-only schema change, loaded from a file named
// NNNN_snake_case_name.sql. Applied migrations are never edited; a change
// is always a new file.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

var migrationName = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.sql$`)

// LoadMigrations reads every .sql file at the root of fsys. Versions must be
// unique and contiguous from 1, which catches merge mistakes before they reach
// a database.
func LoadMigrations(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	var out []Migration
	for _, e := range entries {
		if e.IsDir() || path.Ext(e.Name()) != ".sql" {
			continue
		}
		m := migrationName.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("migration %q: name must match NNNN_snake_case.sql", e.Name())
		}
		version, _ := strconv.Atoi(m[1])
		body, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", e.Name(), err)
		}
		if strings.TrimSpace(string(body)) == "" {
			return nil, fmt.Errorf("migration %q is empty", e.Name())
		}
		out = append(out, Migration{Version: version, Name: m[2], SQL: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	for i, m := range out {
		if m.Version != i+1 {
			return nil, fmt.Errorf("migration versions must be contiguous from 0001: expected %04d, found %04d_%s", i+1, m.Version, m.Name)
		}
	}
	return out, nil
}

// migrationLockKey is the advisory lock that serializes concurrent migrators
// ("SHIPYARD" in ASCII).
const migrationLockKey int64 = 0x5348495059415244

// ErrUnknownMigration means the database has a migration this binary does not
// know about, i.e. an older binary is running against a newer schema.
var ErrUnknownMigration = errors.New("database schema is newer than this binary")

// TxBeginner is satisfied by *pgxpool.Pool and *pgx.Conn.
type TxBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Migrate applies every pending migration in one transaction and returns the
// ones it applied. Either all pending migrations commit or none do.
//
// Concurrent callers are serialized with a transaction-scoped advisory lock,
// which is released automatically at commit or rollback; it is held only for
// the duration of the migration, not across long work (ADR-0002).
func Migrate(ctx context.Context, db TxBeginner, migrations []Migration) (applied []Migration, err error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin migration: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(ctx)
		}
	}()

	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationLockKey); err != nil {
		return nil, fmt.Errorf("acquire migration lock: %w", err)
	}
	if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    integer     PRIMARY KEY,
		name       text        NOT NULL,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return nil, fmt.Errorf("create schema_migrations: %w", err)
	}

	done, err := appliedVersions(ctx, tx)
	if err != nil {
		return nil, err
	}
	known := make(map[int]Migration, len(migrations))
	for _, m := range migrations {
		known[m.Version] = m
	}
	for v, name := range done {
		m, ok := known[v]
		if !ok {
			return nil, fmt.Errorf("%w: applied migration %04d_%s is unknown", ErrUnknownMigration, v, name)
		}
		if m.Name != name {
			return nil, fmt.Errorf("migration %04d was applied as %q but is now named %q; never rename applied migrations", v, name, m.Name)
		}
	}

	for _, m := range migrations {
		if _, ok := done[m.Version]; ok {
			continue
		}
		// The simple query protocol allows several statements per file.
		if _, err := tx.Conn().PgConn().Exec(ctx, m.SQL).ReadAll(); err != nil {
			return nil, fmt.Errorf("apply migration %04d_%s: %w", m.Version, m.Name, err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version, name) VALUES ($1, $2)", m.Version, m.Name); err != nil {
			return nil, fmt.Errorf("record migration %04d_%s: %w", m.Version, m.Name, err)
		}
		applied = append(applied, m)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit migrations: %w", err)
	}
	return applied, nil
}

func appliedVersions(ctx context.Context, tx pgx.Tx) (map[int]string, error) {
	rows, err := tx.Query(ctx, "SELECT version, name FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()
	done := map[int]string{}
	for rows.Next() {
		var v int
		var name string
		if err := rows.Scan(&v, &name); err != nil {
			return nil, fmt.Errorf("scan schema_migrations: %w", err)
		}
		done[v] = name
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	return done, nil
}
