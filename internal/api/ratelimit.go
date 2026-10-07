package api

import (
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RateLimits caps requests and failed authentications per client (P5.3).
// A zero Rate or AuthFailures turns that limit off.
type RateLimits struct {
	Rate  int // requests per second, refilled continuously
	Burst int // requests allowed at once; at least 1 when Rate is set
	// AuthFailures is how many 401s a client may cause per AuthWindow
	// before every request it sends gets 429.
	AuthFailures int
	AuthWindow   time.Duration
}

// DefaultAuthWindow spreads AuthFailures over 15 minutes.
const DefaultAuthWindow = 15 * time.Minute

// maxClients bounds the limiter's memory. Clients whose buckets are full
// again are equivalent to new ones and are dropped first; beyond that, new
// clients share one bucket, so a flood of addresses cannot grow the map.
const maxClients = 10_000

const overflowKey = "overflow"

// bucket is a token bucket: up to burst tokens, refilled at rate per
// second.
type bucket struct {
	tokens float64
	last   time.Time
}

func (b *bucket) refill(now time.Time, rate, burst float64) {
	if b.last.IsZero() {
		b.tokens, b.last = burst, now
		return
	}
	b.tokens = min(burst, b.tokens+now.Sub(b.last).Seconds()*rate)
	b.last = now
}

// wait is how long until one token is available; zero if one is.
func (b *bucket) wait(rate float64) time.Duration {
	if b.tokens >= 1 {
		return 0
	}
	return time.Duration(math.Ceil((1 - b.tokens) / rate * float64(time.Second)))
}

type client struct {
	req, auth bucket
}

type limiter struct {
	log     *slog.Logger
	now     func() time.Time
	metrics *apiMetrics // nil: not counted

	rate, burst         float64
	authRate, authBurst float64

	mu      sync.Mutex
	clients map[string]*client
}

func newLimiter(log *slog.Logger, l RateLimits) *limiter {
	lim := &limiter{log: log, now: time.Now, clients: map[string]*client{}}
	if l.Rate > 0 {
		lim.rate, lim.burst = float64(l.Rate), float64(max(l.Burst, 1))
	}
	if l.AuthFailures > 0 {
		window := l.AuthWindow
		if window <= 0 {
			window = DefaultAuthWindow
		}
		lim.authBurst = float64(l.AuthFailures)
		lim.authRate = lim.authBurst / window.Seconds()
	}
	return lim
}

func (l *limiter) enabled() bool { return l.rate > 0 || l.authRate > 0 }

// client returns key's state, refilled to now. The caller holds mu.
func (l *limiter) client(key string, now time.Time) *client {
	c, ok := l.clients[key]
	if !ok && len(l.clients) >= maxClients {
		l.sweep(now)
		if len(l.clients) >= maxClients {
			key = overflowKey
			c, ok = l.clients[key]
		}
	}
	if !ok {
		c = &client{}
		l.clients[key] = c
	}
	c.req.refill(now, l.rate, l.burst)
	c.auth.refill(now, l.authRate, l.authBurst)
	return c
}

// sweep drops clients whose buckets have refilled completely.
func (l *limiter) sweep(now time.Time) {
	for k, c := range l.clients {
		c.req.refill(now, l.rate, l.burst)
		c.auth.refill(now, l.authRate, l.authBurst)
		if c.req.tokens >= l.burst && c.auth.tokens >= l.authBurst {
			delete(l.clients, k)
		}
	}
}

// allow takes one request token. It refuses, with the time to wait, a
// client out of requests or out of authentication failures.
func (l *limiter) allow(key string) (time.Duration, string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	c := l.client(key, l.now())
	if l.authRate > 0 {
		if d := c.auth.wait(l.authRate); d > 0 {
			l.metrics.throttle(throttleAuth)
			return d, "too many failed authentications; retry later", false
		}
	}
	if l.rate > 0 {
		if d := c.req.wait(l.rate); d > 0 {
			l.metrics.throttle(throttleRequests)
			return d, "too many requests; retry later", false
		}
		c.req.tokens--
	}
	return 0, "", true
}

// failed records a 401 and reports whether the client just ran out.
func (l *limiter) failed(key string) bool {
	if l.authRate <= 0 {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	c := l.client(key, l.now())
	if c.auth.tokens < 1 {
		return false
	}
	c.auth.tokens--
	return c.auth.tokens < 1
}

// limit applies the limiter to every request but the health checks, and
// counts the 401s the wrapped handler answers.
func (l *limiter) limit(next http.Handler) http.Handler {
	if !l.enabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}
		key := clientKey(r)
		if wait, detail, ok := l.allow(key); !ok {
			// Retry-After in whole seconds [RFC6585][RFC9110-RETRY].
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(wait.Seconds())))))
			writeProblem(w, http.StatusTooManyRequests, detail)
			return
		}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if rec.status == http.StatusUnauthorized && l.failed(key) {
			l.log.WarnContext(r.Context(), "client throttled after failed authentications",
				slog.String("client", key), slog.Duration("window", time.Duration(l.authBurst/l.authRate*float64(time.Second))))
		}
	})
}

// clientKey names the client a request counts against. The API listens
// only on loopback or a Unix socket (P2.8), so its peer is Caddy or a local
// process. Caddy ignores X-Forwarded-For from clients and sets it to the
// client's address [CADDY-RP], so the last entry is the client. (The config
// the worker renders sets no trusted_proxies.)
// IPv6 clients count per /64, the block a single host usually holds.
// Without the header (a local client), the peer address is used, and a
// Unix socket peer is "local".
func clientKey(r *http.Request) string {
	if v := r.Header.Values("X-Forwarded-For"); len(v) > 0 {
		all := strings.Split(v[len(v)-1], ",")
		if ip, err := netip.ParseAddr(strings.TrimSpace(all[len(all)-1])); err == nil {
			return addrKey(ip)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return addrKey(ip)
	}
	return "local"
}

func addrKey(ip netip.Addr) string {
	ip = ip.Unmap().WithZone("")
	if ip.Is4() {
		return ip.String()
	}
	return netip.PrefixFrom(ip, 64).Masked().String()
}
