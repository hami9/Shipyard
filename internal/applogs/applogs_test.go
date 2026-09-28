package applogs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hami9/shipyard/internal/store"
)

const (
	appA = "00000000-0000-4000-8000-00000000000a"
	appB = "00000000-0000-4000-8000-00000000000b"
	appC = "00000000-0000-4000-8000-00000000000c"
)

type fakes struct {
	read      string // the container, tail and follow last requested
	secretErr error
}

func (f *fakes) ActiveDeployment(_ context.Context, appID string) (store.Deployment, error) {
	rev := "rev-1"
	switch appID {
	case appA:
		return store.Deployment{ID: "dep-a", ContainerID: "ctr-a", EnvRevisionID: &rev}, nil
	case appC:
		return store.Deployment{ID: "dep-c", ContainerID: "ctr-c"}, nil // no environment
	}
	return store.Deployment{}, store.ErrNotFound
}

func (f *fakes) SecretValues(context.Context, string) ([]string, error) {
	return []string{"hunter2-secret"}, f.secretErr
}

func (f *fakes) StreamLogs(_ context.Context, id string, tail int, follow bool, fn func(Line) error) error {
	f.read = fmt.Sprintf("%s tail=%d follow=%t", id, tail, follow)
	ts := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	for _, l := range []Line{{TS: ts, Stream: "stdout", Text: "listening"}, {TS: ts, Stream: "stderr", Text: "auth with hunter2-secret failed"}} {
		if err := fn(l); err != nil {
			return err
		}
	}
	return nil
}

func serve(t *testing.T, h http.Handler) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "logs.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return path
}

func readAll(t *testing.T, s *Stream) (lines []string, end string) {
	t.Helper()
	defer s.Close()
	for {
		l, err := s.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if l.End != "" {
			return lines, l.End
		}
		lines = append(lines, fmt.Sprintf("%s %s %s", l.TS.Format(time.TimeOnly), l.Stream, l.Text))
	}
}

// The worker serves the active deployment's logs, redacted, and says why
// the stream ended.
func TestLogsOverSocket(t *testing.T) {
	f := &fakes{}
	c := NewClient(serve(t, &Server{Store: f, Secrets: f, Source: f, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}))
	ctx := t.Context()

	s, err := c.Logs(ctx, appA, 5, false)
	if err != nil {
		t.Fatal(err)
	}
	lines, end := readAll(t, s)
	if want := "12:00:00 stdout listening|12:00:00 stderr auth with [REDACTED] failed"; strings.Join(lines, "|") != want || end != EndTail {
		t.Fatalf("lines %q, end %q", lines, end)
	}
	if f.read != "ctr-a tail=5 follow=false" {
		t.Fatalf("read %s", f.read)
	}
	s, _ = c.Logs(ctx, appC, 0, true)
	if _, end := readAll(t, s); end != EndStopped || f.read != "ctr-c tail=0 follow=true" {
		t.Fatalf("follow: end %q, read %s", end, f.read)
	}

	if _, err := c.Logs(ctx, appB, 5, false); !errors.Is(err, ErrNoDeployment) {
		t.Fatalf("no deployment: %v", err)
	}
	for _, id := range []string{"nope", "../" + appA} {
		if _, err := c.Logs(ctx, id, 5, false); err == nil || errors.Is(err, ErrNoDeployment) {
			t.Fatalf("app %q: %v", id, err)
		}
	}
	if _, err := c.Logs(ctx, appA, MaxTail+1, false); err == nil {
		t.Fatal("tail over the maximum accepted")
	}
	// Secrets that cannot be read mean nothing is streamed.
	f.secretErr, f.read = errors.New("kek missing"), ""
	if _, err := c.Logs(ctx, appA, 5, false); err == nil || f.read != "" {
		t.Fatalf("unredactable: %v, read %q", err, f.read)
	}
	// No worker: unavailable.
	if _, err := NewClient(filepath.Join(t.TempDir(), "none.sock")).Logs(ctx, appA, 5, false); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("no socket: %v", err)
	}
}

// A stream that stops without an end line is reported as broken.
func TestStreamCutShort(t *testing.T) {
	c := NewClient(serve(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, `{"stream":"stdout","line":"one"}`)
	})))
	s, err := c.Logs(t.Context(), appA, 5, false)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if l, err := s.Next(); err != nil || l.Text != "one" {
		t.Fatalf("first: %+v %v", l, err)
	}
	if _, err := s.Next(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("cut: %v", err)
	}
}

func TestParseTail(t *testing.T) {
	for in, want := range map[string]int{"": DefaultTail, "0": 0, "1000": 1000, "42": 42} {
		if got, err := ParseTail(in); err != nil || got != want {
			t.Errorf("ParseTail(%q) = %d, %v", in, got, err)
		}
	}
	for _, in := range []string{"-1", "1001", "all", "1e3"} {
		if _, err := ParseTail(in); err == nil {
			t.Errorf("ParseTail(%q) accepted", in)
		}
	}
}
