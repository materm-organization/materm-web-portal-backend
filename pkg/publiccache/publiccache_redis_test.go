//go:build dbtest

package publiccache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/materm-organization/materm-web-portal-backend/pkg/rediskeys"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/redistest"
)

func newCache(t *testing.T) (*Cache, *redistest.Redis) {
	t.Helper()
	r := redistest.New(t)
	c := New(Options{Client: r.Client, Keys: rediskeys.New(r.Prefix), Enabled: true, TTL: time.Minute, TagGrace: time.Hour})
	if !c.Active() {
		t.Fatal("cache must be active")
	}
	return c, r
}

type loadCounter struct {
	n   atomic.Int32
	tag []string
}

func (c *loadCounter) load(context.Context) (page, []string, error) {
	n := int(c.n.Add(1))
	return page{N: n, Name: "v" + strconv.Itoa(n)}, c.tag, nil
}

func get(t *testing.T, c *Cache, spec Spec, l *loadCounter) (page, Status) {
	t.Helper()
	v, st, err := Get(context.Background(), c, spec, l.load)
	if err != nil {
		t.Fatal(err)
	}
	return v, st
}

func exists(t *testing.T, r *redistest.Redis, key string) bool {
	t.Helper()
	n, err := r.Client.Exists(context.Background(), key).Result()
	if err != nil {
		t.Fatal(err)
	}
	return n > 0
}

func counterValue(t *testing.T, r *redistest.Redis, key string) int64 {
	t.Helper()
	v, err := r.Client.Get(context.Background(), key).Int64()
	if errors.Is(err, redis.Nil) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestHitMissAndKeyShape(t *testing.T) {
	c, r := newCache(t)
	ctx := context.Background()
	spec := Spec{Resource: "page", Locale: "en", Params: map[string]string{"path": "/"}}
	l := &loadCounter{tag: []string{"page:1", "pages", "settings"}}

	v, st := get(t, c, spec, l)
	if st != Miss || v.N != 1 {
		t.Fatalf("first: %v %+v", st, v)
	}
	v, st = get(t, c, spec, l)
	if st != Hit || v.N != 1 || l.n.Load() != 1 {
		t.Fatalf("second must be a hit: %v %+v loads=%d", st, v, l.n.Load())
	}

	key := c.keys.Cache(0, "page", "en", ParamsHash(spec.Params))
	if !strings.HasPrefix(key, r.Prefix+"c:0:page:en:") || !exists(t, r, key) {
		t.Fatalf("key %q", key)
	}
	if ttl, _ := r.Client.PTTL(ctx, key).Result(); ttl <= 0 || ttl > time.Minute {
		t.Errorf("entry ttl %v", ttl)
	}
	for _, tag := range []string{"page:1", "pages", "settings"} {
		members, _ := c.Members(ctx, tag)
		if len(members) != 1 || members[0] != key {
			t.Errorf("tag %s members %v", tag, members)
		}
		// the tag set outlives the entry by the grace period
		if ttl, _ := r.Client.PTTL(ctx, c.keys.Tag(tag)).Result(); ttl < time.Hour {
			t.Errorf("tag set ttl %v must be at least entry ttl + 1h", ttl)
		}
	}

	// other locale / other params / other resource are separate entries
	for _, sp := range []Spec{
		{Resource: "page", Locale: "la", Params: spec.Params},
		{Resource: "page", Locale: "en", Params: map[string]string{"path": "/our-path"}},
		{Resource: "post", Locale: "en", Params: spec.Params},
	} {
		if _, st = get(t, c, sp, l); st != Miss {
			t.Errorf("%+v must miss", sp)
		}
	}
	// Spec.TTL shortens, never extends
	short := Spec{Resource: "neg", Locale: "-", TTL: 5 * time.Second}
	get(t, c, short, l)
	if ttl, _ := r.Client.PTTL(ctx, c.keys.Cache(0, "neg", "-", ParamsHash(nil))).Result(); ttl <= 0 || ttl > 5*time.Second {
		t.Errorf("custom ttl %v", ttl)
	}
	long := Spec{Resource: "long", Locale: "-", TTL: 24 * time.Hour}
	get(t, c, long, l)
	if ttl, _ := r.Client.PTTL(ctx, c.keys.Cache(0, "long", "-", ParamsHash(nil))).Result(); ttl > time.Minute {
		t.Errorf("ttl above cache.ttl: %v", ttl)
	}
}

func TestInvalidateByTag(t *testing.T) {
	c, r := newCache(t)
	ctx := context.Background()
	home := &loadCounter{tag: []string{"page:1", "pages"}}
	blog := &loadCounter{tag: []string{"page:2", "pages", "posts:list"}}
	site := &loadCounter{tag: []string{"settings"}}
	hs, bs, ss := Spec{Resource: "page", Locale: "en", Params: map[string]string{"p": "1"}}, Spec{Resource: "page", Locale: "en", Params: map[string]string{"p": "2"}}, Spec{Resource: "site", Locale: "en"}
	get(t, c, hs, home)
	get(t, c, bs, blog)
	get(t, c, ss, site)
	hk := c.keys.Cache(0, "page", "en", ParamsHash(hs.Params))

	n, err := c.Invalidate(ctx, []string{"page:1"})
	if err != nil || n != 1 {
		t.Fatalf("deleted %d, %v", n, err)
	}
	if exists(t, r, hk) || exists(t, r, c.keys.Tag("page:1")) {
		t.Error("entry and its tag set must be gone")
	}
	if _, st := get(t, c, bs, blog); st != Hit {
		t.Error("an unrelated entry must survive")
	}
	if _, st := get(t, c, ss, site); st != Hit {
		t.Error("an unrelated entry must survive")
	}
	if v, st := get(t, c, hs, home); st != Miss || v.N != 2 {
		t.Errorf("reload: %v %+v", st, v)
	}

	// a tag shared by several entries removes all of them
	if n, _ = c.Invalidate(ctx, []string{"pages"}); n != 2 {
		t.Errorf("pages tag: deleted %d, want 2", n)
	}
	if _, st := get(t, c, bs, blog); st != Miss {
		t.Error("blog page must reload")
	}
	// the generation did not change for plain tags
	if g := counterValue(t, r, c.keys.Gen()); g != 0 {
		t.Errorf("gen %d", g)
	}
	if inv := counterValue(t, r, c.keys.Inv()); inv != 2 {
		t.Errorf("inv %d, want 2 (one per Invalidate call)", inv)
	}
}

func TestGenerationFlush(t *testing.T) {
	for _, tag := range []string{"locales", "*"} {
		t.Run(tag, func(t *testing.T) {
			c, r := newCache(t)
			ctx := context.Background()
			l := &loadCounter{tag: []string{"page:1"}}
			sp := Spec{Resource: "page", Locale: "en"}
			get(t, c, sp, l)
			if _, err := c.Invalidate(ctx, []string{tag}); err != nil {
				t.Fatal(err)
			}
			if g := counterValue(t, r, c.keys.Gen()); g != 1 {
				t.Fatalf("gen %d", g)
			}
			v, st := get(t, c, sp, l)
			if st != Miss || v.N != 2 {
				t.Errorf("everything is stale after %s: %v %+v", tag, st, v)
			}
			if !exists(t, r, c.keys.Cache(1, "page", "en", ParamsHash(nil))) {
				t.Error("the new entry lives under the new generation")
			}
		})
	}
	// Flush (used on subscribe) does the same
	c, r := newCache(t)
	if err := c.Flush(context.Background()); err != nil || counterValue(t, r, c.keys.Gen()) != 1 || counterValue(t, r, c.keys.Inv()) != 1 {
		t.Errorf("flush: %v", err)
	}
}

func TestUnknownAndInternalTagsDoNothing(t *testing.T) {
	c, r := newCache(t)
	l := &loadCounter{tag: []string{"page:1"}}
	sp := Spec{Resource: "page", Locale: "en"}
	get(t, c, sp, l)
	n, err := c.Invalidate(context.Background(), []string{"shared_section:3", "menu_id:2", "category:1", "nope", "", "page:1; DEL *"})
	if err != nil || n != 0 {
		t.Fatalf("%d %v", n, err)
	}
	if _, st := get(t, c, sp, l); st != Hit {
		t.Error("the entry must be untouched")
	}
	if counterValue(t, r, c.keys.Inv()) != 0 {
		t.Error("nothing valid: no counter bump")
	}
	// a loader that returns invalid tags still caches, but only valid tags create sets
	l2 := &loadCounter{tag: []string{"page:2", "internal:9"}}
	get(t, c, Spec{Resource: "x", Locale: "en"}, l2)
	if m, _ := c.Members(context.Background(), "internal:9"); len(m) != 0 {
		t.Error("invalid tag created a set")
	}
	if m, _ := c.Members(context.Background(), "page:2"); len(m) != 1 {
		t.Error("valid tag set missing")
	}
}

// R-024: a slow load that started before an invalidation must never store its (stale) answer afterwards.
func TestSlowLoadStartedBeforeInvalidationIsNotStored(t *testing.T) {
	for name, invalidate := range map[string]func(c *Cache) error{
		"tag":       func(c *Cache) error { _, err := c.Invalidate(context.Background(), []string{"page:1"}); return err },
		"unrelated": func(c *Cache) error { _, err := c.Invalidate(context.Background(), []string{"post:9"}); return err },
		"locales":   func(c *Cache) error { _, err := c.Invalidate(context.Background(), []string{"locales"}); return err },
		"flush":     func(c *Cache) error { return c.Flush(context.Background()) },
	} {
		t.Run(name, func(t *testing.T) {
			c, r := newCache(t)
			sp := Spec{Resource: "page", Locale: "en"}
			started, release := make(chan struct{}), make(chan struct{})
			var db atomic.Value
			db.Store("old")
			var first atomic.Bool
			load := func(context.Context) (string, []string, error) {
				v := db.Load().(string) // the "database read"
				if first.CompareAndSwap(false, true) {
					close(started)
					<-release // ... the request is slow (> 2 s in production)
				}
				return v, []string{"page:1"}, nil
			}
			res := make(chan string, 1)
			go func() {
				v, _, err := Get(context.Background(), c, sp, load)
				if err != nil {
					t.Error(err)
				}
				res <- v
			}()
			<-started
			db.Store("new") // the editor commits, the relay invalidates (twice, 2 s apart)
			if err := invalidate(c); err != nil {
				t.Fatal(err)
			}
			close(release)
			if got := <-res; got != "old" {
				t.Errorf("the slow request still answers what it read: %q", got)
			}
			gen := counterValue(t, r, c.keys.Gen())
			if exists(t, r, c.keys.Cache(gen, "page", "en", ParamsHash(nil))) || exists(t, r, c.keys.Cache(0, "page", "en", ParamsHash(nil))) {
				t.Fatal("the stale answer was stored after the invalidation")
			}
			if m, _ := c.Members(context.Background(), "page:1"); len(m) != 0 {
				t.Errorf("tag set got the stale key: %v", m)
			}
			v, st, _ := Get(context.Background(), c, sp, load)
			if v != "new" || st != Miss {
				t.Errorf("next request: %q %v", v, st)
			}
			if v, st, _ = Get(context.Background(), c, sp, load); v != "new" || st != Hit {
				t.Errorf("and it is cached now: %q %v", v, st)
			}
		})
	}
}

func TestCorruptEntryIsReplaced(t *testing.T) {
	c, r := newCache(t)
	sp := Spec{Resource: "page", Locale: "en"}
	key := c.keys.Cache(0, "page", "en", ParamsHash(nil))
	if err := r.Client.Set(context.Background(), key, "{not json", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	l := &loadCounter{}
	v, st := get(t, c, sp, l)
	if st != Miss || v.N != 1 {
		t.Errorf("%v %+v", st, v)
	}
	if _, st = get(t, c, sp, l); st != Hit {
		t.Error("replaced entry must hit")
	}
}

func TestConcurrentMissesLoadOnce(t *testing.T) {
	c, _ := newCache(t)
	l := &loadCounter{}
	slow := func(ctx context.Context) (page, []string, error) {
		time.Sleep(150 * time.Millisecond)
		return l.load(ctx)
	}
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := Get(context.Background(), c, Spec{Resource: "page", Locale: "en"}, slow); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if l.n.Load() != 1 {
		t.Errorf("loader ran %d times", l.n.Load())
	}
}

func TestRedisErrorMidRequestFallsBackToTheLoader(t *testing.T) {
	r := redistest.New(t)
	opt := *r.Client.Options()
	opt.MaxRetries = -1
	client := redis.NewClient(&opt)
	c := New(Options{Client: client, Keys: rediskeys.New(r.Prefix), Enabled: true})
	l := &loadCounter{}
	if _, st := get(t, c, Spec{Resource: "page", Locale: "en"}, l); st != Miss {
		t.Fatal(st)
	}
	_ = client.Close() // every later command fails with "redis: client is closed"
	v, st, err := Get(context.Background(), c, Spec{Resource: "page", Locale: "en"}, l.load)
	if err != nil || st != Bypass || v.N != 2 {
		t.Errorf("%v %v %+v", err, st, v)
	}
	if n, err := c.Invalidate(context.Background(), []string{"pages"}); err == nil || n != 0 {
		t.Errorf("Invalidate reports the Redis error: %d %v", n, err)
	}
}

// ---- subscriber ------------------------------------------------------------------------------------------------------

var subKey = []byte("subscriber-test-key-0123456789abcdef")

type subEnv struct {
	c   *Cache
	r   *redistest.Redis
	sub *Subscriber
	ch  string
	now time.Time
}

func newSub(t *testing.T, mutate func(*SubscriberOptions)) *subEnv {
	t.Helper()
	c, r := newCache(t)
	ch := r.Prefix + "chan"
	o := SubscriberOptions{Client: r.Client, Cache: c, Channel: ch, Key: subKey, SecondDelay: 250 * time.Millisecond}
	if mutate != nil {
		mutate(&o)
	}
	sub, err := NewSubscriber(o)
	if err != nil {
		t.Fatal(err)
	}
	if err = sub.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sub.Close() })
	e := &subEnv{c: c, r: r, sub: sub, ch: ch, now: time.Now()}
	return e
}

func (e *subEnv) publish(t *testing.T, ev Event, key []byte) {
	t.Helper()
	msg, err := Sign(key, ev)
	if err != nil {
		t.Fatal(err)
	}
	e.publishRaw(t, msg)
}

func (e *subEnv) publishRaw(t *testing.T, msg []byte) {
	t.Helper()
	if err := e.r.Client.Publish(context.Background(), e.ch, msg).Err(); err != nil {
		t.Fatal(err)
	}
}

func (e *subEnv) event(id string, tags ...string) Event {
	return Event{EventID: id, EventType: EventType, Tags: tags, OccurredAt: time.Now(), RequestID: "req"}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestSubscriberAppliesASignedMessage(t *testing.T) {
	e := newSub(t, nil)
	ctx := context.Background()
	if e.sub.Stats().Resyncs != 1 || counterValue(t, e.r, e.c.keys.Gen()) != 1 {
		t.Fatalf("subscribing bumps the generation once: %+v gen=%d", e.sub.Stats(), counterValue(t, e.r, e.c.keys.Gen()))
	}
	l1, l2 := &loadCounter{tag: []string{"page:1", "pages"}}, &loadCounter{tag: []string{"settings"}}
	s1, s2 := Spec{Resource: "page", Locale: "en"}, Spec{Resource: "site", Locale: "en"}
	get(t, e.c, s1, l1)
	get(t, e.c, s2, l2)

	e.publish(t, e.event("ev-1", "page:1"), subKey)
	eventually(t, "the entry to be invalidated", func() bool { return e.sub.Stats().Applied == 1 })
	if _, st := get(t, e.c, s1, l1); st != Miss {
		t.Error("page:1 entry must be gone")
	}
	if _, st := get(t, e.c, s2, l2); st != Hit {
		t.Error("an unrelated entry must stay")
	}
	_ = ctx
}

func TestSubscriberDoubleDelete(t *testing.T) {
	e := newSub(t, nil)
	ctx := context.Background()
	staleKey := e.r.Key("stale-entry")
	e.publish(t, e.event("ev-dd", "page:7"), subKey)
	eventually(t, "the first pass", func() bool { return e.sub.Stats().Applied == 1 })
	// a request that read the database before the commit now writes its old answer under the tag (what the
	// second delete exists for) - written straight into the tag set, bypassing the conditional store
	if err := e.r.Client.Set(ctx, staleKey, "old", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if err := e.r.Client.SAdd(ctx, e.c.keys.Tag("page:7"), staleKey).Err(); err != nil {
		t.Fatal(err)
	}
	if !exists(t, e.r, staleKey) {
		t.Fatal("setup")
	}
	eventually(t, "the second delete to remove the stale entry", func() bool { return !exists(t, e.r, staleKey) })
	if inv := counterValue(t, e.r, e.c.keys.Inv()); inv < 3 { // Start (resync) + pass 1 + pass 2
		t.Errorf("inv %d", inv)
	}
}

func TestSubscriberRejectsBadMessages(t *testing.T) {
	e := newSub(t, func(o *SubscriberOptions) { o.MaxAge = time.Minute })
	l := &loadCounter{tag: []string{"page:1"}}
	sp := Spec{Resource: "page", Locale: "en"}
	get(t, e.c, sp, l)

	old := e.event("ev-old", "page:1")
	old.SentAt = time.Now().Add(-10 * time.Minute)
	other := e.event("ev-badsig", "page:1")

	e.publish(t, other, []byte("a-different-key-a-different-key-a-different-key")) // wrong key
	e.publishRaw(t, []byte(`{"payload":"{\"event_id\":\"x\"}","sig":"00"}`))       // bad signature
	e.publishRaw(t, []byte(`not json at all`))
	bare, _ := jsonEvent(e.event("ev-bare", "page:1"))
	e.publishRaw(t, bare)     // unsigned, key configured
	e.publish(t, old, subKey) // correctly signed but too old
	fut := e.event("ev-future", "page:1")
	fut.SentAt = time.Now().Add(time.Hour)
	e.publish(t, fut, subKey) // from the future
	eventually(t, "all bad messages to be counted", func() bool { return e.sub.Stats().Rejected == 6 })
	if _, st := get(t, e.c, sp, l); st != Hit {
		t.Error("a rejected message must not invalidate anything")
	}
	if s := e.sub.Stats(); s.Applied != 0 {
		t.Errorf("%+v", s)
	}
	// a good one still works afterwards
	e.publish(t, e.event("ev-good", "page:1"), subKey)
	eventually(t, "the good message", func() bool { return e.sub.Stats().Applied == 1 })
	if _, st := get(t, e.c, sp, l); st != Miss {
		t.Error("the good message must invalidate")
	}
}

func jsonEvent(ev Event) ([]byte, error) {
	m, err := Sign([]byte("x"), ev)
	if err != nil {
		return nil, err
	}
	var env Envelope
	if err = jsonUnmarshal(m, &env); err != nil {
		return nil, err
	}
	return []byte(env.Payload), nil
}

func TestSubscriberIgnoresReplaysAndInternalTags(t *testing.T) {
	e := newSub(t, nil)
	l := &loadCounter{tag: []string{"page:1"}}
	sp := Spec{Resource: "page", Locale: "en"}
	get(t, e.c, sp, l)
	invBefore := counterValue(t, e.r, e.c.keys.Inv())

	// the five internal shapes and unknown tags: acted on never
	e.publish(t, e.event("ev-int", "shared_section:1", "menu_id:2", "menu_item:3", "category:4", "collection_id:5", "unknown"), subKey)
	eventually(t, "ignored", func() bool { return e.sub.Stats().Ignored == 1 })
	if counterValue(t, e.r, e.c.keys.Inv()) != invBefore {
		t.Error("nothing valid: no invalidation")
	}
	// mixed: only the valid tag counts
	good := e.event("ev-mixed", "category:4", "page:1")
	e.publish(t, good, subKey)
	eventually(t, "applied", func() bool { return e.sub.Stats().Applied == 1 })
	if e.sub.Stats().TagsFound != 1 {
		t.Errorf("%+v", e.sub.Stats())
	}
	// the same event again (a captured message) is not applied twice
	get(t, e.c, sp, l)
	e.publish(t, good, subKey)
	eventually(t, "replay rejected", func() bool { return e.sub.Stats().Rejected == 1 })
	if _, st := get(t, e.c, sp, l); st != Hit {
		t.Error("a replay must not invalidate")
	}
	// another event type is not an invalidation
	ev := e.event("ev-type", "page:1")
	ev.EventType = "cms.page.published"
	e.publish(t, ev, subKey)
	eventually(t, "other type ignored", func() bool { return e.sub.Stats().Ignored == 2 })
}

// A relay that was down publishes old events late: they must still apply.
func TestSubscriberAppliesBacklogEvents(t *testing.T) {
	e := newSub(t, nil)
	l := &loadCounter{tag: []string{"page:1"}}
	sp := Spec{Resource: "page", Locale: "en"}
	get(t, e.c, sp, l)
	ev := e.event("ev-backlog", "page:1")
	ev.OccurredAt = time.Now().Add(-30 * time.Minute)
	e.publish(t, ev, subKey) // Sign sets sent_at = now
	eventually(t, "the backlog event", func() bool { return e.sub.Stats().Applied == 1 })
	if _, st := get(t, e.c, sp, l); st != Miss {
		t.Error("must invalidate")
	}
}

func TestSubscriberFlushTag(t *testing.T) {
	e := newSub(t, nil)
	l := &loadCounter{tag: []string{"page:1"}}
	sp := Spec{Resource: "page", Locale: "en"}
	get(t, e.c, sp, l)
	gen := counterValue(t, e.r, e.c.keys.Gen())
	e.publish(t, e.event("ev-flush", "*"), subKey)
	eventually(t, "gen bump", func() bool { return counterValue(t, e.r, e.c.keys.Gen()) > gen })
	if _, st := get(t, e.c, sp, l); st != Miss {
		t.Error("everything is stale")
	}
}

func TestSubscriberUnsignedDevMode(t *testing.T) {
	e := newSub(t, func(o *SubscriberOptions) { o.Key = nil; o.AllowUnsigned = true })
	l := &loadCounter{tag: []string{"page:1"}}
	sp := Spec{Resource: "page", Locale: "en"}
	get(t, e.c, sp, l)
	bare, _ := jsonEvent(e.event("ev-dev", "page:1"))
	e.publishRaw(t, bare)
	eventually(t, "unsigned accepted", func() bool { return e.sub.Stats().Applied == 1 })
	if _, st := get(t, e.c, sp, l); st != Miss {
		t.Error("must invalidate")
	}
}

func TestSubscriberResubscribesAfterALostConnection(t *testing.T) {
	r := redistest.New(t)
	name := "pc-test-" + strings.ReplaceAll(strings.Trim(r.Prefix, ":"), ":", "-")
	opt := *r.Client.Options()
	opt.ClientName = name
	subClient := redis.NewClient(&opt)
	t.Cleanup(func() { _ = subClient.Close() })
	c := New(Options{Client: r.Client, Keys: rediskeys.New(r.Prefix), Enabled: true})
	sub, err := NewSubscriber(SubscriberOptions{Client: subClient, Cache: c, Channel: r.Prefix + "chan", Key: subKey, SecondDelay: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if err = sub.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sub.Close() })
	if sub.Stats().Resyncs != 1 {
		t.Fatal("first subscribe")
	}
	// kill ONLY our own subscriber connection (found by its client name); other packages share this Redis
	ctx := context.Background()
	list, err := r.Client.ClientList(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	killed := 0
	for _, line := range strings.Split(list, "\n") {
		if strings.Contains(line, " name="+name+" ") && strings.Contains(line, " sub=1 ") {
			id := strings.TrimPrefix(strings.Fields(line)[0], "id=")
			if err = r.Client.ClientKillByFilter(ctx, "ID", id).Err(); err != nil {
				t.Fatal(err)
			}
			killed++
		}
	}
	if killed != 1 {
		t.Fatalf("expected to find exactly one subscriber connection, found %d", killed)
	}
	eventually(t, "the generation to be bumped again after the re-subscription", func() bool { return sub.Stats().Resyncs >= 2 })
	if g := counterValue(t, r, c.keys.Gen()); g < 2 {
		t.Errorf("gen %d", g)
	}
	// and it hears messages again
	ev := Event{EventID: "ev-after", EventType: EventType, Tags: []string{"pages"}, OccurredAt: time.Now()}
	msg, _ := Sign(subKey, ev)
	eventually(t, "delivery after reconnect", func() bool {
		_ = r.Client.Publish(ctx, r.Prefix+"chan", msg).Err()
		return sub.Stats().Applied >= 1
	})
}

func TestSubscriberCloseStopsEverything(t *testing.T) {
	e := newSub(t, func(o *SubscriberOptions) { o.SecondDelay = time.Hour })
	e.publish(t, e.event("ev-1", "pages"), subKey)
	eventually(t, "applied", func() bool { return e.sub.Stats().Applied == 1 }) // a second delete is now pending for an hour
	done := make(chan error, 1)
	go func() { done <- e.sub.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("close: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close must cancel the pending second delete and return")
	}
	if err := e.sub.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	e.publish(t, e.event("ev-2", "pages"), subKey)
	time.Sleep(200 * time.Millisecond)
	if e.sub.Stats().Applied != 1 {
		t.Error("a closed subscriber must not apply messages")
	}
	if err := e.sub.Start(context.Background()); err == nil {
		t.Error("a subscriber cannot be started twice")
	}
}

// publishing on the channel needs no special rights, so the signature is what matters; this documents that
// two subscribers on different channels do not hear each other.
func TestSubscribersOnOtherChannelsDoNotHearEachOther(t *testing.T) {
	a := newSub(t, nil)
	b := newSub(t, nil)
	a.publish(t, a.event("ev-a", "pages"), subKey)
	eventually(t, "a applies", func() bool { return a.sub.Stats().Applied == 1 })
	time.Sleep(150 * time.Millisecond)
	if b.sub.Stats().Applied != 0 || b.sub.Stats().Rejected != 0 {
		t.Errorf("b heard a: %+v", b.sub.Stats())
	}
	_ = fmt.Sprint
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
