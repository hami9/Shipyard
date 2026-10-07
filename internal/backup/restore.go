package backup

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// CaddyRestore puts an archive of Caddy's data back into the edge
// container (runtime.RestoreEdgeData).
type CaddyRestore interface {
	RestoreData(ctx context.Context, archive io.Reader) error
}

// Restore loads one backup of target A onto this host (ADR-0006, the
// runbook in docs/RESTORE.md). The KEKs of target B must already be in
// KEKDir: only root may write there, and they travel apart from the data.
type Restore struct {
	From string // one backup directory of target A
	// DatabaseURL contains credentials; see Job.DatabaseURL.
	DatabaseURL string
	PGRestore   string       // pg_restore: a name on PATH or an absolute path
	KEKDir      string       // the live KEK directory
	Caddy       CaddyRestore // nil when Caddy is disabled
	// DatabaseEmpty reports whether the database has no tables yet.
	DatabaseEmpty func(ctx context.Context) (bool, error)
	Log           *slog.Logger
}

// kekIDRE keeps a manifest's key IDs from naming paths outside KEKDir.
var kekIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// maxManifest bounds what is read as a manifest.
const maxManifest = 1 << 20

// Run restores Caddy's data, then the database. Everything is checked
// before anything is written: the checksums, the keys, and that the
// database is empty. The database comes last and in one transaction, so a
// run that fails can simply be repeated.
func (r *Restore) Run(ctx context.Context) error {
	m, err := r.verify()
	if err != nil {
		return fmt.Errorf("backup %s: %w", r.From, err)
	}
	var missing []string
	for _, id := range m.KEKIDs {
		if !kekIDRE.MatchString(id) {
			return fmt.Errorf("backup %s: invalid KEK ID %q in the manifest", r.From, id)
		}
		// A symmetric key or an HPKE private key opens the values (ADR-0012).
		_, errSym := os.Stat(filepath.Join(r.KEKDir, id+keySuffix))
		_, errPriv := os.Stat(filepath.Join(r.KEKDir, id+privateSuffix))
		if errSym != nil && errPriv != nil {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s lacks the KEKs %s: copy them from the KEK backup (target B) first, or the restored secrets cannot be opened",
			r.KEKDir, strings.Join(missing, ", "))
	}
	if len(m.KEKIDs) == 0 {
		r.Log.Warn("the manifest names no KEKs; secrets in this backup may not be readable")
	}
	empty, err := r.DatabaseEmpty(ctx)
	if err != nil {
		return fmt.Errorf("check the database: %w", err)
	}
	if !empty {
		return errors.New("the database already has tables: restore into a newly created, empty database")
	}
	r.Log.Info("backup verified", slog.String("path", r.From), slog.Time("created_at", m.CreatedAt),
		slog.String("shipyard", m.Shipyard), slog.Int("keks", len(m.KEKIDs)))

	hasCaddy := false
	for _, f := range m.Files {
		hasCaddy = hasCaddy || f.Name == CaddyName
	}
	switch {
	case hasCaddy && r.Caddy == nil:
		r.Log.Warn("caddy is disabled; its data in the backup is not restored")
	case hasCaddy:
		if err := r.restoreCaddy(ctx); err != nil {
			return fmt.Errorf("caddy data: %w", err)
		}
		r.Log.Info("caddy data restored")
	}
	if err := r.restoreDatabase(ctx); err != nil {
		return fmt.Errorf("database: %w", err)
	}
	r.Log.Info("database restored")
	return nil
}

// verify reads the manifest and checks every file it lists against its
// size and SHA-256. Only the files a backup can hold are accepted, so a
// manifest cannot point anywhere else.
func (r *Restore) verify() (Manifest, error) {
	var m Manifest
	raw, err := os.ReadFile(filepath.Join(r.From, ManifestName))
	if err != nil {
		return m, err
	}
	if len(raw) > maxManifest {
		return m, errors.New("the manifest is too large")
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, fmt.Errorf("manifest: %w", err)
	}
	if m.Version != 1 {
		return m, fmt.Errorf("manifest version %d is not supported by this build", m.Version)
	}
	seen := map[string]bool{}
	for _, f := range m.Files {
		if (f.Name != DumpName && f.Name != CaddyName) || seen[f.Name] {
			return m, fmt.Errorf("manifest lists an unexpected file %q", f.Name)
		}
		seen[f.Name] = true
		size, sum, err := checksum(filepath.Join(r.From, f.Name))
		if err != nil {
			return m, err
		}
		if size != f.Bytes || sum != f.SHA256 {
			return m, fmt.Errorf("%s does not match the manifest (%d bytes, sha256 %s; want %d, %s): the backup is damaged",
				f.Name, size, sum, f.Bytes, f.SHA256)
		}
	}
	if !seen[DumpName] {
		return m, fmt.Errorf("manifest lists no %s", DumpName)
	}
	return m, nil
}

func checksum(path string) (int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

func (r *Restore) restoreCaddy(ctx context.Context) error {
	f, err := os.Open(filepath.Join(r.From, CaddyName))
	if err != nil {
		return err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	return r.Caddy.RestoreData(ctx, zr)
}

// restoreDatabase runs pg_restore on the dump, read from standard input.
//   - --single-transaction: all of it or nothing [PG-DUMP].
//   - --no-owner and --no-privileges: the objects belong to the connecting
//     role, whatever the role was called on the old host.
func (r *Restore) restoreDatabase(ctx context.Context) error {
	dbname, env, err := pgConnection(r.DatabaseURL)
	if err != nil {
		return err
	}
	dump, err := os.Open(filepath.Join(r.From, DumpName))
	if err != nil {
		return err
	}
	defer dump.Close()
	cmd := exec.CommandContext(ctx, r.PGRestore, "--single-transaction", "--no-owner", "--no-privileges",
		"--no-password", "--dbname="+dbname)
	cmd.Env = env
	out := &tail{max: 2000}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = dump, out, out
	cmd.WaitDelay = 10 * time.Second
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w: %s", filepath.Base(r.PGRestore), err, strings.TrimSpace(out.String()))
	}
	return nil
}
