//go:build integration

package main

import (
	"bytes"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/hami9/shipyard/internal/secrets"
	"github.com/hami9/shipyard/internal/store"
	"github.com/hami9/shipyard/internal/store/storetest"
	"github.com/hami9/shipyard/migrations"
)

// ADR-0012: the KEK rotation procedure end to end against PostgreSQL. Values
// sealed under k1 are moved to k2, open with k2 alone, and keep their rows
// and revisions; a KEK that wraps values but is not loaded stops the run.
func TestKEKRotation(t *testing.T) {
	ctx := t.Context()
	pool, err := store.Open(ctx, storetest.NewDatabase(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	ms, _ := store.LoadMigrations(migrations.FS)
	if _, err := store.Migrate(ctx, pool, ms); err != nil {
		t.Fatal(err)
	}
	s := store.New(pool)
	u, _ := s.CreateUser(ctx, "admin")
	branch, port := "main", 3000
	app, err := s.CreateApp(ctx, store.NewApp{OwnerID: u.ID, Slug: "web", RepoFullName: "hami9/demo",
		AppSettings: store.AppSettings{Branch: &branch, InternalPort: &port}})
	if err != nil {
		t.Fatal(err)
	}
	k1, k2 := secrets.GenerateKey(), secrets.GenerateKey()
	ring := func(active string, keys map[string][]byte) *secrets.Keyring {
		t.Helper()
		k, err := secrets.NewKeyring(active, keys)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	log := slog.New(slog.DiscardHandler)

	// Under k1: three secrets (one overwritten, so two revisions share a
	// row) and a plain value.
	old := secrets.NewEnv(ring("k1", map[string][]byte{"k1": k1}), s)
	for _, kv := range [][2]string{{"DB", "postgres://a"}, {"TOKEN", "t-1"}, {"TOKEN", "t-2"}} {
		if _, err := old.Set(ctx, app.ID, kv[0], []byte(kv[1]), true); err != nil {
			t.Fatal(err)
		}
	}
	rev, err := old.Set(ctx, app.ID, "MODE", []byte("prod"), false)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := s.EnvRevisionByID(ctx, rev.ID)

	// k2 active, both loaded: the API seals new values with k2 meanwhile.
	both := ring("k2", map[string][]byte{"k1": k1, "k2": k2})
	if _, err := secrets.NewEnv(both, s).Set(ctx, app.ID, "NEW", []byte("n"), true); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := kekStatus(ctx, s, both, &out); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`k1\s+3\s+run .kek rewrap.`).MatchString(out.String()) || !regexp.MustCompile(`k2\s+1\s+active`).MatchString(out.String()) {
		t.Fatalf("status before:\n%s", out.String())
	}

	moved, err := kekRewrap(ctx, s, both, log)
	if err != nil || moved != 3 {
		t.Fatalf("rewrap = %d, %v; want 3", moved, err)
	}
	if usage, _ := s.KEKUsage(ctx); usage["k1"] != 0 || usage["k2"] != 4 {
		t.Fatalf("usage after rewrap = %v", usage)
	}
	// The latest revision, and the one before, open with k2 alone.
	onlyK2 := secrets.NewEnv(ring("k2", map[string][]byte{"k2": k2}), s)
	env, err := onlyK2.Resolve(ctx, rev.ID)
	if err != nil || env["DB"] != "postgres://a" || env["TOKEN"] != "t-2" || env["MODE"] != "prod" {
		t.Fatalf("resolve after rewrap = %v, %v", env, err)
	}
	if after, _ := s.EnvRevisionByID(ctx, rev.ID); !equalEntries(before.Entries, after.Entries) {
		t.Fatalf("revision entries changed: %+v -> %+v", before.Entries, after.Entries)
	}
	out.Reset()
	kekStatus(ctx, s, both, &out)
	if !regexp.MustCompile(`k1\s+0\s+unused: may be retired`).MatchString(out.String()) {
		t.Fatalf("status after:\n%s", out.String())
	}
	if moved, err := kekRewrap(ctx, s, both, log); err != nil || moved != 0 {
		t.Fatalf("second rewrap = %d, %v; want 0", moved, err)
	}
	events, _ := s.AuditEvents(ctx, 1)
	if events[0].Actor != kekActor || events[0].Action != "kek.rewrap" || events[0].Target != "0 values to k2" {
		t.Fatalf("audit = %+v", events[0])
	}

	// Negative: k3 wraps a value but is not loaded; nothing moves.
	k3 := secrets.GenerateKey()
	if _, err := secrets.NewEnv(ring("k3", map[string][]byte{"k3": k3}), s).Set(ctx, app.ID, "LATE", []byte("x"), true); err != nil {
		t.Fatal(err)
	}
	if _, err := kekRewrap(ctx, s, both, log); err == nil || !strings.Contains(err.Error(), "KEKs k3 wrap values but are not in the KEK directory") {
		t.Fatalf("rewrap with k3 missing = %v", err)
	}
	if usage, _ := s.KEKUsage(ctx); usage["k3"] != 1 {
		t.Fatalf("usage = %v", usage)
	}
	out.Reset()
	kekStatus(ctx, s, both, &out)
	if !strings.Contains(out.String(), "NOT LOADED") {
		t.Fatalf("status with k3 missing:\n%s", out.String())
	}

	// Negative: a corrupt wrapping stops the pass with the value's ID.
	all := ring("k2", map[string][]byte{"k1": k1, "k2": k2, "k3": k3})
	if _, err := pool.Exec(ctx, `UPDATE secret_values SET wrapped_dek = '\x00ff', kek_id = 'k1' WHERE kek_id = 'k3'`); err != nil {
		t.Fatal(err)
	}
	if _, err := kekRewrap(ctx, s, all, log); err == nil || !strings.Contains(err.Error(), "cannot be decrypted") {
		t.Fatalf("rewrap of a corrupt value = %v", err)
	}
}

func equalEntries(a, b []store.EnvEntry) bool {
	same := func(x, y *string) bool { return x == nil && y == nil || x != nil && y != nil && *x == *y }
	return len(a) > 0 && slices.EqualFunc(a, b, func(x, y store.EnvEntry) bool {
		return x.Key == y.Key && same(x.SecretValueID, y.SecretValueID) && same(x.PlainValue, y.PlainValue)
	})
}
