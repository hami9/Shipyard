package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hami9/shipyard/internal/logging"
)

type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

func newTestHandler(t *testing.T, db Pinger) (http.Handler, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	return NewHandler(logging.New(&buf, slog.LevelDebug, logging.FormatJSON), db), &buf
}

func do(h http.Handler, method, target string, header http.Header) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHealthz(t *testing.T) {
	h, _ := newTestHandler(t, fakePinger{err: errors.New("db down")})
	rec := do(h, http.MethodGet, "/healthz", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (liveness must not depend on the database)", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestReadyz(t *testing.T) {
	h, _ := newTestHandler(t, fakePinger{})
	if rec := do(h, http.MethodGet, "/readyz", nil); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestReadyzDatabaseDownReturnsProblem(t *testing.T) {
	h, logs := newTestHandler(t, fakePinger{err: errors.New("dial tcp 10.0.0.9:5432: connection refused")})
	rec := do(h, http.MethodGet, "/readyz", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
	var p Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if p.Status != 503 || p.Title != "Service Unavailable" || p.Type != "about:blank" {
		t.Errorf("problem = %+v", p)
	}
	if strings.Contains(rec.Body.String(), "10.0.0.9") {
		t.Error("internal error text leaked to the client")
	}
	if !strings.Contains(logs.String(), "readiness check failed") {
		t.Error("failure was not logged")
	}
}

func TestMethodAndRouteMatching(t *testing.T) {
	h, _ := newTestHandler(t, fakePinger{})
	if rec := do(h, http.MethodPost, "/healthz", nil); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /healthz = %d, want 405", rec.Code)
	}
	if rec := do(h, http.MethodGet, "/nope", nil); rec.Code != http.StatusNotFound {
		t.Errorf("GET /nope = %d, want 404", rec.Code)
	}
}

func TestRequestID(t *testing.T) {
	h, logs := newTestHandler(t, fakePinger{})
	tests := []struct {
		name     string
		incoming string
		keep     bool
	}{
		{"generated when absent", "", false},
		{"valid id kept", "abc-123_X.y", true},
		{"newline rejected", "abc\ninjected", false},
		{"too long rejected", strings.Repeat("a", 65), false},
		{"space rejected", "a b", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hdr := http.Header{}
			if tt.incoming != "" {
				hdr.Set(headerRequestID, tt.incoming)
			}
			rec := do(h, http.MethodGet, "/healthz", hdr)
			got := rec.Header().Get(headerRequestID)
			if tt.keep && got != tt.incoming {
				t.Errorf("request id = %q, want %q", got, tt.incoming)
			}
			if !tt.keep && (got == tt.incoming || !validRequestID(got)) {
				t.Errorf("request id = %q, want a fresh valid id", got)
			}
			if !strings.Contains(logs.String(), `"request_id":"`+got+`"`) {
				t.Errorf("access log does not carry request_id %q", got)
			}
		})
	}
}

func TestAccessLogOmitsQueryString(t *testing.T) {
	h, logs := newTestHandler(t, fakePinger{})
	do(h, http.MethodGet, "/healthz?token=supersecret", nil)
	if strings.Contains(logs.String(), "supersecret") {
		t.Error("query string was logged")
	}
	if !strings.Contains(logs.String(), `"path":"/healthz"`) || !strings.Contains(logs.String(), `"status":200`) {
		t.Errorf("access log missing fields: %s", logs.String())
	}
}

func TestListenUnixSocket(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "api.sock")

	ln, err := Listen("unix:" + sock)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	fi, err := os.Stat(sock)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o660 {
		t.Errorf("socket mode = %o, want 660", perm)
	}
	ln.Close()

	// A leftover socket from a crashed run is replaced.
	stale, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	if l, ok := stale.(*net.UnixListener); ok {
		l.SetUnlinkOnClose(false)
	}
	stale.Close()
	ln, err = Listen("unix:" + sock)
	if err != nil {
		t.Fatalf("Listen over stale socket: %v", err)
	}
	ln.Close()
}

func TestListenRefusesToReplaceRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "important.txt")
	if err := os.WriteFile(path, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen("unix:" + path); err == nil || !strings.Contains(err.Error(), "not a socket") {
		t.Fatalf("err = %v, want refusal", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "keep me" {
		t.Error("regular file was modified")
	}
}

func TestServeShutsDownGracefully(t *testing.T) {
	ln, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	log := logging.New(io.Discard, slog.LevelInfo, logging.FormatJSON)
	h, _ := newTestHandler(t, fakePinger{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, ln, h, 5*time.Second, log) }()

	resp, err := http.Get("http://" + ln.Addr().String() + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned %v, want nil after graceful shutdown", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return after cancel")
	}
}
