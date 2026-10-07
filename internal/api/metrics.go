package api

import (
	"net/http"
	"strconv"

	"github.com/hami9/shipyard/internal/metrics"
)

// Reasons for a 429 (ADR-0011), as the throttled counter labels them.
const (
	throttleRequests = "requests" // the per-client request rate
	throttleAuth     = "auth"     // too many failed authentications
)

// apiMetrics counts what the API answers (ADR-0013). A nil one counts
// nothing.
type apiMetrics struct {
	requests  *metrics.Counter
	throttled *metrics.Counter
}

func newAPIMetrics(r *metrics.Registry) *apiMetrics {
	if r == nil {
		return nil
	}
	m := &apiMetrics{
		requests: r.NewCounter("shipyard_api_requests_total",
			"Requests answered, by status class (2xx, 3xx, 4xx, 5xx).", "code"),
		throttled: r.NewCounter("shipyard_api_throttled_total",
			"Requests refused with 429, by reason: requests (the per-client rate) or auth (too many failed authentications).", "reason"),
	}
	// Every series from the start, so rate() sees a zero before the first event.
	for _, c := range []string{"2xx", "3xx", "4xx", "5xx"} {
		m.requests.Add(0, c)
	}
	m.throttled.Add(0, throttleRequests)
	m.throttled.Add(0, throttleAuth)
	return m
}

// count counts every answer by status class, 429s included.
func (m *apiMetrics) count(next http.Handler) http.Handler {
	if m == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if class := rec.status / 100; class >= 2 && class <= 5 {
			m.requests.Inc(strconv.Itoa(class) + "xx")
		}
	})
}

func (m *apiMetrics) throttle(reason string) {
	if m != nil {
		m.throttled.Inc(reason)
	}
}
