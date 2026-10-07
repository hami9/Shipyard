package api

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hami9/shipyard/internal/logging"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time      { return c.t }
func (c *fakeClock) add(d time.Duration) { c.t = c.t.Add(d) }
func newClock() *fakeClock               { return &fakeClock{t: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)} }
func fromIP(ip string) http.Header       { return http.Header{"X-Forwarded-For": {ip}} }
func withToken(h http.Header, t string) http.Header {
	h.Set("Authorization", "Bearer "+t)
	return h
}

// limitedHandler is the real handler with limits and a fake clock.
func limitedHandler(t *testing.T, l RateLimits) (http.Handler, *fakeTokens, *fakeClock, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	log := logging.New(&buf, slog.LevelDebug, logging.FormatJSON)
	tokens := newFakeTokens()
	clock := newClock()
	lim := newLimiter(log, l)
	lim.now = clock.now
	mux := http.NewServeMux()
	auth := &authenticator{log: log, tokens: tokens, audit: &fakeAudit{}}
	mux.Handle("GET /v1/whoami", auth.protect(ScopeRead, http.HandlerFunc(handleWhoami)))
	mux.HandleFunc("GET /healthz", handleHealthz)
	return lim.limit(mux), tokens, clock, &buf
}

func TestRateLimitRequests(t *testing.T) {
	h, tokens, clock, _ := limitedHandler(t, RateLimits{Rate: 2, Burst: 3})
	tok, _ := tokens.add(ScopeRead)
	for i := range 3 {
		if rec := do(h, "GET", "/v1/whoami", withToken(fromIP("203.0.113.7"), tok)); rec.Code != http.StatusOK {
			t.Fatalf("request %d = %d, want 200 within the burst", i, rec.Code)
		}
	}
	rec := do(h, "GET", "/v1/whoami", withToken(fromIP("203.0.113.7"), tok))
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "1" ||
		rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("over the burst = %d, Retry-After %q, %q", rec.Code, rec.Header().Get("Retry-After"), rec.Header().Get("Content-Type"))
	}
	// Another client has its own bucket; the health check is never limited.
	if rec := do(h, "GET", "/v1/whoami", withToken(fromIP("203.0.113.8"), tok)); rec.Code != http.StatusOK {
		t.Fatalf("other client = %d", rec.Code)
	}
	if rec := do(h, "GET", "/healthz", fromIP("203.0.113.7")); rec.Code != http.StatusOK {
		t.Fatalf("healthz = %d", rec.Code)
	}
	// 2 per second: half a second refills one request.
	clock.add(500 * time.Millisecond)
	if rec := do(h, "GET", "/v1/whoami", withToken(fromIP("203.0.113.7"), tok)); rec.Code != http.StatusOK {
		t.Fatalf("after refill = %d", rec.Code)
	}
}

func TestRateLimitAuthFailures(t *testing.T) {
	h, tokens, clock, logs := limitedHandler(t, RateLimits{AuthFailures: 3, AuthWindow: 3 * time.Minute})
	tok, _ := tokens.add(ScopeRead)
	bad := withToken(fromIP("198.51.100.1"), "shp_"+strings.Repeat("A", 43))
	for i := range 3 {
		if rec := do(h, "GET", "/v1/whoami", bad.Clone()); rec.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d = %d, want 401", i, rec.Code)
		}
	}
	lookups := tokens.lookups
	// Out of failures: even a valid token is refused, before any lookup.
	rec := do(h, "GET", "/v1/whoami", withToken(fromIP("198.51.100.1"), tok))
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "60" {
		t.Fatalf("after 3 failures = %d, Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if tokens.lookups != lookups {
		t.Fatal("a throttled request reached the token store")
	}
	if !strings.Contains(logs.String(), "client throttled after failed authentications") ||
		!strings.Contains(logs.String(), `"client":"198.51.100.1"`) {
		t.Fatalf("throttling not logged: %s", logs)
	}
	// Other clients are unaffected.
	if rec := do(h, "GET", "/v1/whoami", withToken(fromIP("198.51.100.2"), tok)); rec.Code != http.StatusOK {
		t.Fatalf("other client = %d", rec.Code)
	}
	// One failure's worth of the window later, one attempt is allowed again.
	clock.add(time.Minute)
	if rec := do(h, "GET", "/v1/whoami", withToken(fromIP("198.51.100.1"), tok)); rec.Code != http.StatusOK {
		t.Fatalf("after a minute = %d", rec.Code)
	}
}

func TestRateLimitMissingCredentialsCount(t *testing.T) {
	h, _, _, _ := limitedHandler(t, RateLimits{AuthFailures: 2})
	for range 2 {
		do(h, "GET", "/v1/whoami", fromIP("192.0.2.9"))
	}
	if rec := do(h, "GET", "/v1/whoami", fromIP("192.0.2.9")); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("after 2 anonymous 401s = %d, want 429", rec.Code)
	}
}

func TestRateLimitOff(t *testing.T) {
	h, _, _, _ := limitedHandler(t, RateLimits{})
	for range 100 {
		if rec := do(h, "GET", "/v1/whoami", fromIP("192.0.2.1")); rec.Code != http.StatusUnauthorized {
			t.Fatalf("with limits off = %d, want 401", rec.Code)
		}
	}
}

func TestClientKey(t *testing.T) {
	for _, tc := range []struct {
		xff        []string
		remote     string
		want, name string
	}{
		{nil, "192.0.2.5:4000", "192.0.2.5", "peer address"},
		{nil, "@", "local", "unix socket peer"},
		{nil, "", "local", "no peer"},
		{[]string{"203.0.113.9"}, "@", "203.0.113.9", "Caddy's header"},
		{[]string{"10.0.0.1, 203.0.113.9"}, "@", "203.0.113.9", "last entry wins"},
		{[]string{"10.0.0.1", "203.0.113.9"}, "@", "203.0.113.9", "last header wins"},
		{[]string{"2001:db8:1:2:aaaa::1"}, "@", "2001:db8:1:2::/64", "IPv6 per /64"},
		{[]string{"2001:db8:1:2:bbbb::7"}, "@", "2001:db8:1:2::/64", "same /64"},
		{[]string{"::ffff:198.51.100.4"}, "@", "198.51.100.4", "IPv4-mapped"},
		{[]string{"fe80::1%eth0"}, "@", "fe80::/64", "zone dropped"},
		{[]string{"not-an-ip"}, "127.0.0.1:5000", "127.0.0.1", "bad header falls back"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tc.remote
			for _, v := range tc.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			if got := clientKey(r); got != tc.want {
				t.Fatalf("clientKey = %q, want %q", got, tc.want)
			}
		})
	}
}

// The client map never grows past maxClients (+1 shared overflow bucket);
// idle clients are swept before new ones overflow.
func TestRateLimitBoundedClients(t *testing.T) {
	lim := newLimiter(slog.New(slog.DiscardHandler), RateLimits{Rate: 1, Burst: 1})
	clock := newClock()
	lim.now = clock.now
	for i := range maxClients + 50 {
		lim.allow(fmt.Sprintf("10.%d.%d.%d", i>>16&255, i>>8&255, i&255))
	}
	if n := len(lim.clients); n != maxClients+1 {
		t.Fatalf("clients = %d, want %d", n, maxClients+1)
	}
	if _, _, ok := lim.allow("192.0.2.200"); ok {
		t.Fatal("a new client while full did not share the spent overflow bucket")
	}
	clock.add(2 * time.Second) // every bucket full again
	if _, _, ok := lim.allow("192.0.2.201"); !ok || len(lim.clients) != 1 {
		t.Fatalf("after idle sweep: ok=%v clients=%d", ok, len(lim.clients))
	}
}
