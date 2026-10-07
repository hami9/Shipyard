package routing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeCaddy mimics the admin API's concurrency contract on a Unix socket:
// GET /config/ returns an Etag, and POST /config/ with a stale If-Match is
// 412. POST /load, which ignores If-Match in real Caddy, is not served, so
// a client using it fails here.
type fakeCaddy struct {
	mu        sync.Mutex
	config    string
	version   int
	loads     []string // If-Match of every POST /config/
	conflicts int      // answer this many loads with 412 first
	reject    string   // answer loads with 400 and this error
}

func (f *fakeCaddy) etag() string { return fmt.Sprintf(`"/config/ v%d"`, f.version) }

func (f *fakeCaddy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/config/":
		w.Header().Set("Etag", f.etag())
		io.WriteString(w, f.config)
	case r.Method == http.MethodPost && r.URL.Path == "/config/":
		f.loads = append(f.loads, r.Header.Get("If-Match"))
		body, _ := io.ReadAll(r.Body)
		switch {
		case r.Header.Get("Content-Type") != "application/json":
			http.Error(w, `{"error":"wrong content type"}`, http.StatusBadRequest)
		case f.conflicts > 0:
			f.conflicts--
			f.version++ // someone else changed it meanwhile
			w.WriteHeader(http.StatusPreconditionFailed)
		case f.reject != "":
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"error":%q}`, f.reject)
		case r.Header.Get("If-Match") != "" && r.Header.Get("If-Match") != f.etag():
			w.WriteHeader(http.StatusPreconditionFailed)
		default:
			f.config, f.version = string(body), f.version+1
		}
	default:
		http.NotFound(w, r)
	}
}

func serveFake(t *testing.T, f *fakeCaddy) *Admin {
	t.Helper()
	// A short path: Unix socket paths are limited to about 100 bytes.
	dir, err := os.MkdirTemp("", "caddy")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "admin.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: f}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return NewAdmin(sock)
}

const cfgA, cfgB = `{"apps":{"http":{}}}`, `{"apps":{"http":{"servers":{}}}}`

func TestAdminConfig(t *testing.T) {
	f := &fakeCaddy{config: cfgA}
	body, etag, err := serveFake(t, f).Config(t.Context())
	if err != nil || string(body) != cfgA || etag != `"/config/ v0"` {
		t.Fatalf("Config = %s, %s, %v", body, etag, err)
	}
}

func TestAdminApplyLoadsWithIfMatch(t *testing.T) {
	f := &fakeCaddy{config: cfgA, version: 7}
	changed, err := serveFake(t, f).Apply(t.Context(), []byte(cfgB))
	if err != nil || !changed || f.config != cfgB {
		t.Fatalf("Apply = %v, %v; config %s", changed, err, f.config)
	}
	if len(f.loads) != 1 || f.loads[0] != `"/config/ v7"` {
		t.Fatalf("loads sent If-Match %q", f.loads)
	}
}

// Re-rendering an unchanged table costs no reload.
func TestAdminApplySkipsEqualConfig(t *testing.T) {
	f := &fakeCaddy{config: `{"apps": {"http": {}}}`} // same meaning, other bytes
	changed, err := serveFake(t, f).Apply(t.Context(), []byte(cfgA))
	if err != nil || changed || len(f.loads) != 0 {
		t.Fatalf("Apply = %v, %v with %d loads", changed, err, len(f.loads))
	}
}

// A concurrent change (412) is re-read and retried with the new Etag.
func TestAdminApplyRetriesConflict(t *testing.T) {
	f := &fakeCaddy{config: cfgA, conflicts: 1}
	changed, err := serveFake(t, f).Apply(t.Context(), []byte(cfgB))
	if err != nil || !changed || f.config != cfgB {
		t.Fatalf("Apply = %v, %v", changed, err)
	}
	if len(f.loads) != 2 || f.loads[0] == f.loads[1] {
		t.Fatalf("If-Match per attempt = %q, want a fresh Etag on the retry", f.loads)
	}
}

func TestAdminApplyGivesUp(t *testing.T) {
	f := &fakeCaddy{config: cfgA, conflicts: 100}
	_, err := serveFake(t, f).Apply(t.Context(), []byte(cfgB))
	if !errors.Is(err, ErrConflict) || len(f.loads) != applyAttempts || f.config != cfgA {
		t.Fatalf("err = %v after %d loads", err, len(f.loads))
	}
}

func TestAdminLoadRejected(t *testing.T) {
	f := &fakeCaddy{config: cfgA, reject: "loading new config: unknown module: http.handlers.nope"}
	err := serveFake(t, f).Load(t.Context(), []byte(cfgB), "")
	var le *LoadError
	if !errors.As(err, &le) || le.Status != 400 || !strings.Contains(le.Message, "unknown module") || strings.Contains(le.Message, `"error"`) {
		t.Fatalf("err = %#v", err)
	}
	if f.config != cfgA {
		t.Fatal("a rejected load changed the config")
	}
}

// A stale Etag on a direct Load is ErrConflict, not a generic error.
func TestAdminLoadStaleEtag(t *testing.T) {
	f := &fakeCaddy{config: cfgA, version: 3}
	if err := serveFake(t, f).Load(t.Context(), []byte(cfgB), `"/config/ v2"`); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v", err)
	}
}

func TestAdminNoSocket(t *testing.T) {
	a := NewAdmin(filepath.Join(t.TempDir(), "missing.sock"))
	_, _, err := a.Config(t.Context())
	if err == nil || !strings.Contains(err.Error(), "missing.sock") {
		t.Fatalf("err = %v", err)
	}
}

func TestAdminHonorsContext(t *testing.T) {
	f := &fakeCaddy{config: cfgA}
	a := serveFake(t, f)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := a.Apply(ctx, []byte(cfgB)); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestCaddyMessageBounded(t *testing.T) {
	long := strings.Repeat("x", 5000)
	if m := caddyMessage([]byte(`{"error":"` + long + `"}`)); len(m) > 2010 {
		t.Fatalf("message is %d bytes", len(m))
	}
	if m := caddyMessage([]byte("plain text\n")); m != "plain text" {
		t.Fatalf("plain = %q", m)
	}
}
