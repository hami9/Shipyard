//go:build integration

package api

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hami9/shipyard/internal/store"
)

// sseStream reads one event stream line by line.
type sseStream struct {
	t   *testing.T
	res *http.Response
	sc  *bufio.Scanner
}

func (f *apiFixture) stream(opID, token, lastEventID string) (*sseStream, response) {
	f.t.Helper()
	req, _ := http.NewRequestWithContext(f.t.Context(), "GET", f.srv.URL+"/v1/operations/"+opID+"/events", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		defer res.Body.Close()
		return nil, readResponse(res)
	}
	f.t.Cleanup(func() { res.Body.Close() })
	return &sseStream{t: f.t, res: res, sc: bufio.NewScanner(res.Body)}, response{status: res.StatusCode, header: res.Header}
}

// next returns the next event's lines (a comment is an event of its own),
// or nil at the end of the stream.
func (s *sseStream) next() []string {
	s.t.Helper()
	var lines []string
	for s.sc.Scan() {
		if s.sc.Text() == "" {
			return lines
		}
		lines = append(lines, s.sc.Text())
	}
	if len(lines) > 0 {
		s.t.Fatalf("stream ended inside an event: %q", lines)
	}
	return nil
}

// wantEvent reads until an event with an id and checks it.
func (s *sseStream) wantEvent(seq, message string) {
	s.t.Helper()
	for {
		ev := s.next()
		if ev == nil {
			s.t.Fatalf("stream ended, want event %s", seq)
		}
		if strings.HasPrefix(ev[0], ":") || strings.HasPrefix(ev[0], "retry:") {
			continue
		}
		var data eventJSON
		if len(ev) != 2 || ev[0] != "id: "+seq || json.Unmarshal([]byte(strings.TrimPrefix(ev[1], "data: ")), &data) != nil ||
			data.Message != message || data.Level != store.LevelInfo || data.TS.IsZero() {
			s.t.Fatalf("event = %q, want id %s with %q", ev, seq, message)
		}
		return
	}
}

// wantEnd reads the final event and the end of the stream.
func (s *sseStream) wantEnd(status string) {
	s.t.Helper()
	for {
		ev := s.next()
		if ev == nil {
			s.t.Fatal("stream ended without an end event")
		}
		if strings.HasPrefix(ev[0], ":") {
			continue
		}
		var op operationJSON
		if len(ev) != 2 || ev[0] != "event: end" || json.Unmarshal([]byte(strings.TrimPrefix(ev[1], "data: ")), &op) != nil || op.Status != status {
			s.t.Fatalf("end = %q, want status %s", ev, status)
		}
		if rest := s.next(); rest != nil {
			s.t.Fatalf("after the end: %q", rest)
		}
		return
	}
}

// P2.7: operation events as SSE, resumable with Last-Event-ID, with
// keepalives, ending once the operation has finished.
func TestOperationEventsStream(t *testing.T) {
	f := newAPIFixture(t)
	ctx := t.Context()
	if r := f.json("POST", "/v1/apps", `{"slug":"web","repo":"hami9/demo","branch":"main","port":3000}`); r.status != 201 {
		t.Fatalf("setup: %d %s", r.status, r.raw)
	}
	op := opField(f.deploy("web", f.deployer, "one", ""), "id").(string)
	appendEvent := func(msg string) {
		t.Helper()
		if _, err := f.s.AppendOperationEvent(ctx, op, store.LevelInfo, msg); err != nil {
			t.Fatal(err)
		}
	}
	appendEvent("fetching")
	appendEvent("line one\nline two") // JSON keeps it one data line

	s, r := f.stream(op, f.reader, "")
	if r.header.Get("Content-Type") != "text/event-stream" || r.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("headers = %v", r.header)
	}
	if ev := s.next(); len(ev) != 1 || ev[0] != "retry: 2000" {
		t.Fatalf("first = %q", ev)
	}
	s.wantEvent("1", "fetching")
	s.wantEvent("2", "line one\nline two")

	// While nothing happens, a keepalive comment arrives.
	deadline := time.Now().Add(5 * time.Second)
	for ev := s.next(); len(ev) != 1 || ev[0] != ": keepalive"; ev = s.next() {
		if ev == nil || time.Now().After(deadline) {
			t.Fatalf("no keepalive: %q", ev)
		}
	}
	// A new event is streamed live, then the operation ends (a newer
	// request cancels the queued one) and so does the stream.
	appendEvent("building")
	s.wantEvent("3", "building")
	f.deploy("web", f.deployer, "two", "")
	s.wantEnd(store.OpCancelled)

	// A reconnecting client resumes after the last id it saw.
	s, _ = f.stream(op, f.reader, "2")
	s.next() // retry
	s.wantEvent("3", "building")
	s.wantEnd(store.OpCancelled)
	s, _ = f.stream(op, f.reader, "3")
	s.next()
	s.wantEnd(store.OpCancelled)

	for _, tt := range []struct {
		name, op, token, lastID string
		want                    int
	}{
		{"bad last id", op, f.reader, "abc", 422},
		{"negative last id", op, f.reader, "-1", 422},
		{"unknown operation", "00000000-0000-4000-8000-000000000000", f.reader, "", 404},
		{"not a uuid", "nope", f.reader, "", 404},
		{"no token", op, "", "", 401},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if s, r := f.stream(tt.op, tt.token, tt.lastID); s != nil || r.status != tt.want || r.header.Get("Content-Type") != "application/problem+json" {
				t.Fatalf("status %d, want %d problem: %s", r.status, tt.want, r.raw)
			}
		})
	}
}
