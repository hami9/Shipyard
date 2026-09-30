package app

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/hami9/shipyard/internal/store"
)

type retentionFakes struct {
	calls              []string
	pruneErr, trimErr  error
	gotKeep            int
	gotMax, gotMaxUsed int64
}

func (f *retentionFakes) PruneCache(_ context.Context, maxUsed int64) (string, error) {
	f.calls, f.gotMaxUsed = append(f.calls, "prune"), maxUsed
	return "1.5GB", f.pruneErr
}
func (f *retentionFakes) TrimOperationEvents(_ context.Context, keep int, maxBytes int64) (store.Trimmed, error) {
	f.calls, f.gotKeep, f.gotMax = append(f.calls, "trim"), keep, maxBytes
	return store.Trimmed{Operations: 2, Events: 40}, f.trimErr
}

// P3.4b: one pass prunes the build cache to its cap, then trims events with
// the configured limits; a failing step does not skip the other.
func TestRetentionRun(t *testing.T) {
	for name, tc := range map[string]struct {
		pruneErr, trimErr error
		want              string
	}{
		"ok":           {},
		"prune fails":  {pruneErr: errors.New("buildx gone"), want: "buildx gone"},
		"trim fails":   {trimErr: errors.New("db down"), want: "db down"},
		"both failing": {pruneErr: errors.New("buildx gone"), trimErr: errors.New("db down"), want: "db down"},
	} {
		t.Run(name, func(t *testing.T) {
			f := &retentionFakes{pruneErr: tc.pruneErr, trimErr: tc.trimErr}
			var logs strings.Builder
			r := &Retention{Events: f, Cache: f, CacheMax: 10 << 30, KeepOperations: 20, MaxEventBytes: 5 << 20,
				Log: slog.New(slog.NewTextHandler(&logs, nil))}
			err := r.Run(t.Context())
			if strings.Join(f.calls, ",") != "prune,trim" || f.gotMaxUsed != 10<<30 || f.gotKeep != 20 || f.gotMax != 5<<20 {
				t.Fatalf("calls %v with %d, %d, %d", f.calls, f.gotMaxUsed, f.gotKeep, f.gotMax)
			}
			if tc.want == "" {
				if err != nil || !strings.Contains(logs.String(), "reclaimed=1.5GB") || !strings.Contains(logs.String(), "events=40") {
					t.Fatalf("err %v, logs:\n%s", err, logs.String())
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}
