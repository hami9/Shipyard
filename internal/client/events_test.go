package client

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
)

// A dropped stream is resumed with Last-Event-ID: nothing is lost or seen
// twice, and the end event returns the operation.
func TestFollowEventsResumes(t *testing.T) {
	var conns atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/operations/op-1/events" || r.Header.Get("Authorization") != "Bearer shp_test" {
			t.Errorf("%s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch conns.Add(1) {
		case 1:
			if id := r.Header.Get("Last-Event-ID"); id != "" {
				t.Errorf("first Last-Event-ID = %q", id)
			}
			fmt.Fprint(w, "retry: 10\n\n: keepalive\n\n")
			fmt.Fprint(w, "id: 1\ndata: {\"seq\":1,\"level\":\"info\",\"message\":\"fetch\"}\n\n")
			fmt.Fprint(w, "id: 2\ndata: {\"seq\":2,\"level\":\"warn\",\n") // split data lines join with \n
			fmt.Fprint(w, "data: \"message\":\"slow\"}\n\n")
			fmt.Fprint(w, "id: 3\ndata: {\"seq\":3,") // cut mid-event: not delivered
		case 2:
			if id := r.Header.Get("Last-Event-ID"); id != "2" {
				t.Errorf("resume Last-Event-ID = %q, want 2", id)
			}
			fmt.Fprint(w, "id: 3\ndata: {\"seq\":3,\"level\":\"info\",\"message\":\"done\"}\n\n")
			fmt.Fprint(w, "event: end\ndata: {\"id\":\"op-1\",\"status\":\"succeeded\"}\n\n")
		default:
			t.Error("reconnected after the end")
		}
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "shp_test")
	var got []string
	op, err := c.FollowEvents(t.Context(), "op-1", 0, func(e Event) { got = append(got, fmt.Sprintf("%d %s %s", e.Seq, e.Level, e.Message)) })
	if err != nil || op.Status != "succeeded" || op.ID != "op-1" {
		t.Fatalf("op %+v, err %v", op, err)
	}
	if want := []string{"1 info fetch", "2 warn slow", "3 info done"}; !slices.Equal(got, want) {
		t.Fatalf("events = %q, want %q", got, want)
	}
}

func TestLogs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/web/logs" || r.URL.RawQuery != "follow=true&tail=5" {
			t.Errorf("%s?%s", r.URL.Path, r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": logs\n\n")
		fmt.Fprint(w, "data: {\"ts\":\"2026-09-28T12:00:00Z\",\"stream\":\"stderr\",\"line\":\"boom [REDACTED]\"}\n\n")
		fmt.Fprint(w, "event: end\ndata: {\"reason\":\"the container stopped\"}\n\n")
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "shp_test")
	var got []string
	reason, err := c.Logs(t.Context(), "web", 5, true, func(l LogLine) { got = append(got, l.Stream+" "+l.Line) })
	if err != nil || reason != "the container stopped" || !slices.Equal(got, []string{"stderr boom [REDACTED]"}) {
		t.Fatalf("reason %q, lines %q, err %v", reason, got, err)
	}
}

// An API error is final; a server that keeps failing is given up on.
func TestFollowEventsErrors(t *testing.T) {
	var conns atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conns.Add(1)
		switch r.URL.Path {
		case "/v1/operations/gone/events":
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(404)
			fmt.Fprint(w, `{"title":"Not Found","status":404,"detail":"operation not found"}`)
		default: // accepts, then closes at once
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "retry: 1\n\n")
		}
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "shp_test")
	var apiErr *Error
	if _, err := c.FollowEvents(t.Context(), "gone", 0, func(Event) {}); !errors.As(err, &apiErr) || apiErr.Status != 404 || conns.Load() != 1 {
		t.Fatalf("err = %v after %d connections", err, conns.Load())
	}
	conns.Store(0)
	if _, err := c.FollowEvents(t.Context(), "flaky", 0, func(Event) {}); err == nil || conns.Load() != streamAttempts+1 {
		t.Fatalf("err = %v after %d connections", err, conns.Load())
	}
}
