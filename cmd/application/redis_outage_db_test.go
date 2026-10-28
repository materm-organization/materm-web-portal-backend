//go:build dbtest

package application

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/materm-organization/materm-web-portal-backend/bootstrap"
	"github.com/materm-organization/materm-web-portal-backend/pkg/logs"
	"github.com/materm-organization/materm-web-portal-backend/pkg/portalkey"
	"github.com/materm-organization/materm-web-portal-backend/pkg/redisguard"
	"github.com/materm-organization/materm-web-portal-backend/pkg/rediskeys"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/redistest"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/schemadb"
)

// flakyProxy is a throw-away TCP proxy in front of the scratch Redis: Kill refuses new connections and drops the
// open ones (a Redis that went away), Start listens on the SAME port again (it came back). Same idea as the
// proxy of the cross-system run (e2e-cross-system/lib/tcp-proxy.mjs).
type flakyProxy struct {
	t      *testing.T
	target string
	addr   string
	mu     sync.Mutex
	ln     net.Listener
	conns  []net.Conn
}

func newFlakyProxy(t *testing.T, target string) *flakyProxy {
	p := &flakyProxy{t: t, target: target, addr: "127.0.0.1:0"}
	p.Start()
	t.Cleanup(p.Kill)
	return p
}

func (p *flakyProxy) Start() {
	var ln net.Listener
	var err error
	for i := 0; i < 50; i++ { // the port may linger for a moment after Kill
		if ln, err = net.Listen("tcp", p.addr); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		p.t.Fatalf("proxy listen %s: %v", p.addr, err)
	}
	p.mu.Lock()
	p.ln = ln
	p.addr = ln.Addr().String()
	p.mu.Unlock()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			u, err := net.Dial("tcp", p.target)
			if err != nil {
				_ = c.Close()
				continue
			}
			p.mu.Lock()
			p.conns = append(p.conns, c, u)
			p.mu.Unlock()
			go pipe(c, u)
			go pipe(u, c)
		}
	}()
}

func pipe(dst, src net.Conn) {
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				break
			}
		}
		if err != nil {
			break
		}
	}
	_ = dst.Close()
	_ = src.Close()
}

func (p *flakyProxy) Kill() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ln != nil {
		_ = p.ln.Close()
		p.ln = nil
	}
	for _, c := range p.conns {
		_ = c.Close()
	}
	p.conns = nil
}

func (p *flakyProxy) HostPort() (string, int) {
	h, ps, _ := net.SplitHostPort(p.addr)
	n, _ := strconv.Atoi(ps)
	return h, n
}

func pageTitle(t *testing.T, body map[string]any) string {
	t.Helper()
	data, _ := body["data"].(map[string]any)
	page, _ := data["page"].(map[string]any)
	title, _ := page["title"].(string)
	if title == "" {
		t.Fatalf("no page.title in %v", body)
	}
	return title
}

// B1: the REAL wiring (Setup, real repositories on the read-only role, the real publiccache, subscriber and Redis
// client built by bootstrap.NewRedisClient) behind a proxy that is killed and restarted. While Redis is gone every
// page request is answered from Postgres in milliseconds (before the fix: ~2.1 s each, over the frontend's 2 s
// timeout), the library and our own warnings stay at one line per kind, and after Redis returns nothing cached
// before the outage is served: the content edited meanwhile shows at the first request.
func TestRedisOutageIsFastAndTheCacheIsFreshAfterRecovery(t *testing.T) {
	for name, withSubscriber := range map[string]bool{"with the invalidation subscriber": true, "breaker epoch alone": false} {
		t.Run(name, func(t *testing.T) { redisOutageScenario(t, withSubscriber) })
	}
}

func redisOutageScenario(t *testing.T, withSubscriber bool) {
	s := schemadb.Shared(t)
	r := redistest.New(t)
	rawURL := os.Getenv(redistest.EnvURL)
	opt, err := redis.ParseURL(rawURL)
	if err != nil {
		t.Fatal("unparseable scratch Redis URL")
	}
	ctx := context.Background()
	keys := rediskeys.New("")
	// Setup names its keys under the production prefix on this SCRATCH Redis: start from a clean prefix so no
	// answer of another database is served (same reason as in TestBootServesTheSeventeenEndpoints).
	clean := func() {
		iter := r.Client.Scan(ctx, 0, "materm-portal:*", 0).Iterator()
		for iter.Next(ctx) {
			r.Client.Del(ctx, iter.Val())
		}
	}
	clean()
	t.Cleanup(clean)

	proxy := newFlakyProxy(t, opt.Addr)
	host, port := proxy.HostPort()
	env := roEnv(t, s.RODSN, bootstrap.EnvDev)
	env.Cache = bootstrap.CacheConfig{Enabled: true, TTL: time.Minute, InvalidateChannel: r.Prefix + "outage", InvalidateMaxAge: time.Minute}
	env.Redis = bootstrap.RedisConfig{Host: host, Port: port, Username: opt.Username, Password: opt.Password, DB: opt.DB, BreakerCooldown: 500 * time.Millisecond}
	env.Defaults()
	client := bootstrap.NewRedisClient(env.Redis)
	t.Cleanup(func() { redisguard.Forget(client); _ = client.Close() })
	app := &bootstrap.Application{Env: env, Fiber: bootstrap.NewFiber(env.Server), Redis: client}
	rt, err := Setup(ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Close)
	if rt.Invalidation == nil {
		t.Fatal("the subscriber must be running")
	}
	if !withSubscriber {
		_ = rt.Invalidation.Close() // only the breaker's recovery flush can keep the cache fresh now
	}
	guard := redisguard.Of(client)
	if guard == nil {
		t.Fatal("no breaker on the client")
	}

	key := map[string]string{portalkey.Header: testKey}
	const resolve = "/api/v1/public/pages/resolve?path=/&locale=en"
	get := func() (string, time.Duration) {
		t0 := time.Now()
		st, body := req(t, rt.Fiber, "GET", resolve, key)
		d := time.Since(t0)
		if st != 200 {
			t.Fatalf("resolve: %d %v after %v", st, body, d)
		}
		return pageTitle(t, body), d
	}

	// warm: the second answer is the cached one
	oldTitle, _ := get()
	get()
	genBefore, _ := r.Client.Get(ctx, keys.Gen()).Int64()
	cached, _ := r.Client.Keys(ctx, "materm-portal:c:*").Result()
	if len(cached) == 0 {
		t.Fatal("nothing was cached while Redis was up")
	}
	resyncsBefore := rt.Invalidation.Stats().Resyncs

	// ---- Redis goes away
	var logBuf bytes.Buffer
	restore := logs.Capture(&logBuf)
	proxy.Kill()
	newTitle := oldTitle + " (edited during the outage)"
	if _, err = s.Owner.ExecContext(ctx, `update cms_page_translations set title = $1
		where locale_code = 'en' and page_id = (select id from cms_pages where path = '/' and deleted_at is null limit 1)`, newTitle); err != nil {
		restore()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.Owner.ExecContext(ctx, `update cms_page_translations set title = $1
			where locale_code = 'en' and page_id = (select id from cms_pages where path = '/' and deleted_at is null limit 1)`, oldTitle)
	})
	var worst, total time.Duration
	const n = 20
	for i := 0; i < n; i++ {
		title, d := get()
		if title != newTitle {
			restore()
			t.Fatalf("request %d during the outage served %q, want the database answer %q", i, title, newTitle)
		}
		total += d
		if d > worst {
			worst = d
		}
		if d > 300*time.Millisecond {
			restore()
			t.Fatalf("request %d took %v with Redis down, want < 300ms", i, d)
		}
	}
	time.Sleep(1500 * time.Millisecond) // let the subscriber's reconnect loop and the library run against the dead port
	get()
	restore()
	out := logBuf.String()
	for kind, needle := range map[string]string{"library dial": "failed to dial", "circuit open": "circuit open", "degraded cache": "answering from the database"} {
		if c := strings.Count(out, needle); c > 1 {
			t.Errorf("%d %q warnings during the outage, want at most one per kind (%s)", c, kind, needle)
		}
	}
	if guard.Skipped() < n-5 {
		t.Errorf("only %d commands were refused by the breaker", guard.Skipped())
	}
	t.Logf("OUTAGE %s: %d resolves, worst %v, mean %v, breaker refused %d commands, trips %d", t.Name(), n, worst, total/n, guard.Skipped(), guard.Trips())

	// ---- Redis comes back
	proxy.Start()
	deadline := time.Now().Add(8 * time.Second)
	for {
		title, _ := get()
		if title != newTitle {
			t.Fatalf("after Redis returned the old cached title %q was served (want %q)", title, newTitle)
		}
		if g, _ := r.Client.Get(ctx, keys.Gen()).Int64(); g > genBefore && !guard.Open() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Redis is back but the breaker stayed open or the generation did not move (open=%v, epoch=%d)", guard.Open(), guard.Epoch())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if guard.Epoch() < 1 {
		t.Error("the breaker must have counted the recovery")
	}
	if withSubscriber {
		for rt.Invalidation.Stats().Resyncs <= resyncsBefore {
			if time.Now().After(deadline) {
				t.Fatalf("the subscriber did not resubscribe: %+v", rt.Invalidation.Stats())
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	// the answer is cached again under the new generation and is the fresh one
	get()
	if title, _ := get(); title != newTitle {
		t.Errorf("after recovery: %q, want %q", title, newTitle)
	}
	if cached, _ = r.Client.Keys(ctx, fmt.Sprintf("materm-portal:c:%d:*", mustGen(t, r, keys))).Result(); len(cached) == 0 {
		t.Error("the page is not cached again under the new generation")
	}
}

func mustGen(t *testing.T, r *redistest.Redis, keys rediskeys.Keys) int64 {
	t.Helper()
	g, err := r.Client.Get(context.Background(), keys.Gen()).Int64()
	if err != nil {
		t.Fatal(err)
	}
	return g
}
