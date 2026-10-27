//go:build dbtest

package application

import (
	"context"
	"testing"
	"time"

	"github.com/materm-organization/materm-web-portal-backend/bootstrap"
	"github.com/materm-organization/materm-web-portal-backend/pkg/publiccache"
	"github.com/materm-organization/materm-web-portal-backend/pkg/rediskeys"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/dbtest"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/redistest"
)

// The real wiring: Setup starts the subscriber (random channel), a signed message clears the cache entry that
// the real PublicCache stored, and Runtime.Close stops the subscriber.
func TestSetupStartsAndCloseStopsTheInvalidationSubscriber(t *testing.T) {
	d := dbtest.New(t)
	r := redistest.New(t)
	key := "wiring-test-key-0123456789abcdef0123456789"

	env := scratchEnv(t, d, bootstrap.EnvDev)
	env.Cache = bootstrap.CacheConfig{Enabled: true, TTL: time.Minute, InvalidateKey: key, InvalidateChannel: r.Prefix + "chan", InvalidateMaxAge: time.Minute}
	app := &bootstrap.Application{Env: env, Fiber: bootstrap.NewFiber(env.Server), Redis: r.Client}
	rt, err := Setup(context.Background(), app)
	if err != nil {
		t.Fatal(err)
	}
	if rt.Invalidation == nil {
		rt.Close()
		t.Fatal("the subscriber must be running")
	}
	ctx := context.Background()
	// Setup's cache uses the production prefix (materm-portal:) on this SCRATCH Redis, which the bootstrap test
	// also clears: so assert on the subscriber's own counters, never on those keys. Remove only what this test
	// wrote at start-up (exactly these two keys).
	t.Cleanup(func() { r.Client.Del(ctx, rediskeys.New("").Gen(), rediskeys.New("").Inv()) })
	if rt.Invalidation.Stats().Resyncs < 1 {
		t.Errorf("subscribing must bump the generation: %+v", rt.Invalidation.Stats())
	}

	msg, err := publiccache.Sign([]byte(key), publiccache.Event{EventID: "wire-1", EventType: publiccache.EventType, Tags: []string{"locales"}, OccurredAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Client.Publish(ctx, r.Prefix+"chan", msg).Err(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for rt.Invalidation.Stats().Applied != 1 {
		if time.Now().After(deadline) {
			rt.Close()
			t.Fatal("the signed message was not applied")
		}
		time.Sleep(20 * time.Millisecond)
	}
	sub := rt.Invalidation
	rt.Close()
	if rt.Invalidation != nil {
		t.Error("Close must release the subscriber")
	}
	if err = sub.Close(); err != nil {
		t.Errorf("closing twice: %v", err)
	}
	rt.Close() // idempotent
}

// With cache.enabled=false (the default of the other boot tests) nothing subscribes.
func TestSetupWithoutCacheDoesNotSubscribe(t *testing.T) {
	d := dbtest.New(t)
	r := redistest.New(t)
	env := scratchEnv(t, d, bootstrap.EnvDev)
	app := &bootstrap.Application{Env: env, Fiber: bootstrap.NewFiber(env.Server), Redis: r.Client}
	rt, err := Setup(context.Background(), app)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	if rt.Invalidation != nil {
		t.Error("cache.enabled=false: no subscriber")
	}
}
