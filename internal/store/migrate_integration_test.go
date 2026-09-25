//go:build integration

package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hami9/shipyard/internal/store"
	"github.com/hami9/shipyard/internal/store/storetest"
	"github.com/hami9/shipyard/migrations"
)

var (
	m1 = store.Migration{Version: 1, Name: "widgets", SQL: `
		CREATE TABLE widgets (id bigint PRIMARY KEY);
		CREATE INDEX widgets_id_idx ON widgets (id);`}
	m2 = store.Migration{Version: 2, Name: "gadgets", SQL: `CREATE TABLE gadgets (id bigint PRIMARY KEY);`}
	m3 = store.Migration{Version: 3, Name: "gizmos", SQL: `CREATE TABLE gizmos (id bigint PRIMARY KEY);`}
)

func openTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := store.Open(t.Context(), storetest.NewDatabase(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func versions(ms []store.Migration) []int {
	out := make([]int, len(ms))
	for i, m := range ms {
		out[i] = m.Version
	}
	return out
}

func tableExists(t *testing.T, db *pgxpool.Pool, name string) bool {
	t.Helper()
	var ok bool
	if err := db.QueryRow(t.Context(), "SELECT to_regclass($1) IS NOT NULL", name).Scan(&ok); err != nil {
		t.Fatalf("check table %s: %v", name, err)
	}
	return ok
}

func TestMigrateAppliesPendingOnce(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()

	applied, err := store.Migrate(ctx, db, []store.Migration{m1, m2})
	if err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if got := versions(applied); len(got) != 2 {
		t.Fatalf("applied %v, want [1 2]", got)
	}
	if !tableExists(t, db, "widgets") || !tableExists(t, db, "widgets_id_idx") || !tableExists(t, db, "gadgets") {
		t.Fatal("multi-statement migration was not fully applied")
	}

	applied, err = store.Migrate(ctx, db, []store.Migration{m1, m2})
	if err != nil || len(applied) != 0 {
		t.Fatalf("second Migrate = %v, %v; want nothing applied", versions(applied), err)
	}

	applied, err = store.Migrate(ctx, db, []store.Migration{m1, m2, m3})
	if err != nil || len(applied) != 1 || applied[0].Version != 3 {
		t.Fatalf("third Migrate = %v, %v; want [3]", versions(applied), err)
	}
}

func TestMigrateFailureRollsBackEverything(t *testing.T) {
	db := openTestDB(t)
	bad := store.Migration{Version: 2, Name: "broken", SQL: `CREATE TABLE ok_table (id int); SELECT * FROM missing_table;`}

	if _, err := store.Migrate(t.Context(), db, []store.Migration{m1, bad}); err == nil {
		t.Fatal("Migrate succeeded, want error")
	}
	if tableExists(t, db, "widgets") || tableExists(t, db, "ok_table") {
		t.Error("partial migration was committed")
	}
	var n int
	if err := db.QueryRow(t.Context(), "SELECT count(*) FROM schema_migrations").Scan(&n); err == nil && n != 0 {
		t.Errorf("schema_migrations has %d rows, want 0", n)
	}
}

func TestMigrateRefusesNewerSchema(t *testing.T) {
	db := openTestDB(t)
	if _, err := store.Migrate(t.Context(), db, []store.Migration{m1, m2}); err != nil {
		t.Fatal(err)
	}
	_, err := store.Migrate(t.Context(), db, []store.Migration{m1})
	if !errors.Is(err, store.ErrUnknownMigration) {
		t.Fatalf("err = %v, want ErrUnknownMigration", err)
	}
}

func TestMigrateRefusesRenamedMigration(t *testing.T) {
	db := openTestDB(t)
	if _, err := store.Migrate(t.Context(), db, []store.Migration{m1}); err != nil {
		t.Fatal(err)
	}
	renamed := m1
	renamed.Name = "renamed"
	if _, err := store.Migrate(t.Context(), db, []store.Migration{renamed}); err == nil {
		t.Fatal("Migrate accepted a renamed migration")
	}
}

func TestMigrateConcurrentCallersApplyOnce(t *testing.T) {
	db := openTestDB(t)
	const callers = 5
	var wg sync.WaitGroup
	results := make(chan int, callers)
	errs := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			applied, err := store.Migrate(context.Background(), db, []store.Migration{m1, m2, m3})
			if err != nil {
				errs <- err
				return
			}
			results <- len(applied)
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Errorf("concurrent Migrate: %v", err)
	}
	total := 0
	for n := range results {
		total += n
	}
	if total != 3 {
		t.Errorf("migrations applied %d times in total, want 3", total)
	}
}

func TestEmbeddedMigrationsApply(t *testing.T) {
	db := openTestDB(t)
	ms, err := store.LoadMigrations(migrations.FS)
	if err != nil {
		t.Fatalf("LoadMigrations: %v", err)
	}
	if _, err := store.Migrate(t.Context(), db, ms); err != nil {
		t.Fatalf("Migrate embedded: %v", err)
	}
}
