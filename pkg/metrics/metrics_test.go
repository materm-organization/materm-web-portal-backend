package metrics

import (
	"bytes"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func render(t *testing.T, r *Registry) string {
	t.Helper()
	var b bytes.Buffer
	if err := r.Write(&b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestCounterAndHistogramExposition(t *testing.T) {
	r := NewRegistry()
	c := r.Counter("t_requests_total", "Requests.", "route", "status_class")
	c.Inc("/a", "2xx")
	c.Add(2, "/a", "2xx")
	c.Inc("/b", "5xx")
	h := r.Histogram("t_seconds", "Latency.", []float64{0.1, 1}, "route")
	h.Observe(0.05, "/a")
	h.Observe(0.5, "/a")
	h.Observe(7, "/a")
	r.GaugeFunc("t_gauge", "A gauge.", "gauge", func() []Sample {
		return []Sample{{Labels: map[string]string{"k": "v"}, Value: 2.5}}
	})

	out := render(t, r)
	for _, want := range []string{
		"# HELP t_requests_total Requests.\n# TYPE t_requests_total counter\n",
		`t_requests_total{route="/a",status_class="2xx"} 3` + "\n",
		`t_requests_total{route="/b",status_class="5xx"} 1` + "\n",
		"# TYPE t_seconds histogram\n",
		// cumulative buckets: <=0.1 has 1, <=1 has 2, +Inf has 3
		`t_seconds_bucket{route="/a",le="0.1"} 1` + "\n",
		`t_seconds_bucket{route="/a",le="1"} 2` + "\n",
		`t_seconds_bucket{route="/a",le="+Inf"} 3` + "\n",
		`t_seconds_count{route="/a"} 3` + "\n",
		`t_seconds_sum{route="/a"} 7.55` + "\n",
		"# TYPE t_gauge gauge\n" + `t_gauge{k="v"} 2.5` + "\n",
		"materm_portal_metrics_dropped_series_total 0\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if c.Value("/a", "2xx") != 3 || h.Count("/a") != 3 {
		t.Error("Value/Count")
	}
}

func TestLabelEscapingAndWrongArity(t *testing.T) {
	r := NewRegistry()
	c := r.Counter("t_total", "x", "route")
	c.Inc("a\"b\\c\nd")
	c.Inc("too", "many") // wrong arity is ignored, never panics
	out := render(t, r)
	if !strings.Contains(out, `t_total{route="a\"b\\c\nd"} 1`) {
		t.Errorf("label not escaped:\n%s", out)
	}
	if strings.Contains(out, "too") {
		t.Error("a call with the wrong number of label values must be ignored")
	}
}

func TestSeriesLimitDropsInsteadOfGrowing(t *testing.T) {
	r := NewRegistry()
	c := r.Counter("t_total", "x", "route")
	for i := 0; i < maxSeries+50; i++ {
		c.Inc("/r" + string(rune('a'+i%26)) + strings.Repeat("x", i))
	}
	out := render(t, r)
	if n := strings.Count(out, "t_total{"); n != maxSeries {
		t.Errorf("series = %d, want the limit %d", n, maxSeries)
	}
	if !strings.Contains(out, "materm_portal_metrics_dropped_series_total 50\n") {
		t.Errorf("dropped series not reported:\n%s", out[len(out)-200:])
	}
}

func TestStatusClassAndBoundedMethod(t *testing.T) {
	for in, want := range map[int]string{200: "2xx", 304: "3xx", 404: "4xx", 429: "4xx", 503: "5xx", 0: "other", 700: "other"} {
		if got := StatusClass(in); got != want {
			t.Errorf("StatusClass(%d)=%s want %s", in, got, want)
		}
	}
	if boundedMethod("get") != "GET" || boundedMethod("PROPFIND") != "OTHER" {
		t.Error("method label must come from a fixed set")
	}
}

func TestRequestAndHelpers(t *testing.T) {
	route := "/test/metrics/route/" + t.Name()
	before := RequestCount("GET", route, "4xx")
	rl := RateLimitedCount(route)
	Request("GET", route, 429, 20*time.Millisecond)
	Request("GET", route, 200, 2*time.Millisecond)
	if RequestCount("GET", route, "4xx") != before+1 || RequestCount("GET", route, "2xx") != 1 {
		t.Error("request counters")
	}
	if RateLimitedCount(route) != rl+1 {
		t.Error("a 429 must be counted as rate limited, a 200 must not")
	}
	out := render(t, Default)
	if !strings.Contains(out, Namespace+"http_request_duration_seconds_bucket{method=\"GET\",route=\""+route+"\",status_class=\"2xx\",le=\"0.005\"} 1") {
		t.Errorf("latency histogram of the 2 ms request:\n%s", out)
	}
}

func TestPortalCollectorsExposeTheDocumentedNames(t *testing.T) {
	RegisterRuntime()
	RegisterRedisBreaker(nil) // no Redis: gauge 0, counters 0, never a panic
	RegisterDBPool(nil)       // nil pool is a no-op
	Cache("hit")
	Invalidation("applied")
	out := render(t, Default)
	for _, name := range []string{
		"go_goroutines", Namespace + "redis_breaker_open 0", Namespace + "redis_breaker_trips_total 0",
		Namespace + "redis_breaker_skipped_total 0", Namespace + "cache_requests_total{result=\"hit\"}",
		Namespace + "invalidate_events_total{result=\"applied\"}", Namespace + "rate_limited_total",
		Namespace + "http_requests_total",
	} {
		if !strings.Contains(out, name) {
			t.Errorf("missing %s", name)
		}
	}
}

func TestMetricsAppServesOnlyMetrics(t *testing.T) {
	r := NewRegistry()
	r.Counter("t_total", "x").Inc()
	app := NewApp(r)

	resp, err := app.Test(httptest.NewRequest("GET", "/metrics", nil), -1)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != ContentType || !strings.Contains(string(b), "t_total 1") {
		t.Fatalf("%d %q %s", resp.StatusCode, resp.Header.Get("Content-Type"), b)
	}
	for _, p := range []struct{ method, path string }{{"GET", "/"}, {"GET", "/api/v1/public/site"}, {"POST", "/metrics"}, {"GET", "/health"}} {
		resp, err = app.Test(httptest.NewRequest(p.method, p.path, nil), -1)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 404 && resp.StatusCode != 405 {
			t.Errorf("%s %s = %d, the metrics listener serves nothing but GET /metrics", p.method, p.path, resp.StatusCode)
		}
	}
}
