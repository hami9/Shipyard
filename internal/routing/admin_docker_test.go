//go:build docker

package routing

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hami9/shipyard/internal/runtime"
)

// The admin client against a real Caddy (the P2.1 edge): Etag format,
// Apply's no-op on an equal config, 412 on a stale If-Match, and a rejected
// config leaving the running one in place.
func TestAdminAgainstCaddy(t *testing.T) {
	ctx := t.Context()
	rt, err := runtime.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	edge := runtime.EdgeSpec{Name: "shipyard-test-edge-" + strings.ToLower(rand.Text()[:8]), Image: runtime.DefaultEdgeImage,
		AdminDir: filepath.Join(t.TempDir(), "admin"), GID: os.Getgid(), BindIP: netip.MustParseAddr("127.0.0.1")}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		rt.RemoveEdge(c, edge.Name, true)
	})
	if _, err := rt.EnsureEdge(ctx, edge); err != nil {
		t.Fatal(err)
	}
	a := NewAdmin(edge.AdminSocket())

	_, etag0, err := a.Config(ctx)
	if err != nil || !strings.HasPrefix(etag0, `"/config/ `) {
		t.Fatalf("Etag = %q, %v", etag0, err)
	}

	s := Settings{AdminSocket: edge.AdminSocket(), CA: CAInternal}
	cfg1, _ := Render(s, []Route{{Hostname: "one.test.example"}})
	cfg2, _ := Render(s, []Route{{Hostname: "two.test.example"}})
	if changed, err := a.Apply(ctx, cfg1); err != nil || !changed {
		t.Fatalf("first Apply = %v, %v", changed, err)
	}
	// Caddy returns its stored config re-encoded; Apply sees it as equal.
	if changed, err := a.Apply(ctx, cfg1); err != nil || changed {
		t.Fatalf("second Apply = %v, %v; want no reload", changed, err)
	}

	// /load honors If-Match: an Etag read before another change is stale.
	_, stale, _ := a.Config(ctx)
	if changed, err := a.Apply(ctx, cfg2); err != nil || !changed {
		t.Fatalf("Apply cfg2 = %v, %v", changed, err)
	}
	if err := a.Load(ctx, cfg1, stale); !errors.Is(err, ErrConflict) {
		t.Fatalf("Load with a stale Etag: %v, want ErrConflict", err)
	}
	if running, _, _ := a.Config(ctx); !mustSame(t, running, cfg2) {
		t.Fatal("a 412 load changed the config")
	}

	// The vendor fact behind Load's choice of /config/: POST /load ignores
	// If-Match and applies a load made from a stale read [CADDY-ADMIN-SRC].
	// If a Caddy upgrade changes this, this check fails and the docs need
	// revisiting.
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://caddy/load", strings.NewReader(string(cfg1)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("If-Match", stale)
	resp, err := a.hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if running, _, _ := a.Config(ctx); resp.StatusCode != http.StatusOK || !mustSame(t, running, cfg1) {
		t.Fatalf("POST /load with a stale If-Match = %d; Caddy now honors it, revisit [CADDY-ADMIN-SRC]", resp.StatusCode)
	}
	if changed, err := a.Apply(ctx, cfg2); err != nil || !changed {
		t.Fatalf("back to cfg2 = %v, %v", changed, err)
	}

	// A config Caddy rejects leaves the running one in place (invariant 5).
	broken := `{"admin":{"listen":"unix/` + edge.AdminSocket() + `|0660"},"apps":{"http":{"servers":{"x":{"listen":[":80"],"routes":[{"handle":[{"handler":"nope"}]}]}}}}}`
	var le *LoadError
	// Through /config/, Caddy answers a config that fails to load with 500
	// (/load says 400) [CADDY-ADMIN-SRC].
	if err := a.Load(ctx, []byte(broken), ""); !errors.As(err, &le) || le.Status != 500 || !strings.Contains(le.Message, "unknown module") {
		t.Fatalf("broken config: %v", err)
	}
	if running, _, err := a.Config(ctx); err != nil || !mustSame(t, running, cfg2) {
		t.Fatalf("after a rejected load: %v", err)
	}
}

func mustSame(t *testing.T, a, b []byte) bool {
	t.Helper()
	same, err := sameJSON(a, b)
	if err != nil {
		t.Fatal(err)
	}
	return same
}
