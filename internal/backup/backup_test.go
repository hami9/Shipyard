package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

type fakeCaddy struct{ err error }

func (c fakeCaddy) ArchiveData(_ context.Context, w io.Writer) error {
	if c.err != nil {
		return c.err
	}
	tw := tar.NewWriter(w)
	tw.WriteHeader(&tar.Header{Name: "data/caddy/certs/key.pem", Mode: 0o600, Size: 3})
	tw.Write([]byte("key"))
	return tw.Close()
}

const testPassword = "s3cret-pw"

// fixture is a Job over temp directories, with a fake pg_dump: a shell
// script that records its arguments and whether it got PGPASSWORD, then
// runs body.
type fixture struct {
	t    *testing.T
	job  *Job
	root string
	logs *strings.Builder
}

func newFixture(t *testing.T, body string) *fixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	root := t.TempDir()
	f := &fixture{t: t, root: root, logs: &strings.Builder{}}
	src := filepath.Join(root, "kek")
	os.Mkdir(src, 0o700)
	f.write("kek/k1.key", strings.Repeat("a", 32))
	f.write("kek/k2.key", strings.Repeat("b", 32))
	f.write("kek/README", "not a key")
	script := "#!/bin/sh\necho \"$@\" > " + filepath.Join(root, "args") + "\n" +
		"env > " + filepath.Join(root, "env") + "\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(root, "pg_dump"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	f.job = &Job{
		DatabaseURL: "postgres://shipyard:" + testPassword + "@127.0.0.1:5432/shipyard?sslmode=disable",
		PGDump:      filepath.Join(root, "pg_dump"),
		Caddy:       fakeCaddy{},
		KEKSource:   src,
		Dir:         filepath.Join(root, "a"),
		KEKDir:      filepath.Join(root, "b"),
		KeepDaily:   14, KeepWeekly: 8,
		Version: "test",
		Now:     func() time.Time { return time.Date(2026, 10, 1, 2, 30, 0, 0, time.UTC) },
		Log:     slog.New(slog.NewTextHandler(f.logs, nil)),
	}
	return f
}

const okDump = `printf 'PGDMP-fake-archive'`

func (f *fixture) write(rel, content string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.root, rel), []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) read(rel string) string {
	f.t.Helper()
	b, err := os.ReadFile(filepath.Join(f.root, rel))
	if err != nil {
		f.t.Fatal(err)
	}
	return string(b)
}

// names lists a directory, sorted; missing means empty.
func (f *fixture) names(rel string) []string {
	entries, _ := os.ReadDir(filepath.Join(f.root, rel))
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func (f *fixture) mode(rel string) os.FileMode {
	f.t.Helper()
	st, err := os.Stat(filepath.Join(f.root, rel))
	if err != nil {
		f.t.Fatal(err)
	}
	return st.Mode().Perm()
}

// P3.5: one run writes a whole backup into target A (dump, Caddy data,
// manifest with checksums), the KEKs into target B, and calls both hooks;
// everything is readable by the owner only; the database password is in
// PGPASSWORD, not in pg_dump's arguments, and no hook sees it.
func TestRun(t *testing.T) {
	f := newFixture(t, okDump)
	f.job.Hook = `echo "$SHIPYARD_BACKUP_PATH" > ` + filepath.Join(f.root, "hook-a") + `; env > ` + filepath.Join(f.root, "hook-env")
	f.job.KEKHook = `echo "$SHIPYARD_BACKUP_PATH" > ` + filepath.Join(f.root, "hook-b")
	if err := f.job.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	const name = "20261001T023000Z"
	if got := f.names("a"); !slices.Equal(got, []string{name}) {
		t.Fatalf("target A = %v", got)
	}
	if got := f.names("a/" + name); !slices.Equal(got, []string{CaddyName, DumpName, ManifestName}) {
		t.Fatalf("backup = %v", got)
	}
	if got := f.names("b"); !slices.Equal(got, []string{"k1.key", "k2.key"}) {
		t.Fatalf("target B = %v", got)
	}
	for rel, want := range map[string]os.FileMode{"a": 0o700, "a/" + name: 0o700, "a/" + name + "/" + DumpName: 0o600,
		"a/" + name + "/" + CaddyName: 0o600, "a/" + name + "/" + ManifestName: 0o600, "b": 0o700, "b/k1.key": 0o600} {
		if got := f.mode(rel); got != want {
			t.Errorf("%s: mode %o, want %o", rel, got, want)
		}
	}
	if f.read("b/k2.key") != strings.Repeat("b", 32) || f.read("a/"+name+"/"+DumpName) != "PGDMP-fake-archive" {
		t.Fatal("copied content differs")
	}

	var m Manifest
	if err := json.Unmarshal([]byte(f.read("a/"+name+"/"+ManifestName)), &m); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("PGDMP-fake-archive"))
	if m.Version != 1 || m.Shipyard != "test" || !m.CreatedAt.Equal(f.job.Now()) || !slices.Equal(m.KEKIDs, []string{"k1", "k2"}) ||
		len(m.Files) != 2 || m.Files[0] != (File{Name: DumpName, Bytes: 18, SHA256: hex.EncodeToString(sum[:])}) || m.Files[1].Name != CaddyName {
		t.Fatalf("manifest = %+v", m)
	}
	// The Caddy archive is a gzip of the tar stream.
	zf, _ := os.Open(filepath.Join(f.root, "a", name, CaddyName))
	defer zf.Close()
	zr, err := gzip.NewReader(zf)
	if err != nil {
		t.Fatal(err)
	}
	if h, err := tar.NewReader(zr).Next(); err != nil || h.Name != "data/caddy/certs/key.pem" {
		t.Fatalf("caddy archive: %v, %v", h, err)
	}

	args := f.read("args")
	if strings.Contains(args, testPassword) || !strings.Contains(args, "--format=custom --no-password --dbname=postgres://shipyard@127.0.0.1:5432/shipyard?sslmode=disable") {
		t.Fatalf("pg_dump args = %s", args)
	}
	if env := f.read("env"); !strings.Contains(env, "PGPASSWORD="+testPassword) {
		t.Fatal("pg_dump did not get PGPASSWORD")
	}
	if strings.TrimSpace(f.read("hook-a")) != filepath.Join(f.root, "a", name) || strings.TrimSpace(f.read("hook-b")) != filepath.Join(f.root, "b") {
		t.Fatalf("hooks saw %q and %q", f.read("hook-a"), f.read("hook-b"))
	}
	if env := f.read("hook-env"); strings.Contains(env, testPassword) || strings.Contains(env, "PGPASSWORD") {
		t.Fatalf("the hook saw the database password:\n%s", env)
	}
	if strings.Contains(f.logs.String(), testPassword) {
		t.Fatal("the password was logged")
	}
}

// A failed or wrong dump, or a failed Caddy archive, leaves nothing in
// target A, not even a partial directory, and removes no older backup. The
// KEKs are still backed up.
func TestRunDataFails(t *testing.T) {
	for name, tc := range map[string]struct {
		body  string
		caddy error
		want  string
	}{
		"pg_dump fails":  {body: `echo "connection refused" >&2; exit 1`, want: "connection refused"},
		"not an archive": {body: `echo "pg_dump: usage"`, want: "did not write a custom-format archive"},
		"empty":          {body: `true`, want: "did not write a custom-format archive"},
		"caddy fails":    {body: okDump, caddy: errors.New("no such container"), want: "caddy data: no such container"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, tc.body)
			f.job.Caddy = fakeCaddy{err: tc.caddy}
			f.job.KeepDaily, f.job.KeepWeekly = 1, 0
			f.job.Hook = "touch " + filepath.Join(f.root, "hook-ran")
			old := filepath.Join(f.root, "a", "20260901T023000Z")
			if err := os.MkdirAll(old, 0o700); err != nil {
				t.Fatal(err)
			}
			err := f.job.Run(t.Context())
			if err == nil || !strings.Contains(err.Error(), "data backup: ") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if strings.Contains(err.Error(), testPassword) {
				t.Fatalf("the error leaks the password: %v", err)
			}
			if got := f.names("a"); !slices.Equal(got, []string{"20260901T023000Z"}) {
				t.Fatalf("target A = %v, want only the older backup", got)
			}
			if got := f.names("b"); len(got) != 2 {
				t.Fatalf("target B = %v", got)
			}
			if _, err := os.Stat(filepath.Join(f.root, "hook-ran")); err == nil {
				t.Fatal("the hook ran without a backup")
			}
		})
	}
}

// Target B only gains keys. A retired KEK stays, and a KEK whose bytes
// changed under the same ID is reported, not overwritten; the data backup
// still runs.
func TestBackupKeys(t *testing.T) {
	f := newFixture(t, okDump)
	if err := f.job.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(f.root, "kek", "k1.key")) // retired
	f.write("kek/k3.key", strings.Repeat("c", 32))
	f.job.Now = func() time.Time { return time.Date(2026, 10, 2, 2, 30, 0, 0, time.UTC) }
	if err := f.job.Run(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := f.names("b"); !slices.Equal(got, []string{"k1.key", "k2.key", "k3.key"}) {
		t.Fatalf("target B = %v", got)
	}

	f.write("kek/k2.key", strings.Repeat("x", 32))
	f.job.KEKHook = "touch " + filepath.Join(f.root, "hook-ran")
	f.job.Now = func() time.Time { return time.Date(2026, 10, 3, 2, 30, 0, 0, time.UTC) }
	err := f.job.Run(t.Context())
	if err == nil || !strings.Contains(err.Error(), "KEK backup: key k2 differs from its backup") {
		t.Fatalf("err = %v", err)
	}
	if f.read("b/k2.key") != strings.Repeat("b", 32) {
		t.Fatal("the backed-up key was overwritten")
	}
	if _, err := os.Stat(filepath.Join(f.root, "hook-ran")); err == nil {
		t.Fatal("the KEK hook ran after a failed KEK backup")
	}
	if got := f.names("a"); len(got) != 3 {
		t.Fatalf("target A = %v, want 3 backups", got)
	}

	// No keys at all is an error too: the dump would be unreadable alone.
	empty := newFixture(t, okDump)
	for _, k := range []string{"k1.key", "k2.key"} {
		os.Remove(filepath.Join(empty.root, "kek", k))
	}
	if err := empty.job.Run(t.Context()); err == nil || !strings.Contains(err.Error(), "no .key files") {
		t.Fatalf("no keys: %v", err)
	}
}

// Rotation removes expired backups only: other entries in target A are not
// touched. A failing hook is reported, the backup stays, and rotation
// still runs.
func TestRotateAndHookFailure(t *testing.T) {
	f := newFixture(t, okDump)
	f.job.KeepDaily, f.job.KeepWeekly = 2, 0
	for _, n := range []string{"20260928T023000Z", "20260929T023000Z", "20260930T023000Z", "notes", partialPrefix + "20260930T023000Z"} {
		if err := os.MkdirAll(filepath.Join(f.root, "a", n), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	f.write("a/20260101T000000Z", "a file, not a backup")
	f.job.Hook = "echo upload failed >&2; exit 3"
	err := f.job.Run(t.Context())
	if err == nil || !strings.Contains(err.Error(), "upload failed") || !strings.Contains(err.Error(), "exit status 3") {
		t.Fatalf("err = %v", err)
	}
	want := []string{"20260101T000000Z", "20260930T023000Z", "20261001T023000Z", "notes"}
	if got := f.names("a"); !slices.Equal(got, want) {
		t.Fatalf("target A = %v\nwant       %v", got, want)
	}
	// The same second again: refused, nothing overwritten.
	f.job.Hook = ""
	if err := f.job.Run(t.Context()); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second run in the same second: %v", err)
	}
}

func TestDumpCommand(t *testing.T) {
	for name, tc := range map[string]struct {
		url, dbname, password string
	}{
		"userinfo": {"postgres://u:p%40ss@db.internal:5433/shipyard?sslmode=verify-full", "postgres://u@db.internal:5433/shipyard?sslmode=verify-full", "p@ss"},
		"query":    {"postgresql://u@h/d?password=qp&sslmode=disable", "postgresql://u@h/d?sslmode=disable", "qp"},
		"none":     {"postgres://u@h/d", "postgres://u@h/d", ""},
		"socket":   {"postgres:///shipyard?host=/var/run/postgresql", "postgres:///shipyard?host=/var/run/postgresql", ""},
	} {
		args, env, err := dumpCommand(tc.url)
		if err != nil || !slices.Equal(args, []string{"--format=custom", "--no-password", "--dbname=" + tc.dbname}) {
			t.Errorf("%s: args = %q, %v", name, args, err)
		}
		if got := slices.Contains(env, "PGPASSWORD="+tc.password); got != (tc.password != "") {
			t.Errorf("%s: env has the password = %v", name, got)
		}
		for _, e := range env {
			if strings.HasPrefix(e, "SHIPYARD_") {
				t.Errorf("%s: %s reaches pg_dump", name, e)
			}
		}
	}
	for _, bad := range []string{"host=localhost password=" + testPassword, "mysql://u:" + testPassword + "@h/d", "postgres://u:" + testPassword + "@h:port/d"} {
		if _, _, err := dumpCommand(bad); err == nil || strings.Contains(err.Error(), testPassword) {
			t.Errorf("dumpCommand(%q) = %v, want an error without the password", bad, err)
		}
	}
}
