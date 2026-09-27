package health

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fast(url string) Config {
	return Config{URL: url, Timeout: 2 * time.Second, Interval: 5 * time.Millisecond, Successes: 3, RequestTimeout: time.Second}
}

func running(context.Context) error { return nil }

// statuses serves the given codes in order, then repeats the last one.
func statuses(t *testing.T, codes ...int) (*httptest.Server, *atomic.Int32) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := int(n.Add(1)) - 1
		code := codes[min(i, len(codes)-1)]
		if code == http.StatusFound {
			http.Redirect(w, r, "/elsewhere", code)
			return
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func TestPassesAfterConsecutiveSuccesses(t *testing.T) {
	// A failure resets the count: 200, 500, then three 200s.
	srv, n := statuses(t, 200, 500, 200, 204, 200, 500)
	if err := Wait(t.Context(), fast(srv.URL+"/healthz"), running); err != nil {
		t.Fatal(err)
	}
	if got := n.Load(); got != 5 {
		t.Fatalf("probes = %d, want 5", got)
	}
}

// A 3xx passes and is not followed.
func TestRedirectPasses(t *testing.T) {
	srv, n := statuses(t, http.StatusFound)
	if err := Wait(t.Context(), fast(srv.URL), running); err != nil {
		t.Fatal(err)
	}
	if got := n.Load(); got != 3 {
		t.Fatalf("requests = %d, want 3 (redirects followed?)", got)
	}
}

func TestUnhealthyTimesOut(t *testing.T) {
	srv, _ := statuses(t, 500)
	c := fast(srv.URL + "/ready")
	c.Timeout = 100 * time.Millisecond
	err := Wait(t.Context(), c, running)
	if !errors.Is(err, ErrUnhealthy) || !strings.Contains(err.Error(), "GET /ready: status 500") {
		t.Fatalf("err = %v", err)
	}
}

func TestConnectionRefused(t *testing.T) {
	srv, _ := statuses(t, 200)
	url := srv.URL
	srv.Close()
	c := fast(url)
	c.Timeout = 100 * time.Millisecond
	if err := Wait(t.Context(), c, running); !errors.Is(err, ErrUnhealthy) || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("err = %v", err)
	}
}

// A slow endpoint fails each probe at the request timeout.
func TestRequestTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	c := fast(srv.URL)
	c.Timeout, c.RequestTimeout = 300*time.Millisecond, 20*time.Millisecond
	if err := Wait(t.Context(), c, running); !errors.Is(err, ErrUnhealthy) {
		t.Fatalf("err = %v", err)
	}
}

// The container dying ends the gate at once, even while probes pass.
func TestDeadContainerStopsTheGate(t *testing.T) {
	srv, _ := statuses(t, 200)
	exited := errors.New("container exited with code 1")
	calls := 0
	start := time.Now()
	err := Wait(t.Context(), fast(srv.URL), func(context.Context) error {
		if calls++; calls == 2 {
			return exited
		}
		return nil
	})
	if !errors.Is(err, exited) || time.Since(start) > time.Second {
		t.Fatalf("err = %v after %s", err, time.Since(start))
	}
}

// Cancellation by the caller (e.g. a lost lease) is reported as its cause,
// not as an unhealthy candidate.
func TestCallerCancellation(t *testing.T) {
	srv, _ := statuses(t, 500)
	lost := errors.New("lease lost")
	ctx, cancel := context.WithCancelCause(t.Context())
	time.AfterFunc(30*time.Millisecond, func() { cancel(lost) })
	if err := Wait(ctx, fast(srv.URL), running); !errors.Is(err, lost) {
		t.Fatalf("err = %v", err)
	}
}
