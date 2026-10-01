// Package backup writes Shipyard's recovery data (ADR-0006) to two separate
// local targets: A holds one directory per backup, with the PostgreSQL dump
// and Caddy's data; B holds the KEK files. A hook per target copies it
// off-host. Ciphertext and keys never share a target (ADR-0005).
package backup

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Names inside a backup directory of target A.
const (
	DumpName     = "database.dump"     // pg_dump custom format [PG-DUMP]
	CaddyName    = "caddy-data.tar.gz" // the edge container's /data
	ManifestName = "manifest.json"
)

// nameLayout names a backup directory by its UTC time; names sort by age.
const nameLayout = "20060102T150405Z"

const (
	partialPrefix = ".partial-"
	keySuffix     = ".key"
	// maxKeySize guards against copying something that is not a KEK (32 bytes).
	maxKeySize = 4096
)

// dumpMagic starts every pg_dump custom-format archive.
var dumpMagic = []byte("PGDMP")

// CaddyData writes the edge container's data volume as a tar stream
// (runtime.ArchiveEdgeData).
type CaddyData interface {
	ArchiveData(ctx context.Context, w io.Writer) error
}

// Job is one backup run.
type Job struct {
	// DatabaseURL contains credentials. It is never logged, and its
	// password never appears on a command line.
	DatabaseURL string
	PGDump      string    // pg_dump: a name on PATH or an absolute path
	Caddy       CaddyData // nil when Caddy is disabled: no data to archive
	KEKSource   string    // the live KEK directory
	Dir         string    // target A
	KEKDir      string    // target B
	Hook        string    // run after a backup lands in Dir
	KEKHook     string    // run after KEKDir is brought up to date
	KeepDaily   int
	KeepWeekly  int
	Version     string // the Shipyard build, for the manifest
	Now         func() time.Time
	Log         *slog.Logger
}

// Manifest describes one backup of target A, for the restore (P3.6).
type Manifest struct {
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	Shipyard  string    `json:"shipyard"`
	Files     []File    `json:"files"`
	// KEKIDs are the keys the dump's secrets may need, by ID. The keys
	// themselves are in target B only.
	KEKIDs []string `json:"kek_ids"`
}

// File is one file of a backup, with its checksum.
type File struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// Run backs up the KEKs and then the data. The two are independent: a
// failure of one does not skip the other, and both are reported.
func (j *Job) Run(ctx context.Context) error {
	var errs []error
	ids, err := j.backupKeys(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("KEK backup: %w", err))
	}
	if err := j.backupData(ctx, ids); err != nil {
		errs = append(errs, fmt.Errorf("data backup: %w", err))
	}
	return errors.Join(errs...)
}

// backupKeys copies every KEK that target B lacks and returns the IDs of the
// live keys. A key is never overwritten or removed in B: a retired KEK still
// opens older values, and a KEK whose bytes changed under the same ID is an
// error to look at, not to paper over.
func (j *Job) backupKeys(ctx context.Context) ([]string, error) {
	entries, err := os.ReadDir(j.KEKSource)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(j.KEKDir, 0o700); err != nil {
		return nil, err
	}
	var ids []string
	var errs []error
	copied := 0
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), keySuffix)
		if !ok || id == "" || e.IsDir() {
			continue
		}
		ids = append(ids, id)
		key, err := readKey(filepath.Join(j.KEKSource, e.Name()))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		dst := filepath.Join(j.KEKDir, e.Name())
		switch have, err := readKey(dst); {
		case err == nil && bytes.Equal(have, key):
		case err == nil:
			errs = append(errs, fmt.Errorf("key %s differs from its backup %s; a KEK must not change under the same ID", id, dst))
		case errors.Is(err, fs.ErrNotExist):
			if err := writeAtomic(dst, key); err != nil {
				errs = append(errs, err)
				continue
			}
			copied++
		default:
			errs = append(errs, err)
		}
	}
	if len(ids) == 0 {
		errs = append(errs, fmt.Errorf("no %s files in %s", keySuffix, j.KEKSource))
	}
	if err := errors.Join(errs...); err != nil {
		return ids, err
	}
	j.Log.Info("KEKs backed up", slog.String("dir", j.KEKDir), slog.Int("keys", len(ids)), slog.Int("copied", copied))
	return ids, j.runHook(ctx, j.KEKHook, j.KEKDir)
}

func readKey(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxKeySize+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxKeySize {
		return nil, fmt.Errorf("%s is larger than %d bytes: not a KEK", path, maxKeySize)
	}
	return b, nil
}

// writeAtomic writes a file readable by its owner only, complete or not at all.
func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// backupData writes one complete backup directory into target A, runs the
// hook, and rotates. The directory appears under its final name only once
// every file and the manifest are written, so each backup in A is whole.
func (j *Job) backupData(ctx context.Context, kekIDs []string) error {
	now := time.Now
	if j.Now != nil {
		now = j.Now
	}
	created := now().UTC().Truncate(time.Second)
	name := created.Format(nameLayout)
	final := filepath.Join(j.Dir, name)
	if err := os.MkdirAll(j.Dir, 0o700); err != nil {
		return err
	}
	// Leftovers of a run that was killed.
	stale, _ := filepath.Glob(filepath.Join(j.Dir, partialPrefix+"*"))
	for _, p := range stale {
		os.RemoveAll(p)
	}
	if _, err := os.Lstat(final); err == nil {
		return fmt.Errorf("%s already exists", final)
	}
	tmp := filepath.Join(j.Dir, partialPrefix+name)
	if err := os.Mkdir(tmp, 0o700); err != nil {
		return err
	}
	done := false
	defer func() {
		if !done {
			os.RemoveAll(tmp)
		}
	}()

	m := Manifest{Version: 1, CreatedAt: created, Shipyard: j.Version, KEKIDs: kekIDs}
	dump, err := writeFile(filepath.Join(tmp, DumpName), func(w io.Writer) error { return j.dump(ctx, w) })
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	m.Files = append(m.Files, dump)
	if j.Caddy != nil {
		caddy, err := writeFile(filepath.Join(tmp, CaddyName), func(w io.Writer) error {
			zw := gzip.NewWriter(w)
			if err := j.Caddy.ArchiveData(ctx, zw); err != nil {
				return err
			}
			return zw.Close()
		})
		if err != nil {
			return fmt.Errorf("caddy data: %w", err)
		}
		m.Files = append(m.Files, caddy)
	}
	manifest, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, ManifestName), append(manifest, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	done = true
	attrs := []any{slog.String("path", final)}
	for _, f := range m.Files {
		attrs = append(attrs, slog.Int64(f.Name, f.Bytes))
	}
	j.Log.Info("backup written", attrs...)

	// Rotate even if the hook fails: the disk bound must hold while the
	// off-host target is unreachable.
	hookErr := j.runHook(ctx, j.Hook, final)
	return errors.Join(hookErr, j.rotate())
}

// writeFile creates path for its owner only, fills it, and returns its
// size and checksum.
func writeFile(path string, fill func(io.Writer) error) (File, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return File{}, err
	}
	defer f.Close()
	sum := sha256.New()
	n := &counter{}
	if err := fill(io.MultiWriter(f, sum, n)); err != nil {
		return File{}, err
	}
	if err := f.Sync(); err != nil {
		return File{}, err
	}
	if err := f.Close(); err != nil {
		return File{}, err
	}
	return File{Name: filepath.Base(path), Bytes: n.n, SHA256: hex.EncodeToString(sum.Sum(nil))}, nil
}

type counter struct{ n int64 }

func (c *counter) Write(p []byte) (int, error) { c.n += int64(len(p)); return len(p), nil }

// dump runs pg_dump in the custom format, which is consistent while the
// database is in use and does not block it [PG-DUMP], and checks that what
// arrived is such an archive.
func (j *Job) dump(ctx context.Context, w io.Writer) error {
	args, env, err := dumpCommand(j.DatabaseURL)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, j.PGDump, args...)
	cmd.Env = env
	head := &prefix{max: len(dumpMagic)}
	stderr := &tail{max: 2000}
	cmd.Stdout, cmd.Stderr = io.MultiWriter(w, head), stderr
	cmd.WaitDelay = 10 * time.Second
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w: %s", filepath.Base(j.PGDump), err, strings.TrimSpace(stderr.String()))
	}
	if !bytes.Equal(head.b, dumpMagic) {
		return fmt.Errorf("%s did not write a custom-format archive", filepath.Base(j.PGDump))
	}
	return nil
}

// dumpCommand builds pg_dump's arguments and environment. The password moves
// from the URL into PGPASSWORD, so it is not visible in the process list.
// Nothing else of this process's environment reaches pg_dump.
func dumpCommand(dbURL string) (args, env []string, err error) {
	u, err := url.Parse(dbURL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		// The parse error would quote the URL, password included.
		return nil, nil, errors.New("the database URL must be a postgres:// URL")
	}
	password, _ := u.User.Password()
	if u.User != nil {
		u.User = url.User(u.User.Username())
	}
	if q := u.Query(); q.Has("password") {
		password = q.Get("password")
		q.Del("password")
		u.RawQuery = q.Encode()
	}
	for _, k := range []string{"PATH", "HOME", "DOCKER_HOST", "PGPASSFILE", "PGSSLROOTCERT", "PGSSLCERT", "PGSSLKEY"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	if password != "" {
		env = append(env, "PGPASSWORD="+password)
	}
	return []string{"--format=custom", "--no-password", "--dbname=" + u.String()}, env, nil
}

// runHook runs an operator's command with sh -c, to copy a target off-host.
// It sees SHIPYARD_BACKUP_PATH and a minimal environment, never the
// database URL.
func (j *Job) runHook(ctx context.Context, command, path string) error {
	if command == "" {
		return nil
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Env = []string{"SHIPYARD_BACKUP_PATH=" + path}
	for _, k := range []string{"PATH", "HOME"} {
		if v, ok := os.LookupEnv(k); ok {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	out := &tail{max: 2000}
	cmd.Stdout, cmd.Stderr = out, out
	cmd.WaitDelay = 10 * time.Second
	started := time.Now()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("hook for %s: %w: %s", path, err, strings.TrimSpace(out.String()))
	}
	j.Log.Info("backup hook finished", slog.String("path", path), slog.Duration("took", time.Since(started).Round(time.Millisecond)))
	return nil
}

// rotate removes the backups of target A that Expired picks. Only
// directories named like a backup are considered.
func (j *Job) rotate() error {
	entries, err := os.ReadDir(j.Dir)
	if err != nil {
		return err
	}
	var times []time.Time
	for _, e := range entries {
		if t, err := time.Parse(nameLayout, e.Name()); err == nil && e.IsDir() {
			times = append(times, t)
		}
	}
	var errs []error
	expired := Expired(times, j.KeepDaily, j.KeepWeekly)
	slices.SortFunc(expired, func(a, b time.Time) int { return a.Compare(b) })
	for _, t := range expired {
		name := t.Format(nameLayout)
		if err := os.RemoveAll(filepath.Join(j.Dir, name)); err != nil {
			errs = append(errs, err)
			continue
		}
		j.Log.Info("old backup removed", slog.String("name", name))
	}
	return errors.Join(errs...)
}

// prefix keeps the first max bytes written to it.
type prefix struct {
	max int
	b   []byte
}

func (p *prefix) Write(b []byte) (int, error) {
	if room := p.max - len(p.b); room > 0 {
		p.b = append(p.b, b[:min(room, len(b))]...)
	}
	return len(b), nil
}

// tail keeps the last max bytes written to it.
type tail struct {
	max int
	b   []byte
}

func (t *tail) Write(b []byte) (int, error) {
	t.b = append(t.b, b...)
	if len(t.b) > t.max {
		t.b = t.b[len(t.b)-t.max:]
	}
	return len(b), nil
}

func (t *tail) String() string { return string(t.b) }
