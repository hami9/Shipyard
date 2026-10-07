//go:build integration

package secrets_test

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hami9/shipyard/internal/secrets"
	"github.com/hami9/shipyard/internal/store"
	"github.com/hami9/shipyard/internal/store/storetest"
	"github.com/hami9/shipyard/migrations"
)

func ptr[T any](v T) *T { return &v }

type fixture struct {
	pool *pgxpool.Pool
	st   *store.Store
	app  string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := t.Context()
	pool, err := store.Open(ctx, storetest.NewDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ms, err := store.LoadMigrations(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Migrate(ctx, pool, ms); err != nil {
		t.Fatal(err)
	}
	st := store.New(pool)
	u, err := st.CreateUser(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	app, err := st.CreateApp(ctx, store.NewApp{OwnerID: u.ID, Slug: "web", RepoFullName: "hami9/demo",
		AppSettings: store.AppSettings{Branch: ptr("main"), InternalPort: ptr(3000)}})
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{pool: pool, st: st, app: app.ID}
}

func keyring(t *testing.T, active string, keys map[string][]byte) *secrets.Keyring {
	t.Helper()
	k, err := secrets.NewKeyring(active, keys)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestEnvRevisions(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	env := secrets.NewEnv(keyring(t, "k1", map[string][]byte{"k1": secrets.GenerateKey()}), f.st)

	r1, err := env.Set(ctx, f.app, "DATABASE_URL", []byte("postgres://u:hunter2@db/app"), true)
	if err != nil || r1.Number != 1 {
		t.Fatalf("Set 1 = %+v, %v", r1, err)
	}
	r2, err := env.Set(ctx, f.app, "LOG_LEVEL", []byte("info"), false)
	if err != nil || r2.Number != 2 || len(r2.Entries) != 2 {
		t.Fatalf("Set 2 = %+v, %v", r2, err)
	}
	// The untouched secret is reused by reference, not re-encrypted.
	if *r2.Entries[0].SecretValueID != *r1.Entries[0].SecretValueID {
		t.Fatal("unchanged secret was copied into a new row")
	}
	r3, err := env.Set(ctx, f.app, "DATABASE_URL", []byte("postgres://u:rotated@db/app"), true)
	if err != nil || r3.Number != 3 {
		t.Fatalf("Set 3 = %+v, %v", r3, err)
	}

	got, err := env.Resolve(ctx, r3.ID)
	if err != nil || got["DATABASE_URL"] != "postgres://u:rotated@db/app" || got["LOG_LEVEL"] != "info" || len(got) != 2 {
		t.Fatalf("Resolve r3 = %v, %v", got, err)
	}
	// Only secret values are returned for redaction; LOG_LEVEL is plain.
	if vals, err := env.SecretValues(ctx, r3.ID); err != nil || len(vals) != 1 || vals[0] != "postgres://u:rotated@db/app" {
		t.Fatalf("SecretValues r3 = %d values, %v", len(vals), err)
	}
	// Revisions are immutable: r2 still holds the old secret (rollback, ADR-0005).
	old, err := env.Resolve(ctx, r2.ID)
	if err != nil || old["DATABASE_URL"] != "postgres://u:hunter2@db/app" {
		t.Fatalf("Resolve r2 = %v, %v", old, err)
	}

	n, vars, err := env.Keys(ctx, f.app)
	if err != nil || n != 3 || fmt.Sprint(vars) != "[{DATABASE_URL true} {LOG_LEVEL false}]" {
		t.Fatalf("Keys = %d %v %v", n, vars, err)
	}

	r4, err := env.Unset(ctx, f.app, "LOG_LEVEL")
	if err != nil || r4.Number != 4 || len(r4.Entries) != 1 {
		t.Fatalf("Unset = %+v, %v", r4, err)
	}
	if _, err := env.Unset(ctx, f.app, "LOG_LEVEL"); !errors.Is(err, secrets.ErrUnknownKey) {
		t.Fatalf("Unset missing key = %v", err)
	}
}

func TestEnvRejects(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	env := secrets.NewEnv(keyring(t, "k1", map[string][]byte{"k1": secrets.GenerateKey()}), f.st)

	for name, v := range map[string][]byte{"NUL": []byte("a\x00b"), "oversize": make([]byte, secrets.MaxValueSize+1)} {
		if _, err := env.Set(ctx, f.app, "K", v, true); !errors.Is(err, secrets.ErrInvalidValue) {
			t.Errorf("%s value: %v", name, err)
		}
	}
	if _, err := env.Set(ctx, f.app, "BAD-KEY", []byte("v"), true); !errors.Is(err, store.ErrInvalid) {
		t.Errorf("invalid key: %v", err)
	}
	if _, err := env.Set(ctx, "00000000-0000-4000-8000-000000000000", "K", []byte("v"), true); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown app: %v", err)
	}
	if n, vars, err := env.Keys(ctx, f.app); err != nil || n != 0 || len(vars) != 0 {
		t.Errorf("failed Sets left a revision: %d %v %v", n, vars, err)
	}
}

// Setting a key must not decrypt the others: an Env whose keyring cannot
// open the existing secret can still add a key, and the old secret stays
// intact for a keyring that can.
func TestSetDoesNotDecrypt(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	k1, k2 := secrets.GenerateKey(), secrets.GenerateKey()
	before := secrets.NewEnv(keyring(t, "k1", map[string][]byte{"k1": k1}), f.st)
	if _, err := before.Set(ctx, f.app, "OLD", []byte("sealed-by-k1"), true); err != nil {
		t.Fatal(err)
	}
	blind := secrets.NewEnv(keyring(t, "k2", map[string][]byte{"k2": k2}), f.st)
	rev, err := blind.Set(ctx, f.app, "NEW", []byte("sealed-by-k2"), true)
	if err != nil {
		t.Fatalf("Set without the old KEK: %v", err)
	}
	if _, err := blind.Resolve(ctx, rev.ID); !errors.Is(err, secrets.ErrDecrypt) {
		t.Fatalf("Resolve without k1 = %v, want ErrDecrypt", err)
	}
	both := secrets.NewEnv(keyring(t, "k2", map[string][]byte{"k1": k1, "k2": k2}), f.st)
	got, err := both.Resolve(ctx, rev.ID)
	if err != nil || got["OLD"] != "sealed-by-k1" || got["NEW"] != "sealed-by-k2" {
		t.Fatalf("Resolve with both KEKs = %v, %v", got, err)
	}
}

// Concurrent writers get consecutive revision numbers and lose no keys.
func TestConcurrentSet(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	env := secrets.NewEnv(keyring(t, "k1", map[string][]byte{"k1": secrets.GenerateKey()}), f.st)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := env.Set(ctx, f.app, fmt.Sprintf("K%d", i), []byte("v"), i%2 == 0)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Set: %v", err)
		}
	}
	n, vars, err := env.Keys(ctx, f.app)
	if err != nil || n != 8 || len(vars) != 8 {
		t.Fatalf("after 8 concurrent Sets: revision %d with %d keys (%v)", n, len(vars), err)
	}
}

// A dump of every table contains no secret plaintext, raw or hex-encoded,
// while non-secret values are stored as given.
func TestDatabaseDumpHasNoPlaintext(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	env := secrets.NewEnv(keyring(t, "k1", map[string][]byte{"k1": secrets.GenerateKey()}), f.st)
	const secret, plain = "canary-secret-7f3a9c", "canary-plain-2b8e1d"
	if _, err := env.Set(ctx, f.app, "API_KEY", []byte(secret), true); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Set(ctx, f.app, "MODE", []byte(plain), false); err != nil {
		t.Fatal(err)
	}

	rows, err := f.pool.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname = 'public' ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	var dump strings.Builder
	for _, table := range tables {
		rows, err := f.pool.Query(ctx, `SELECT row_to_json(t)::text FROM `+pgx.Identifier{table}.Sanitize()+` t`)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			dump.WriteString(line + "\n")
		}
	}
	d := dump.String()
	if !strings.Contains(d, `"kek_id":"k1"`) {
		t.Fatalf("dump did not include secret_values rows (tables %v)", tables)
	}
	for _, leak := range []string{secret, hex.EncodeToString([]byte(secret))} {
		if strings.Contains(d, leak) {
			t.Fatalf("secret plaintext %q found in the database dump", leak)
		}
	}
	if !strings.Contains(d, plain) {
		t.Fatal("plain value missing from dump: the check is not seeing entry rows")
	}
}
