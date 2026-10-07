package api

import (
	"bufio"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/hami9/shipyard/internal/metrics"
)

// P5.5b: the API counts answers by status class, 429s included, and 429s
// by reason (ADR-0011, ADR-0013).
func TestAPIMetrics(t *testing.T) {
	reg := &metrics.Registry{}
	tokens := newFakeTokens()
	tok, _ := tokens.add(ScopeRead)
	h := NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), Deps{DB: fakePinger{}, Tokens: tokens, Audit: &fakeAudit{},
		Limits: RateLimits{Rate: 1, Burst: 2, AuthFailures: 1}, Metrics: reg})

	expose := func() string {
		var b strings.Builder
		if err := reg.Write(t.Context(), bufio.NewWriter(&b)); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	if got := expose(); !strings.Contains(got, `shipyard_api_throttled_total{reason="auth"} 0`+"\n") ||
		!strings.Contains(got, `shipyard_api_requests_total{code="5xx"} 0`+"\n") {
		t.Fatalf("series are not there from the start:\n%s", got)
	}

	codes := []int{
		do(h, "GET", "/v1/whoami", withToken(fromIP("203.0.113.7"), tok)).Code,     // 200
		do(h, "GET", "/v1/whoami", withToken(fromIP("203.0.113.7"), tok)).Code,     // 200, the burst is spent
		do(h, "GET", "/v1/whoami", withToken(fromIP("203.0.113.7"), tok)).Code,     // 429: requests
		do(h, "GET", "/v1/whoami", withToken(fromIP("203.0.113.8"), "shp_x")).Code, // 401, the one failure allowed
		do(h, "GET", "/v1/whoami", withToken(fromIP("203.0.113.8"), tok)).Code,     // 429: auth
		do(h, "GET", "/healthz", nil).Code,                                         // 200, never limited
	}
	if want := []int{200, 200, 429, 401, 429, 200}; !slices.Equal(codes, want) {
		t.Fatalf("codes %v, want %v", codes, want)
	}
	got := expose()
	for _, want := range []string{
		`shipyard_api_requests_total{code="2xx"} 3`,
		`shipyard_api_requests_total{code="4xx"} 3`,
		`shipyard_api_throttled_total{reason="auth"} 1`,
		`shipyard_api_throttled_total{reason="requests"} 1`,
	} {
		if !strings.Contains(got, want+"\n") {
			t.Errorf("missing %s in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "shp_") || strings.Contains(got, "203.0.113") {
		t.Errorf("metrics name a token or a client:\n%s", got)
	}
}

// Without a registry, nothing is counted and nothing breaks.
func TestAPIMetricsOff(t *testing.T) {
	h := NewHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), Deps{DB: fakePinger{}, Tokens: newFakeTokens(), Audit: &fakeAudit{},
		Limits: RateLimits{Rate: 1, Burst: 1}})
	if rec := do(h, "GET", "/healthz", nil); rec.Code != http.StatusOK {
		t.Fatalf("healthz = %d", rec.Code)
	}
	do(h, "GET", "/v1/whoami", fromIP("203.0.113.7"))
	if rec := do(h, "GET", "/v1/whoami", fromIP("203.0.113.7")); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("over the limit = %d", rec.Code)
	}
}
