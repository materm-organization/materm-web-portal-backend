package publiccache

import (
	"bytes"
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/materm-organization/materm-web-portal-backend/pkg/logs"
	"github.com/materm-organization/materm-web-portal-backend/pkg/redisguard"
)

// A Redis that refuses connections: the first request notices (one attempt), the next ones do not touch the
// network at all. Every answer comes from the loader, the log says so at most once a minute.
func TestRedisDownIsFastAndAnswersFromTheLoader(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	c := redis.NewClient(&redis.Options{Addr: addr, DialTimeout: 100 * time.Millisecond, ReadTimeout: 100 * time.Millisecond,
		MaxRetries: -1, DialerRetries: 1, DialerRetryTimeout: time.Millisecond})
	g := redisguard.Attach(c, time.Minute)
	t.Cleanup(func() { redisguard.Forget(c); _ = c.Close() })
	cache := New(Options{Client: FromClient(c), Enabled: true})
	if cache.guard != g {
		t.Fatal("the cache must pick up the guard of its client")
	}

	var buf bytes.Buffer
	defer logs.Capture(&buf)()
	logs.SetSourceTrace(true)
	defer logs.SetSourceTrace(false)
	var loads atomic.Int64
	t0 := time.Now()
	for i := 0; i < 20; i++ {
		start := time.Now()
		v, st, err := Get(context.Background(), cache, Spec{Resource: "page", Locale: "en", Params: map[string]string{"path": "/"}},
			func(context.Context) (string, []string, error) { loads.Add(1); return "db", []string{"pages"}, nil })
		if err != nil || v != "db" || st != Bypass {
			t.Fatalf("request %d: %q %v %v", i, v, st, err)
		}
		if d := time.Since(start); d > 150*time.Millisecond {
			t.Errorf("request %d took %v with Redis down", i, d)
		}
	}
	if total := time.Since(t0); total > 400*time.Millisecond {
		t.Errorf("20 requests took %v", total)
	}
	if loads.Load() != 20 {
		t.Errorf("loads = %d", loads.Load())
	}
	if g.Skipped() < 19 {
		t.Errorf("breaker skipped %d commands, want >= 19 (only the first request dials)", g.Skipped())
	}
	if !strings.Contains(buf.String(), "From Postgres (Redis failed)") {
		t.Errorf("the dev source log must say where the answer came from:\n%s", buf.String())
	}
	if n := strings.Count(buf.String(), "answering from the database"); n > 1 {
		t.Errorf("%d degraded warnings, want at most one a minute:\n%s", n, buf.String())
	}
}
