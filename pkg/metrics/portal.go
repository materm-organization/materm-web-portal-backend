package metrics

import (
	"database/sql"
	"runtime"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/materm-organization/materm-web-portal-backend/pkg/redisguard"
)

// Default is the process registry that the instrumentation helpers below write to.
var Default = NewRegistry()

// DurationBuckets are the upper bounds (seconds) of the request latency histogram; the request deadline is 3 s.
var DurationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 3, 5}

var (
	httpRequests = Default.Counter(Namespace+"http_requests_total",
		"HTTP requests by method, route template and status class.", "method", "route", "status_class")
	httpDuration = Default.Histogram(Namespace+"http_request_duration_seconds",
		"HTTP request latency in seconds by method, route template and status class.", DurationBuckets, "method", "route", "status_class")
	rateLimited = Default.Counter(Namespace+"rate_limited_total",
		"Requests answered 429, by route template.", "route")
	cacheRequests = Default.Counter(Namespace+"cache_requests_total",
		"Response cache lookups by result: hit (from Redis), miss (loaded from the database and offered to Redis), bypass (cache off or Redis failed).", "result")
	invalidations = Default.Counter(Namespace+"invalidate_events_total",
		"Cache invalidation messages by result: applied, rejected (bad signature, stale, replayed, malformed) or ignored.", "result")
)

// Request records one finished HTTP request. route must be the matched route template ("unmatched" otherwise).
func Request(method, route string, status int, d time.Duration) {
	class := StatusClass(status)
	m := boundedMethod(method)
	httpRequests.Inc(m, route, class)
	httpDuration.Observe(d.Seconds(), m, route, class)
	if status == 429 {
		rateLimited.Inc(route)
	}
}

// Cache records the outcome of one cache lookup ("hit", "miss" or "bypass").
func Cache(result string) { cacheRequests.Inc(result) }

// Invalidation records the outcome of one invalidation message ("applied", "rejected" or "ignored").
func Invalidation(result string) { invalidations.Inc(result) }

// CacheCount, InvalidationCount, RequestCount and RateLimitedCount read the series (tests).
func CacheCount(result string) uint64        { return cacheRequests.Value(result) }
func InvalidationCount(result string) uint64 { return invalidations.Value(result) }
func RequestCount(method, route, class string) uint64 {
	return httpRequests.Value(method, route, class)
}
func RateLimitedCount(route string) uint64 { return rateLimited.Value(route) }

// StatusClass maps a status code to "2xx" ... "5xx" ("other" outside 100-599).
func StatusClass(status int) string {
	if status < 100 || status > 599 {
		return "other"
	}
	return string(rune('0'+status/100)) + "xx"
}

func boundedMethod(m string) string {
	switch m = strings.ToUpper(m); m {
	case "GET", "POST", "HEAD", "OPTIONS", "PUT", "PATCH", "DELETE":
		return m
	}
	return "OTHER"
}

// RegisterRuntime adds the Go runtime gauges. Safe to call more than once.
func RegisterRuntime() {
	Default.GaugeFunc("go_goroutines", "Number of goroutines.", "gauge", func() []Sample {
		return []Sample{{Value: float64(runtime.NumGoroutine())}}
	})
	Default.GaugeFunc("go_memstats_alloc_bytes", "Bytes of allocated heap objects.", "gauge", func() []Sample {
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return []Sample{{Value: float64(ms.Alloc)}}
	})
}

// RegisterDBPool exposes the connection pool of the read-only role. A later call replaces the earlier one.
func RegisterDBPool(db *sql.DB) {
	if db == nil {
		return
	}
	g := func(name, help, kind string, v func(sql.DBStats) float64) {
		Default.GaugeFunc(Namespace+name, help, kind, func() []Sample { return []Sample{{Value: v(db.Stats())}} })
	}
	g("db_pool_open_connections", "Open connections (in use + idle).", "gauge", func(s sql.DBStats) float64 { return float64(s.OpenConnections) })
	g("db_pool_in_use_connections", "Connections in use.", "gauge", func(s sql.DBStats) float64 { return float64(s.InUse) })
	g("db_pool_idle_connections", "Idle connections.", "gauge", func(s sql.DBStats) float64 { return float64(s.Idle) })
	g("db_pool_max_open_connections", "Configured pool ceiling.", "gauge", func(s sql.DBStats) float64 { return float64(s.MaxOpenConnections) })
	g("db_pool_wait_total", "Connections waited for.", "counter", func(s sql.DBStats) float64 { return float64(s.WaitCount) })
	g("db_pool_wait_seconds_total", "Time blocked waiting for a connection.", "counter", func(s sql.DBStats) float64 { return s.WaitDuration.Seconds() })
	g("db_pool_max_idle_closed_total", "Connections closed because of the idle ceiling.", "counter", func(s sql.DBStats) float64 { return float64(s.MaxIdleClosed) })
	g("db_pool_max_lifetime_closed_total", "Connections closed because of their maximum lifetime.", "counter", func(s sql.DBStats) float64 { return float64(s.MaxLifetimeClosed) })
}

// RegisterRedisBreaker exposes the circuit breaker of the Redis client. rc may be nil (no Redis): the gauge is
// then 0 and the counters stay 0.
func RegisterRedisBreaker(rc redis.Cmdable) {
	var g *redisguard.Guard
	if rc != nil {
		g = redisguard.Of(rc)
	}
	Default.GaugeFunc(Namespace+"redis_breaker_open", "1 while the Redis circuit breaker is open (commands fail at once), else 0.", "gauge", func() []Sample {
		if g != nil && g.Open() {
			return []Sample{{Value: 1}}
		}
		return []Sample{{Value: 0}}
	})
	count := func(f func(*redisguard.Guard) uint64) func() []Sample {
		return func() []Sample {
			if g == nil {
				return []Sample{{Value: 0}}
			}
			return []Sample{{Value: float64(f(g))}}
		}
	}
	Default.GaugeFunc(Namespace+"redis_breaker_trips_total", "Times the Redis circuit breaker opened (redis_degraded_events).", "counter",
		count(func(g *redisguard.Guard) uint64 { return g.Trips() }))
	Default.GaugeFunc(Namespace+"redis_breaker_skipped_total", "Redis commands refused while the breaker was open.", "counter",
		count(func(g *redisguard.Guard) uint64 { return g.Skipped() }))
}
