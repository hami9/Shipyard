// Package metrics keeps counters, gauges and histograms and writes them in
// the Prometheus text exposition format, version 0.0.4 [PROM-TEXT]. It is
// the few parts of a client library Shipyard needs (ADR-0013).
package metrics

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hami9/shipyard/internal/buildinfo"
)

// ContentType is the text format's media type [PROM-TEXT].
const ContentType = "text/plain; version=0.0.4; charset=utf-8"

var nameRE = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)

// Registry holds metric families in registration order. Its methods panic on
// a programming error (an invalid or repeated name), as at startup only.
type Registry struct {
	mu       sync.Mutex
	families []family
	names    map[string]bool
	scrape   []func(context.Context)
}

type family interface {
	write(w *bufio.Writer)
}

func (r *Registry) add(name string, labels []string, f family) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.names == nil {
		r.names = map[string]bool{}
	}
	if !nameRE.MatchString(name) || r.names[name] {
		panic(fmt.Sprintf("metrics: invalid or repeated name %q", name))
	}
	for _, l := range labels {
		if !nameRE.MatchString(l) || strings.HasPrefix(l, "__") || l == "le" {
			panic(fmt.Sprintf("metrics: invalid label %q on %s", l, name))
		}
	}
	r.names[name] = true
	r.families = append(r.families, f)
}

// OnScrape runs fn before every exposition, to refresh gauges that are read
// from elsewhere (the database). Concurrent scrapes run fn concurrently.
func (r *Registry) OnScrape(fn func(context.Context)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.scrape = append(r.scrape, fn)
}

// Write runs the OnScrape functions and writes every family.
func (r *Registry) Write(ctx context.Context, w *bufio.Writer) error {
	r.mu.Lock()
	scrape, families := slices.Clone(r.scrape), slices.Clone(r.families)
	r.mu.Unlock()
	for _, fn := range scrape {
		fn(ctx)
	}
	for _, f := range families {
		f.write(w)
	}
	return w.Flush()
}

// Handler serves GET /metrics.
func (r *Registry) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", ContentType)
		_ = r.Write(req.Context(), bufio.NewWriter(w))
	})
	return mux
}

// Serve serves the registry on addr, a loopback host:port the config has
// checked, until the returned server is closed.
func Serve(addr string, r *Registry, log *slog.Logger) (*http.Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("metrics listener: %w", err)
	}
	srv := &http.Server{Handler: r.Handler(), ReadHeaderTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("metrics listener stopped", slog.Any("err", err))
		}
	}()
	log.Info("metrics ready", slog.String("addr", ln.Addr().String()))
	return srv, nil
}

// BuildInfo registers shipyard_build_info, always 1, labelled with this
// binary's version and commit.
func BuildInfo(r *Registry) {
	info := buildinfo.Get()
	r.NewGauge("shipyard_build_info", "The running Shipyard version.", "version", "commit").Set(1, info.Version, info.Commit)
}

// vec is the label-value bookkeeping every kind shares.
type vec[T any] struct {
	name, help, kind string
	labels           []string
	mu               sync.Mutex
	series           map[string]*T
	values           map[string][]string // key -> label values
	make             func() *T
}

func newVec[T any](name, help, kind string, labels []string, mk func() *T) *vec[T] {
	return &vec[T]{name: name, help: help, kind: kind, labels: labels,
		series: map[string]*T{}, values: map[string][]string{}, make: mk}
}

// get returns the series for the label values, creating it; mu must be held.
func (v *vec[T]) get(values []string) *T {
	if len(values) != len(v.labels) {
		panic(fmt.Sprintf("metrics: %s takes %d label values, got %d", v.name, len(v.labels), len(values)))
	}
	key := strings.Join(values, "\xff")
	s, ok := v.series[key]
	if !ok {
		s = v.make()
		v.series[key] = s
		v.values[key] = slices.Clone(values)
	}
	return s
}

// each calls fn for every series in label order; mu must be held.
func (v *vec[T]) each(fn func(values []string, s *T)) {
	keys := make([]string, 0, len(v.series))
	for k := range v.series {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		fn(v.values[k], v.series[k])
	}
}

func (v *vec[T]) header(w *bufio.Writer) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", v.name, escapeHelp(v.help), v.name, v.kind)
}

// Counter is a count that only goes up, per label values.
type Counter struct{ v *vec[float64] }

// NewCounter registers a counter; its name should end in _total.
func (r *Registry) NewCounter(name, help string, labels ...string) *Counter {
	c := &Counter{newVec(name, help, "counter", labels, func() *float64 { return new(float64) })}
	r.add(name, labels, c)
	return c
}

// Add adds n (≥ 0) to the series with these label values.
func (c *Counter) Add(n float64, values ...string) {
	if n < 0 || math.IsNaN(n) {
		panic("metrics: a counter cannot go down")
	}
	c.v.mu.Lock()
	defer c.v.mu.Unlock()
	*c.v.get(values) += n
}

// Inc adds 1.
func (c *Counter) Inc(values ...string) { c.Add(1, values...) }

func (c *Counter) write(w *bufio.Writer) {
	c.v.mu.Lock()
	defer c.v.mu.Unlock()
	c.v.header(w)
	c.v.each(func(values []string, s *float64) { sample(w, c.v.name, c.v.labels, values, "", *s) })
}

// Gauge is a value that goes up and down, per label values.
type Gauge struct{ v *vec[float64] }

// NewGauge registers a gauge.
func (r *Registry) NewGauge(name, help string, labels ...string) *Gauge {
	g := &Gauge{newVec(name, help, "gauge", labels, func() *float64 { return new(float64) })}
	r.add(name, labels, g)
	return g
}

// Set sets the series with these label values.
func (g *Gauge) Set(n float64, values ...string) {
	g.v.mu.Lock()
	defer g.v.mu.Unlock()
	*g.v.get(values) = n
}

// Reset drops every series, so label values that no longer exist (a
// deleted app) stop being exposed.
func (g *Gauge) Reset() {
	g.v.mu.Lock()
	defer g.v.mu.Unlock()
	clear(g.v.series)
	clear(g.v.values)
}

// Replace drops every series and keeps those fn sets, in one step: a
// scrape sees the old set or the new one, never a mix.
func (g *Gauge) Replace(fn func(set func(n float64, values ...string))) {
	g.v.mu.Lock()
	defer g.v.mu.Unlock()
	clear(g.v.series)
	clear(g.v.values)
	fn(func(n float64, values ...string) { *g.v.get(values) = n })
}

func (g *Gauge) write(w *bufio.Writer) {
	g.v.mu.Lock()
	defer g.v.mu.Unlock()
	g.v.header(w)
	g.v.each(func(values []string, s *float64) { sample(w, g.v.name, g.v.labels, values, "", *s) })
}

// Histogram counts observations into cumulative buckets, per label values.
type Histogram struct {
	v      *vec[histogram]
	bounds []float64
}

type histogram struct {
	counts []uint64 // per bound, not cumulative; the last is +Inf
	sum    float64
	count  uint64
}

// NewHistogram registers a histogram with these upper bounds, which must
// increase; the +Inf bucket is implied.
func (r *Registry) NewHistogram(name, help string, bounds []float64, labels ...string) *Histogram {
	if !slices.IsSorted(bounds) || slices.Contains(bounds, math.Inf(1)) || len(slices.Compact(slices.Clone(bounds))) != len(bounds) {
		panic(fmt.Sprintf("metrics: %s needs strictly increasing finite bounds", name))
	}
	h := &Histogram{bounds: slices.Clone(bounds)}
	h.v = newVec(name, help, "histogram", labels, func() *histogram {
		return &histogram{counts: make([]uint64, len(bounds)+1)}
	})
	r.add(name, labels, h)
	return h
}

// Observe records one value in the series with these label values.
func (h *Histogram) Observe(x float64, values ...string) {
	i, _ := slices.BinarySearch(h.bounds, x) // the first bound ≥ x: le is inclusive
	h.v.mu.Lock()
	defer h.v.mu.Unlock()
	s := h.v.get(values)
	s.counts[i]++
	s.count++
	s.sum += x
}

func (h *Histogram) write(w *bufio.Writer) {
	h.v.mu.Lock()
	defer h.v.mu.Unlock()
	h.v.header(w)
	labels := append(slices.Clone(h.v.labels), "le")
	h.v.each(func(values []string, s *histogram) {
		var cum uint64
		for i, n := range s.counts {
			cum += n
			le := "+Inf"
			if i < len(h.bounds) {
				le = formatFloat(h.bounds[i])
			}
			sample(w, h.v.name, labels, append(slices.Clone(values), le), "_bucket", float64(cum))
		}
		sample(w, h.v.name, h.v.labels, values, "_sum", s.sum)
		sample(w, h.v.name, h.v.labels, values, "_count", float64(s.count))
	})
}

func sample(w *bufio.Writer, name string, labels, values []string, suffix string, x float64) {
	w.WriteString(name)
	w.WriteString(suffix)
	if len(labels) > 0 {
		w.WriteByte('{')
		for i, l := range labels {
			if i > 0 {
				w.WriteByte(',')
			}
			fmt.Fprintf(w, `%s="%s"`, l, escapeLabel(values[i]))
		}
		w.WriteByte('}')
	}
	w.WriteByte(' ')
	w.WriteString(formatFloat(x))
	w.WriteByte('\n')
}

func formatFloat(x float64) string {
	switch {
	case math.IsInf(x, 1):
		return "+Inf"
	case math.IsInf(x, -1):
		return "-Inf"
	case math.IsNaN(x):
		return "NaN"
	}
	return strconv.FormatFloat(x, 'g', -1, 64)
}

// [PROM-TEXT]: help escapes \ and line feed; label values also escape ".
var (
	helpEscaper  = strings.NewReplacer(`\`, `\\`, "\n", `\n`)
	labelEscaper = strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`)
)

func escapeHelp(s string) string  { return helpEscaper.Replace(s) }
func escapeLabel(s string) string { return labelEscaper.Replace(s) }
