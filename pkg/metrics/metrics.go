// Package metrics is the Prometheus exposition of the portal backend (PB-13, Q-B10 of doc/06): a few counters,
// one histogram and gauge callbacks, rendered in the Prometheus text format (version 0.0.4) by hand so that no
// client library is needed. Values are process-local; the endpoint is served by Server on a separate,
// internal-only port and never by the public listener.
//
// Names are prefixed `materm_portal_` and are snake_case, like the counters of the admin backend's
// pkg/metrics (redis_degraded_events, authz_denied, ...). Label values are bounded on purpose: the route is the
// matched route TEMPLATE (never the concrete path, so no slug or id), the method is one of a fixed set and the
// status is a class (2xx ... 5xx). No label ever carries an IP, a key, a token or a visitor value.
package metrics

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Namespace prefixes every metric.
const Namespace = "materm_portal_"

// maxSeries bounds the number of label combinations of one family; further combinations are dropped
// (counted in materm_portal_metrics_dropped_series_total) so a bug can never grow memory without limit.
const maxSeries = 512

// Sample is one line of a gauge or counter callback.
type Sample struct {
	Labels map[string]string
	Value  float64
}

// Registry holds the families. The zero value is not usable; use NewRegistry. Default is the process registry.
type Registry struct {
	mu         sync.RWMutex
	counters   map[string]*CounterVec
	histograms map[string]*HistogramVec
	funcs      map[string]*funcFamily
	dropped    atomic.Uint64
}

type funcFamily struct {
	help    string
	kind    string // "gauge" or "counter"
	collect func() []Sample
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{counters: map[string]*CounterVec{}, histograms: map[string]*HistogramVec{}, funcs: map[string]*funcFamily{}}
}

// Counter returns the counter family `name` (created on first use; later calls ignore help and labels).
func (r *Registry) Counter(name, help string, labels ...string) *CounterVec {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.counters[name]; ok {
		return c
	}
	c := &CounterVec{reg: r, name: name, help: help, labels: labels, series: map[string]*counterSeries{}}
	r.counters[name] = c
	return c
}

// Histogram returns the histogram family `name` with the given upper bounds in seconds (ascending).
func (r *Registry) Histogram(name, help string, buckets []float64, labels ...string) *HistogramVec {
	r.mu.Lock()
	defer r.mu.Unlock()
	if h, ok := r.histograms[name]; ok {
		return h
	}
	h := &HistogramVec{reg: r, name: name, help: help, labels: labels, buckets: buckets, series: map[string]*histSeries{}}
	r.histograms[name] = h
	return h
}

// GaugeFunc registers (or replaces) a family whose samples are produced at scrape time. kind is "gauge" or
// "counter" (a counter callback must be monotonic, e.g. the trips of the Redis breaker).
func (r *Registry) GaugeFunc(name, help, kind string, collect func() []Sample) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.funcs[name] = &funcFamily{help: help, kind: kind, collect: collect}
}

// CounterVec is a monotonic counter with labels.
type CounterVec struct {
	reg    *Registry
	name   string
	help   string
	labels []string
	mu     sync.RWMutex
	series map[string]*counterSeries
}

type counterSeries struct {
	values []string
	n      atomic.Uint64
}

// Inc adds 1 to the series with these label values (in the order of the declared labels).
func (c *CounterVec) Inc(values ...string) { c.Add(1, values...) }

// Add adds n.
func (c *CounterVec) Add(n uint64, values ...string) {
	if len(values) != len(c.labels) {
		return
	}
	key := strings.Join(values, "\xff")
	c.mu.RLock()
	s := c.series[key]
	c.mu.RUnlock()
	if s == nil {
		c.mu.Lock()
		if s = c.series[key]; s == nil {
			if len(c.series) >= maxSeries {
				c.mu.Unlock()
				c.reg.dropped.Add(1)
				return
			}
			s = &counterSeries{values: append([]string(nil), values...)}
			c.series[key] = s
		}
		c.mu.Unlock()
	}
	s.n.Add(n)
}

// Value returns the current value of one series (0 when it does not exist). Tests use it.
func (c *CounterVec) Value(values ...string) uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if s := c.series[strings.Join(values, "\xff")]; s != nil {
		return s.n.Load()
	}
	return 0
}

// HistogramVec is a cumulative histogram with labels.
type HistogramVec struct {
	reg     *Registry
	name    string
	help    string
	labels  []string
	buckets []float64
	mu      sync.Mutex
	series  map[string]*histSeries
}

type histSeries struct {
	values []string
	counts []uint64 // per bucket (not cumulative); the last slot is +Inf
	sum    float64
	count  uint64
}

// Observe records one value in seconds.
func (h *HistogramVec) Observe(seconds float64, values ...string) {
	if len(values) != len(h.labels) {
		return
	}
	key := strings.Join(values, "\xff")
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.series[key]
	if s == nil {
		if len(h.series) >= maxSeries {
			h.reg.dropped.Add(1)
			return
		}
		s = &histSeries{values: append([]string(nil), values...), counts: make([]uint64, len(h.buckets)+1)}
		h.series[key] = s
	}
	i := sort.SearchFloat64s(h.buckets, seconds) // first bound >= seconds
	s.counts[i]++
	s.sum += seconds
	s.count++
}

// Count returns the number of observations of one series.
func (h *HistogramVec) Count(values ...string) uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s := h.series[strings.Join(values, "\xff")]; s != nil {
		return s.count
	}
	return 0
}

// Write renders every family in the Prometheus text format, sorted by name so the output is stable.
func (r *Registry) Write(w io.Writer) error {
	r.mu.RLock()
	names := make([]string, 0, len(r.counters)+len(r.histograms)+len(r.funcs)+1)
	for n := range r.counters {
		names = append(names, n)
	}
	for n := range r.histograms {
		names = append(names, n)
	}
	for n := range r.funcs {
		names = append(names, n)
	}
	r.mu.RUnlock()
	sort.Strings(names)

	var b strings.Builder
	for _, n := range names {
		r.mu.RLock()
		c, h, f := r.counters[n], r.histograms[n], r.funcs[n]
		r.mu.RUnlock()
		switch {
		case c != nil:
			c.write(&b)
		case h != nil:
			h.write(&b)
		case f != nil:
			f.write(&b, n)
		}
	}
	head(&b, Namespace+"metrics_dropped_series_total", "Label combinations refused because a family reached its series limit.", "counter")
	fmt.Fprintf(&b, "%smetrics_dropped_series_total %d\n", Namespace, r.dropped.Load())
	_, err := io.WriteString(w, b.String())
	return err
}

func head(b *strings.Builder, name, help, kind string) {
	fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s %s\n", name, escapeHelp(help), name, kind)
}

func (c *CounterVec) write(b *strings.Builder) {
	head(b, c.name, c.help, "counter")
	c.mu.RLock()
	defer c.mu.RUnlock()
	keys := sortedKeys(c.series)
	for _, k := range keys {
		s := c.series[k]
		fmt.Fprintf(b, "%s%s %d\n", c.name, labelText(c.labels, s.values, "", ""), s.n.Load())
	}
}

func (h *HistogramVec) write(b *strings.Builder) {
	head(b, h.name, h.help, "histogram")
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, k := range sortedKeys(h.series) {
		s := h.series[k]
		var cum uint64
		for i, ub := range h.buckets {
			cum += s.counts[i]
			fmt.Fprintf(b, "%s_bucket%s %d\n", h.name, labelText(h.labels, s.values, "le", formatFloat(ub)), cum)
		}
		cum += s.counts[len(h.buckets)]
		fmt.Fprintf(b, "%s_bucket%s %d\n", h.name, labelText(h.labels, s.values, "le", "+Inf"), cum)
		fmt.Fprintf(b, "%s_sum%s %s\n", h.name, labelText(h.labels, s.values, "", ""), formatFloat(s.sum))
		fmt.Fprintf(b, "%s_count%s %d\n", h.name, labelText(h.labels, s.values, "", ""), s.count)
	}
}

func (f *funcFamily) write(b *strings.Builder, name string) {
	head(b, name, f.help, f.kind)
	samples := f.collect()
	lines := make([]string, 0, len(samples))
	for _, s := range samples {
		names := make([]string, 0, len(s.Labels))
		for k := range s.Labels {
			names = append(names, k)
		}
		sort.Strings(names)
		vals := make([]string, len(names))
		for i, k := range names {
			vals[i] = s.Labels[k]
		}
		lines = append(lines, fmt.Sprintf("%s%s %s\n", name, labelText(names, vals, "", ""), formatFloat(s.Value)))
	}
	sort.Strings(lines)
	for _, l := range lines {
		b.WriteString(l)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func labelText(names, values []string, extraName, extraValue string) string {
	if len(names) == 0 && extraName == "" {
		return ""
	}
	var b strings.Builder
	b.WriteByte('{')
	for i, n := range names {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `%s="%s"`, n, escapeLabel(values[i]))
	}
	if extraName != "" {
		if len(names) > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `%s="%s"`, extraName, extraValue)
	}
	b.WriteByte('}')
	return b.String()
}

var (
	labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	helpEscaper  = strings.NewReplacer(`\`, `\\`, "\n", `\n`)
)

func escapeLabel(s string) string { return labelEscaper.Replace(s) }
func escapeHelp(s string) string  { return helpEscaper.Replace(s) }

func formatFloat(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "+Inf"
	case math.IsInf(f, -1):
		return "-Inf"
	case math.IsNaN(f):
		return "NaN"
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}
