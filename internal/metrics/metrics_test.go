package metrics

import (
	"bufio"
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func expose(t *testing.T, r *Registry) string {
	t.Helper()
	var b strings.Builder
	if err := r.Write(context.Background(), bufio.NewWriter(&b)); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestExposition(t *testing.T) {
	r := &Registry{}
	c := r.NewCounter("ops_total", "Operations.\nBy kind \\ result.", "kind", "result")
	g := r.NewGauge("depth", "Queue depth.")
	h := r.NewHistogram("dur_seconds", "Duration.", []float64{1, 10}, "kind")
	c.Inc("deploy", "failed")
	c.Add(2, "deploy", "succeeded")
	c.Inc("a\"b\\c\nd", "x")
	g.Set(3)
	for _, x := range []float64{0.5, 1, 5, 11} {
		h.Observe(x, "deploy")
	}
	want := `# HELP ops_total Operations.\nBy kind \\ result.
# TYPE ops_total counter
ops_total{kind="a\"b\\c\nd",result="x"} 1
ops_total{kind="deploy",result="failed"} 1
ops_total{kind="deploy",result="succeeded"} 2
# HELP depth Queue depth.
# TYPE depth gauge
depth 3
# HELP dur_seconds Duration.
# TYPE dur_seconds histogram
dur_seconds_bucket{kind="deploy",le="1"} 2
dur_seconds_bucket{kind="deploy",le="10"} 3
dur_seconds_bucket{kind="deploy",le="+Inf"} 4
dur_seconds_sum{kind="deploy"} 17.5
dur_seconds_count{kind="deploy"} 4
`
	if got := expose(t, r); got != want {
		t.Errorf("exposition:\n%s\nwant:\n%s", got, want)
	}
}

func TestGaugeResetAndScrape(t *testing.T) {
	r := &Registry{}
	g := r.NewGauge("app_up", "Up.", "app")
	calls := 0
	r.OnScrape(func(context.Context) {
		calls++
		g.Reset()
		g.Set(1, "web")
	})
	g.Set(0, "deleted")
	got := expose(t, r)
	if calls != 1 || strings.Contains(got, "deleted") || !strings.Contains(got, `app_up{app="web"} 1`) {
		t.Errorf("calls %d, exposition:\n%s", calls, got)
	}
}

func TestGaugeReplace(t *testing.T) {
	r := &Registry{}
	g := r.NewGauge("app_up", "Up.", "app")
	g.Set(1, "gone")
	g.Replace(func(set func(float64, ...string)) { set(0, "web"); set(1, "api") })
	if got, want := expose(t, r), "# HELP app_up Up.\n# TYPE app_up gauge\napp_up{app=\"api\"} 1\napp_up{app=\"web\"} 0\n"; got != want {
		t.Errorf("exposition:\n%s\nwant:\n%s", got, want)
	}
}

func TestFormatFloat(t *testing.T) {
	for x, want := range map[float64]string{
		0: "0", 1.5: "1.5", 1e21: "1e+21", math.Inf(1): "+Inf", math.Inf(-1): "-Inf", math.NaN(): "NaN",
	} {
		if got := formatFloat(x); got != want {
			t.Errorf("formatFloat(%v) = %q, want %q", x, got, want)
		}
	}
}

func TestRegistryRejects(t *testing.T) {
	cases := map[string]func(r *Registry){
		"bad name":       func(r *Registry) { r.NewGauge("bad-name", "") },
		"repeated name":  func(r *Registry) { r.NewGauge("x", ""); r.NewCounter("x", "") },
		"reserved label": func(r *Registry) { r.NewGauge("x", "", "__x") },
		"le label":       func(r *Registry) { r.NewHistogram("x", "", []float64{1}, "le") },
		"bounds":         func(r *Registry) { r.NewHistogram("x", "", []float64{2, 1}) },
		"equal bounds":   func(r *Registry) { r.NewHistogram("x", "", []float64{1, 1}) },
		"label count":    func(r *Registry) { r.NewGauge("x", "", "a").Set(1) },
		"counter down":   func(r *Registry) { r.NewCounter("x", "").Add(-1) },
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("no panic")
				}
			}()
			fn(&Registry{})
		})
	}
}

func TestHandler(t *testing.T) {
	r := &Registry{}
	r.NewGauge("up", "Up.").Set(1)
	srv := httptest.NewServer(r.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != ContentType {
		t.Errorf("GET /metrics: %d %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	for _, req := range []struct{ method, path string }{{"POST", "/metrics"}, {"GET", "/"}} {
		rq, _ := http.NewRequest(req.method, srv.URL+req.path, nil)
		resp, err := http.DefaultClient.Do(rq)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode < 400 {
			t.Errorf("%s %s: %d, want an error", req.method, req.path, resp.StatusCode)
		}
	}
}
