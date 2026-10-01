package backup

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeCaddyRestore struct {
	root  string // records "caddy" in root/calls, as the fake pg_restore does
	entry string // the first entry of the archive it received
	err   error
}

func (c *fakeCaddyRestore) RestoreData(_ context.Context, archive io.Reader) error {
	f, _ := os.OpenFile(filepath.Join(c.root, "calls"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	f.WriteString("caddy\n")
	f.Close()
	if c.err != nil {
		return c.err
	}
	h, err := tar.NewReader(archive).Next()
	if err != nil {
		return err
	}
	c.entry = h.Name
	return nil
}

// restoreFixture takes a backup with the fixture's Job, then prepares a
// Restore of it with a fake pg_restore: a script that records its call, its
// arguments and environment, and what it read, then runs body.
func restoreFixture(t *testing.T, body string) (*fixture, *Restore, *fakeCaddyRestore) {
	t.Helper()
	f := newFixture(t, okDump)
	if err := f.job.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho pg_restore >> " + filepath.Join(f.root, "calls") + "\necho \"$@\" > " + filepath.Join(f.root, "rargs") +
		"\nenv > " + filepath.Join(f.root, "renv") + "\ncat > " + filepath.Join(f.root, "received") + "\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(f.root, "pg_restore"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	caddy := &fakeCaddyRestore{root: f.root}
	return f, &Restore{
		From:          filepath.Join(f.root, "a", "20261001T023000Z"),
		DatabaseURL:   f.job.DatabaseURL,
		PGRestore:     filepath.Join(f.root, "pg_restore"),
		KEKDir:        filepath.Join(f.root, "kek"),
		Caddy:         caddy,
		DatabaseEmpty: func(context.Context) (bool, error) { return true, nil },
		Log:           f.job.Log,
	}, caddy
}

func (f *fixture) calls() string {
	b, _ := os.ReadFile(filepath.Join(f.root, "calls"))
	return strings.Join(strings.Fields(string(b)), ",")
}

// P3.6b: a restore verifies the backup, then puts back Caddy's data, then
// the database: pg_restore reads the dump on standard input, in one
// transaction, with the password in PGPASSWORD only.
func TestRestore(t *testing.T) {
	f, r, caddy := restoreFixture(t, "")
	if err := r.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := f.calls(); got != "caddy,pg_restore" {
		t.Fatalf("order = %s, want caddy then pg_restore", got)
	}
	if caddy.entry != "data/caddy/certs/key.pem" || f.read("received") != "PGDMP-fake-archive" {
		t.Fatalf("caddy got %q, pg_restore got %q", caddy.entry, f.read("received"))
	}
	args := strings.TrimSpace(f.read("rargs"))
	if want := "--single-transaction --no-owner --no-privileges --no-password --dbname=postgres://shipyard@127.0.0.1:5432/shipyard?sslmode=disable"; args != want {
		t.Fatalf("pg_restore args = %s\nwant              %s", args, want)
	}
	if !strings.Contains(f.read("renv"), "PGPASSWORD="+testPassword) || strings.Contains(f.logs.String(), testPassword) {
		t.Fatal("the password is not in PGPASSWORD, or it was logged")
	}

	// With Caddy disabled its data is skipped, with a warning.
	f, r, _ = restoreFixture(t, "")
	r.Caddy = nil
	if err := r.Run(t.Context()); err != nil || f.calls() != "pg_restore" || !strings.Contains(f.logs.String(), "caddy is disabled") {
		t.Fatalf("without caddy: %v, calls %s", err, f.calls())
	}
}

// Everything is checked before anything is written: a damaged or altered
// backup, a missing KEK, or a database that is not empty stops the restore
// with nothing done.
func TestRestoreRefused(t *testing.T) {
	manifest := func(f *fixture, edit func(*Manifest)) {
		t.Helper()
		path := filepath.Join("a", "20261001T023000Z", ManifestName)
		var m Manifest
		if err := json.Unmarshal([]byte(f.read(path)), &m); err != nil {
			t.Fatal(err)
		}
		edit(&m)
		raw, _ := json.Marshal(m)
		f.write(path, string(raw))
	}
	for name, tc := range map[string]struct {
		mutate func(*fixture, *Restore)
		want   string
	}{
		"damaged dump": {func(f *fixture, _ *Restore) { f.write("a/20261001T023000Z/"+DumpName, "PGDMP-fake-archivX") },
			DumpName + " does not match the manifest"},
		"truncated caddy data": {func(f *fixture, _ *Restore) { f.write("a/20261001T023000Z/"+CaddyName, "") },
			CaddyName + " does not match the manifest"},
		"file missing": {func(f *fixture, _ *Restore) { os.Remove(filepath.Join(f.root, "a", "20261001T023000Z", DumpName)) }, DumpName},
		"no manifest":  {func(f *fixture, _ *Restore) { os.Remove(filepath.Join(f.root, "a", "20261001T023000Z", ManifestName)) }, ManifestName},
		"path in manifest": {func(f *fixture, _ *Restore) {
			manifest(f, func(m *Manifest) { m.Files = append(m.Files, File{Name: "../../kek/k1.key"}) })
		}, `unexpected file "../../kek/k1.key"`},
		"no dump in manifest": {func(f *fixture, _ *Restore) { manifest(f, func(m *Manifest) { m.Files = m.Files[1:] }) }, "lists no " + DumpName},
		"newer manifest":      {func(f *fixture, _ *Restore) { manifest(f, func(m *Manifest) { m.Version = 2 }) }, "manifest version 2 is not supported"},
		"path as KEK ID": {func(f *fixture, _ *Restore) {
			manifest(f, func(m *Manifest) { m.KEKIDs = []string{"../../etc/passwd"} })
		}, `invalid KEK ID "../../etc/passwd"`},
		"KEK missing": {func(f *fixture, _ *Restore) { os.Remove(filepath.Join(f.root, "kek", "k2.key")) },
			"lacks the KEKs k2: copy them from the KEK backup (target B) first"},
		"database not empty": {func(_ *fixture, r *Restore) {
			r.DatabaseEmpty = func(context.Context) (bool, error) { return false, nil }
		}, "the database already has tables"},
		"database unreachable": {func(_ *fixture, r *Restore) {
			r.DatabaseEmpty = func(context.Context) (bool, error) { return false, errors.New("connection refused") }
		}, "check the database: connection refused"},
	} {
		t.Run(name, func(t *testing.T) {
			f, r, _ := restoreFixture(t, "")
			tc.mutate(f, r)
			err := r.Run(t.Context())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if got := f.calls(); got != "" {
				t.Fatalf("the restore touched something: %s", got)
			}
		})
	}
}

// A pg_restore failure is reported with its output; the database is last,
// so the run can be repeated. A Caddy failure stops before the database.
func TestRestoreFails(t *testing.T) {
	f, r, _ := restoreFixture(t, `echo 'relation "apps" already exists' >&2; exit 1`)
	err := r.Run(t.Context())
	if err == nil || !strings.Contains(err.Error(), `database: pg_restore: exit status 1: relation "apps" already exists`) ||
		strings.Contains(err.Error(), testPassword) {
		t.Fatalf("err = %v", err)
	}
	if got := f.calls(); got != "caddy,pg_restore" {
		t.Fatalf("calls = %s", got)
	}

	f, r, caddy := restoreFixture(t, "")
	caddy.err = errors.New("container rootfs is marked read-only")
	if err := r.Run(t.Context()); err == nil || !strings.Contains(err.Error(), "caddy data: container rootfs") || f.calls() != "caddy" {
		t.Fatalf("err = %v, calls %s", err, f.calls())
	}
}
