package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/hami9/shipyard/internal/config"
	"github.com/hami9/shipyard/internal/secrets"
	"github.com/hami9/shipyard/internal/store"
)

// KEK rotation (ADR-0012). The operator adds a key file, makes it active
// for both services, restarts them, and then runs `kek rewrap`, which moves
// every value's data key to the active KEK. The worker runs it because it
// is the process that may open data keys.

// rewrapBatch is how many values one page of a rewrap reads.
const rewrapBatch = 500

const kekActor = "cli:shipyard-worker"

// kekStore is what the kek commands need from persistence.
type kekStore interface {
	KEKUsage(ctx context.Context) (map[string]int64, error)
	WrapsNotUnder(ctx context.Context, kekID, after string, limit int) ([]store.SecretWrap, error)
	Rewrap(ctx context.Context, from store.SecretWrap, wrapped []byte, kekID string) (bool, error)
	RecordAudit(ctx context.Context, e store.AuditEvent) (store.AuditEvent, error)
}

// kekGenerate writes a new HPKE KEK into dir: <id>.hpke (the private key,
// mode 0600: only the worker may read it) and <id>.pub (the public key the
// API seals with). It refuses an ID that already has any key file, and
// leaves no half-written pair behind.
func kekGenerate(dir, id string, out io.Writer) error {
	if !kekIDRE.MatchString(id) {
		return fmt.Errorf("invalid KEK id %q: 1-64 letters, digits, '_' or '-'", id)
	}
	for _, suffix := range []string{secrets.SymmetricSuffix, secrets.PrivateSuffix, secrets.PublicSuffix} {
		if _, err := os.Lstat(filepath.Join(dir, id+suffix)); err == nil {
			return fmt.Errorf("%s already exists: a KEK ID is never reused", filepath.Join(dir, id+suffix))
		}
	}
	private, public, err := secrets.GenerateHPKE()
	if err != nil {
		return err
	}
	defer clear(private)
	privPath, pubPath := filepath.Join(dir, id+secrets.PrivateSuffix), filepath.Join(dir, id+secrets.PublicSuffix)
	if err := writeNew(privPath, private, 0o600); err != nil {
		return err
	}
	if err := writeNew(pubPath, public, 0o644); err != nil {
		os.Remove(privPath)
		return err
	}
	fmt.Fprintf(out, `Wrote %s (private, mode 0600) and %s (public).
Next:
  1. Give the private key to the worker's user only, e.g.
     chown shipyard-worker: %s
  2. Set %s=%s in shipyard.env and restart shipyard-api and shipyard-worker.
  3. Run: shipyard-worker kek rewrap
  4. When kek status shows the old KEKs unused, delete their %s files, so
     the API holds nothing that decrypts. Backups keep their copies.
`, privPath, pubPath, privPath, config.EnvKEKActive, id, secrets.SymmetricSuffix)
	return nil
}

// kekIDRE mirrors the CHECK on secret_values.kek_id.
var kekIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// writeNew creates path with mode perm and writes data, failing if it
// exists; a partial file is removed.
func writeNew(path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return err
	}
	return nil
}

// kekStatus prints the loaded KEKs and how many values each one wraps.
func kekStatus(ctx context.Context, s kekStore, keys *secrets.Keyring, out io.Writer) error {
	usage, err := s.KEKUsage(ctx)
	if err != nil {
		return err
	}
	ids := keys.IDs()
	for id := range usage {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "KEK\tVALUES\tSTATE")
	for _, id := range ids {
		var state []string
		if id == keys.Active() {
			state = append(state, "active")
		}
		if !keys.Has(id) {
			state = append(state, "NOT LOADED: values under it cannot be opened")
		} else if !keys.CanOpen(id) {
			state = append(state, "PUBLIC KEY ONLY: values under it cannot be opened here")
		} else if id != keys.Active() && usage[id] == 0 {
			state = append(state, "unused: may be retired")
		} else if id != keys.Active() {
			state = append(state, "run `kek rewrap`")
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\n", id, usage[id], strings.Join(state, ", "))
	}
	return tw.Flush()
}

// kekRewrap moves every value whose data key another KEK wraps to the
// active KEK, in one pass in ID order, one row per statement. It refuses to
// start while a KEK in use is not loaded, and stops at the first value that
// does not open: such a row needs an operator, not a retry. Running it again
// is safe; it only touches rows still under another KEK.
func kekRewrap(ctx context.Context, s kekStore, keys *secrets.Keyring, log *slog.Logger) (int, error) {
	usage, err := s.KEKUsage(ctx)
	if err != nil {
		return 0, err
	}
	var missing []string
	for id, n := range usage {
		if n > 0 && id != keys.Active() && !keys.CanOpen(id) {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return 0, fmt.Errorf("KEKs %s wrap values but are not in the KEK directory (or only their public key is): restore them first", strings.Join(missing, ", "))
	}
	active, moved, after := keys.Active(), 0, ""
	for {
		page, err := s.WrapsNotUnder(ctx, active, after, rewrapBatch)
		if err != nil {
			return moved, err
		}
		if len(page) == 0 {
			break
		}
		for _, w := range page {
			wrapped, err := keys.Rewrap(w.ID, w.WrappedDEK, w.KEKID)
			if err != nil {
				return moved, fmt.Errorf("value %s (KEK %s): %w", w.ID, w.KEKID, err)
			}
			ok, err := s.Rewrap(ctx, w, wrapped, active)
			if err != nil {
				return moved, fmt.Errorf("value %s: %w", w.ID, err)
			}
			if ok {
				moved++
			}
		}
		after = page[len(page)-1].ID
		log.Info("rewrap progress", slog.String("kek", active), slog.Int("moved", moved))
	}
	if _, err := s.RecordAudit(ctx, store.AuditEvent{Actor: kekActor, Action: "kek.rewrap",
		Target: fmt.Sprintf("%d values to %s", moved, active), Result: store.AuditSuccess}); err != nil {
		return moved, err
	}
	return moved, nil
}
