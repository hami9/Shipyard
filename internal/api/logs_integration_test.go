//go:build integration

package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hami9/shipyard/internal/applogs"
)

type fakeLogSource struct {
	mu    sync.Mutex // the handler runs on the server's goroutines
	req   string
	lines []applogs.Line
	err   error
	hold  chan struct{} // closed when the stream should end; nil ends at once
}

func (f *fakeLogSource) set(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

func (f *fakeLogSource) asked() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.req
}

func (f *fakeLogSource) Logs(_ context.Context, appID string, tail int, follow bool) (LogStream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.req = appID + " " + map[bool]string{true: "follow", false: "tail"}[follow] + " " + strings.Repeat("|", min(tail, 5))
	if f.err != nil {
		return nil, f.err
	}
	return &fakeLogStream{lines: f.lines, hold: f.hold}, nil
}

type fakeLogStream struct {
	lines []applogs.Line
	hold  chan struct{}
}

func (s *fakeLogStream) Next() (applogs.Line, error) {
	if len(s.lines) == 0 {
		if s.hold != nil {
			<-s.hold
			return applogs.Line{}, io.ErrUnexpectedEOF
		}
		return applogs.Line{End: applogs.EndTail}, nil
	}
	l := s.lines[0]
	s.lines = s.lines[1:]
	return l, nil
}

func (s *fakeLogStream) Close() error { return nil }

// P2.7b: the API proxies the worker's log stream as SSE (ADR-0008).
func TestAppLogs(t *testing.T) {
	f := newAPIFixture(t)
	r := f.json("POST", "/v1/apps", `{"slug":"web","repo":"hami9/demo","branch":"main","port":3000}`)
	if r.status != 201 {
		t.Fatalf("setup: %d %s", r.status, r.raw)
	}
	appID := r.body["id"].(string)
	src := &fakeLogSource{lines: []applogs.Line{
		{TS: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC), Stream: "stderr", Text: "boom [REDACTED]"},
	}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(conform(t, NewHandler(log, Deps{Tokens: f.s, Audit: f.s, Apps: f.s, Ops: f.s, Logs: src,
		StreamKeepalive: 100 * time.Millisecond})))
	defer srv.Close()
	get := func(path, token string) (int, string, http.Header) {
		t.Helper()
		req, _ := http.NewRequestWithContext(t.Context(), "GET", srv.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b), res.Header
	}

	code, body, hdr := get("/v1/apps/web/logs?tail=3", f.reader)
	want := ": logs\n\n" +
		`data: {"ts":"2026-09-28T12:00:00Z","stream":"stderr","line":"boom [REDACTED]"}` + "\n\n" +
		"event: end\ndata: {\"reason\":\"" + applogs.EndTail + "\"}\n\n"
	if code != 200 || body != want || hdr.Get("Content-Type") != "text/event-stream" || src.asked() != appID+" tail |||" {
		t.Fatalf("logs: %d %q (asked %q)", code, body, src.asked())
	}

	// Following: keepalives while quiet; a worker that goes away ends it.
	hold := make(chan struct{})
	src.set(func() { src.lines, src.hold = nil, hold })
	time.AfterFunc(350*time.Millisecond, func() { close(hold) })
	code, body, _ = get("/v1/apps/web/logs?follow=true&tail=0", f.reader)
	if code != 200 || !strings.Contains(body, ": keepalive\n\n") || !strings.HasSuffix(body, "event: end\ndata: {\"reason\":\"the worker stopped streaming\"}\n\n") ||
		src.asked() != appID+" follow " {
		t.Fatalf("follow: %d %q (asked %q)", code, body, src.asked())
	}

	src.set(func() { src.hold = nil })
	for _, tt := range []struct {
		name, path, token string
		err               error
		want              int
	}{
		{"no deployment", "/v1/apps/web/logs", f.reader, applogs.ErrNoDeployment, 404},
		{"worker down", "/v1/apps/web/logs", f.reader, applogs.ErrUnavailable, 503},
		{"worker error", "/v1/apps/web/logs", f.reader, errors.New("worker log service: 500"), 502},
		{"tail too big", "/v1/apps/web/logs?tail=1001", f.reader, nil, 422},
		{"bad follow", "/v1/apps/web/logs?follow=maybe", f.reader, nil, 422},
		{"unknown app", "/v1/apps/nope/logs", f.reader, nil, 404},
		{"no token", "/v1/apps/web/logs", "", nil, 401},
	} {
		t.Run(tt.name, func(t *testing.T) {
			src.set(func() { src.err = tt.err })
			if code, body, hdr := get(tt.path, tt.token); code != tt.want || hdr.Get("Content-Type") != "application/problem+json" {
				t.Fatalf("%d %s, want %d", code, body, tt.want)
			}
		})
	}

	// Without a worker socket configured, logs are unavailable.
	bare := httptest.NewServer(conform(t, NewHandler(log, Deps{Tokens: f.s, Audit: f.s, Apps: f.s, Ops: f.s})))
	defer bare.Close()
	req, _ := http.NewRequest("GET", bare.URL+"/v1/apps/web/logs", nil)
	req.Header.Set("Authorization", "Bearer "+f.reader)
	if res, err := http.DefaultClient.Do(req); err != nil || res.StatusCode != 503 {
		t.Fatalf("no source: %v %v", res, err)
	} else {
		res.Body.Close()
	}
}
