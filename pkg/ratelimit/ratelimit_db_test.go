//go:build dbtest

package ratelimit

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/materm-organization/materm-web-portal-backend/pkg/apperror"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/redistest"
)

func TestHitCountsAndExpires(t *testing.T) {
	r := redistest.New(t)
	l := NewRedis(r.Client)
	ctx := context.Background()
	key := r.Key("login")

	for i := 1; i <= 5; i++ {
		res, err := l.Hit(ctx, key, 3, 400*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		wantAllowed := i <= 3
		wantRemaining := 3 - i
		if wantRemaining < 0 {
			wantRemaining = 0
		}
		if res.Allowed != wantAllowed || res.Count != int64(i) || res.Remaining != wantRemaining || res.Limit != 3 {
			t.Errorf("hit %d: %+v", i, res)
		}
		if res.RetryAfter <= 0 || res.RetryAfter > 400*time.Millisecond || !res.ResetAt.After(time.Now()) {
			t.Errorf("hit %d: window %v reset %v", i, res.RetryAfter, res.ResetAt)
		}
	}
	n, remaining, err := l.Count(ctx, key)
	if err != nil || n != 5 || remaining <= 0 {
		t.Errorf("Count: %d %v %v", n, remaining, err)
	}
	time.Sleep(450 * time.Millisecond)
	res, err := l.Hit(ctx, key, 3, 400*time.Millisecond)
	if err != nil || res.Count != 1 || !res.Allowed {
		t.Errorf("after the window the counter restarts: %+v %v", res, err)
	}
	// a key that never existed
	if n, rem, err := l.Count(ctx, r.Key("none")); err != nil || n != 0 || rem != 0 {
		t.Errorf("Count of a missing key: %d %v %v", n, rem, err)
	}
	if err := l.Reset(ctx, key); err != nil {
		t.Fatal(err)
	}
	if n, _, _ := l.Count(ctx, key); n != 0 {
		t.Error("Reset must delete the counter")
	}
}

// the window is fixed from the first hit: later hits must not extend it
func TestWindowIsNotExtendedByLaterHits(t *testing.T) {
	r := redistest.New(t)
	l := NewRedis(r.Client)
	ctx := context.Background()
	first, _ := l.Hit(ctx, r.Key("w"), 100, 2*time.Second)
	time.Sleep(300 * time.Millisecond)
	later, _ := l.Hit(ctx, r.Key("w"), 100, 2*time.Second)
	if later.RetryAfter >= first.RetryAfter {
		t.Errorf("the window must keep running: first %v later %v", first.RetryAfter, later.RetryAfter)
	}
}

func TestRepairsCounterWithoutTTL(t *testing.T) {
	r := redistest.New(t)
	l := NewRedis(r.Client)
	ctx := context.Background()
	key := r.Key("nottl")
	if err := r.Client.Set(ctx, key, 7, 0).Err(); err != nil { // a counter that lost its expiry
		t.Fatal(err)
	}
	res, err := l.Hit(ctx, key, 3, time.Second)
	if err != nil || res.Count != 8 || res.Allowed {
		t.Fatalf("%+v %v", res, err)
	}
	if ttl := r.Client.PTTL(ctx, key).Val(); ttl <= 0 {
		t.Errorf("the script must re-arm the expiry, ttl=%v", ttl)
	}
}

func TestConcurrentHitsAreAtomic(t *testing.T) {
	r := redistest.New(t)
	l := NewRedis(r.Client)
	ctx := context.Background()
	var wg sync.WaitGroup
	allowed := make(chan bool, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := l.Hit(ctx, r.Key("c"), 10, 5*time.Second)
			if err != nil {
				t.Error(err)
			}
			allowed <- res.Allowed
		}()
	}
	wg.Wait()
	close(allowed)
	n := 0
	for a := range allowed {
		if a {
			n++
		}
	}
	if n != 10 {
		t.Errorf("exactly 10 of 50 concurrent hits may pass, got %d", n)
	}
}

func TestInvalidArguments(t *testing.T) {
	r := redistest.New(t)
	l := NewRedis(r.Client)
	if _, err := l.Hit(context.Background(), r.Key("x"), 0, time.Second); err == nil {
		t.Error("limit 0")
	}
	if _, err := l.Hit(context.Background(), r.Key("x"), 1, 0); err == nil {
		t.Error("window 0")
	}
}

func TestMiddleware(t *testing.T) {
	r := redistest.New(t)
	l := NewRedis(r.Client)
	app := fiber.New(fiber.Config{ErrorHandler: apperror.Handler})
	app.Use(Middleware(l, Config{Name: "t", Limit: 2, Window: 5 * time.Second, Key: func(c *fiber.Ctx) string {
		if c.Query("skip") != "" {
			return ""
		}
		return r.Key("mw:" + Hash(c.Query("who")))
	}}))
	app.Get("/x", func(c *fiber.Ctx) error { return c.SendString("ok") })

	get := func(q string) (int, string, string, string) {
		resp, err := app.Test(httptest.NewRequest("GET", "/x?"+q, nil), -1)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode, string(b), resp.Header.Get("X-RateLimit-Remaining"), resp.Header.Get("Retry-After")
	}
	if s, _, rem, _ := get("who=a"); s != 200 || rem != "1" {
		t.Errorf("first: %d remaining=%s", s, rem)
	}
	if s, _, rem, _ := get("who=a"); s != 200 || rem != "0" {
		t.Errorf("second: %d remaining=%s", s, rem)
	}
	s, body, _, ra := get("who=a")
	if s != 429 || ra == "" || !strings.Contains(body, `"code":"RATE_LIMITED"`) {
		t.Errorf("third must be 429 with Retry-After: %d %q %s", s, ra, body)
	}
	if s, _, _, _ := get("who=b"); s != 200 {
		t.Error("another key is independent")
	}
	if s, _, _, _ := get("who=a&skip=1"); s != 200 {
		t.Error("an empty key skips limiting")
	}
}

func TestUnavailableLimiter(t *testing.T) {
	l := NewRedis(nil)
	ctx := context.Background()
	if _, err := l.Hit(ctx, "k", 1, time.Second); err != ErrUnavailable {
		t.Errorf("Hit: %v", err)
	}
	if _, _, err := l.Count(ctx, "k"); err != ErrUnavailable {
		t.Errorf("Count: %v", err)
	}
	if err := l.Reset(ctx, "k"); err != ErrUnavailable {
		t.Errorf("Reset: %v", err)
	}
	app := fiber.New(fiber.Config{ErrorHandler: apperror.Handler})
	app.Use(Middleware(l, Config{Name: "closed", Limit: 1, Window: time.Second, Key: func(*fiber.Ctx) string { return "k" }}))
	app.Get("/x", func(c *fiber.Ctx) error { return c.SendString("ok") })
	resp, _ := app.Test(httptest.NewRequest("GET", "/x", nil), -1)
	if resp.StatusCode != 503 {
		t.Errorf("fail-closed default: %d", resp.StatusCode)
	}
	app = fiber.New(fiber.Config{ErrorHandler: apperror.Handler})
	app.Use(Middleware(l, Config{Name: "open", Limit: 1, Window: time.Second, FailOpen: true, Key: func(*fiber.Ctx) string { return "k" }}))
	app.Get("/x", func(c *fiber.Ctx) error { return c.SendString("ok") })
	resp, _ = app.Test(httptest.NewRequest("GET", "/x", nil), -1)
	if resp.StatusCode != 200 {
		t.Errorf("FailOpen: %d", resp.StatusCode)
	}
}

func TestHashIsStableAndOpaque(t *testing.T) {
	a, a2 := Hash("Alice"), Hash("Alice")
	if a != a2 || a == Hash("alice") || len(Hash("x")) != 32 {
		t.Error("Hash must be deterministic, case-sensitive and 32 hex chars")
	}
}
