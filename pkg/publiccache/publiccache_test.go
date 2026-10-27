package publiccache

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestValidTag(t *testing.T) {
	ok := []string{"*", "pages", "posts:list", "albums:list", "settings", "locales", "sitemap", "redirects",
		"page:1", "page:123456789", "post:42", "album:7", "faq:2", "form:9", "menu:main", "menu:footer", "menu:top-nav", "menu:a_b",
		"collection:events", "ui:la", "ui:en", "ui:pt-BR"}
	bad := []string{"", "page", "page:", "page:0", "page:01", "page:-1", "page:1a", "page:99999999999999999999", "post:abc",
		"shared_section:5", "menu_id:5", "menu_item:5", "category:5", "collection_id:5", // internal shapes
		"menu:", "menu:Main", "menu:a b", "menu:a:b", "ui:", "ui:EN", "ui:e", "unknown", "pages:1", "PAGES", " pages",
		"page:1\n", "ui:la:extra", strings.Repeat("a", 121), "menu:" + strings.Repeat("a", 65)}
	for _, tag := range ok {
		if !ValidTag(tag) {
			t.Errorf("%q should be valid", tag)
		}
	}
	for _, tag := range bad {
		if ValidTag(tag) {
			t.Errorf("%q should be rejected", tag)
		}
	}
}

func TestFilterTags(t *testing.T) {
	valid, rejected := FilterTags([]string{"pages", "page:1", "pages", "shared_section:3", "nope", "page:1", "*"})
	if strings.Join(valid, ",") != "pages,page:1,*" || rejected != 2 {
		t.Errorf("%v %d", valid, rejected)
	}
	if v, r := FilterTags(nil); v != nil || r != 0 {
		t.Errorf("%v %d", v, r)
	}
	if !flushes("*") || !flushes("locales") || flushes("pages") {
		t.Error("flushes")
	}
}

func TestParamsHash(t *testing.T) {
	a := ParamsHash(map[string]string{"page": "1", "limit": "12"})
	b := ParamsHash(map[string]string{"limit": "12", "page": "1"})
	if a != b || len(a) != 40 {
		t.Errorf("order must not matter: %s %s", a, b)
	}
	if a == ParamsHash(map[string]string{"page": "2", "limit": "12"}) {
		t.Error("value must matter")
	}
	// delimiter injection: {"a":"b=c"} must not equal {"a=b":"c"} or {"a":"b","c":""}
	if ParamsHash(map[string]string{"a": "b=c"}) == ParamsHash(map[string]string{"a=b": "c"}) {
		t.Error("ambiguous encoding")
	}
	if ParamsHash(nil) != ParamsHash(map[string]string{}) {
		t.Error("nil and empty")
	}
}

var key32 = []byte("0123456789abcdef0123456789abcdef")

func event(now time.Time, tags ...string) Event {
	return Event{EventID: "ev-1", EventType: EventType, Tags: tags, OccurredAt: now, SentAt: now, RequestID: "r"}
}

func TestSignVerify(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	msg, err := Sign(key32, event(now, "page:1", "pages"))
	if err != nil {
		t.Fatal(err)
	}
	ev, err := Verify(msg, key32, now.Add(time.Second), 0)
	if err != nil || ev.EventID != "ev-1" || len(ev.Tags) != 2 {
		t.Fatalf("%+v %v", ev, err)
	}

	// wrong key
	if _, err = Verify(msg, []byte("another-key-another-key-another-key"), now, 0); !errors.Is(err, ErrBadSignature) {
		t.Errorf("wrong key: %v", err)
	}
	// tampered payload keeps the old signature
	var env Envelope
	_ = json.Unmarshal(msg, &env)
	env.Payload = strings.Replace(env.Payload, "page:1", "page:2", 1)
	tampered, _ := json.Marshal(env)
	if _, err = Verify(tampered, key32, now, 0); !errors.Is(err, ErrBadSignature) {
		t.Errorf("tampered: %v", err)
	}
	// missing / non-hex / truncated signature
	for _, sig := range []string{"", "zz", env.Sig[:10]} {
		bad, _ := json.Marshal(Envelope{Payload: env.Payload, Sig: sig})
		if _, err = Verify(bad, key32, now, 0); !errors.Is(err, ErrBadSignature) {
			t.Errorf("sig %q: %v", sig, err)
		}
	}
	// a bare (unsigned) event is refused when a key is set
	bare, _ := json.Marshal(event(now, "*"))
	if _, err = Verify(bare, key32, now, 0); !errors.Is(err, ErrBadSignature) {
		t.Errorf("unsigned with key: %v", err)
	}
	// stale and future
	if _, err = Verify(msg, key32, now.Add(11*time.Minute), 0); !errors.Is(err, ErrStale) {
		t.Errorf("stale: %v", err)
	}
	if _, err = Verify(msg, key32, now.Add(-2*time.Minute), 0); !errors.Is(err, ErrStale) {
		t.Errorf("future: %v", err)
	}
	if _, err = Verify(msg, key32, now.Add(9*time.Minute), 0); err != nil {
		t.Errorf("inside the window: %v", err)
	}
	if _, err = Verify(msg, key32, now.Add(3*time.Minute), time.Minute); !errors.Is(err, ErrStale) {
		t.Errorf("custom max age: %v", err)
	}
	// other event type
	other := event(now, "pages")
	other.EventType = "cms.page.published"
	om, _ := Sign(key32, other)
	if _, err = Verify(om, key32, now, 0); !errors.Is(err, ErrNotInterest) {
		t.Errorf("event type: %v", err)
	}
	// malformed
	for _, m := range [][]byte{nil, []byte(""), []byte("{"), []byte("[]"), []byte(`"x"`), make([]byte, maxMessage+1)} {
		if _, err = Verify(m, key32, now, 0); err == nil {
			t.Errorf("%q must fail", m)
		}
	}
	noID := event(now, "pages")
	noID.EventID = ""
	nm, _ := Sign(key32, noID)
	if _, err = Verify(nm, key32, now, 0); !errors.Is(err, ErrMalformed) {
		t.Errorf("no id: %v", err)
	}
	many := event(now, make([]string, maxTags+1)...)
	mm, _ := Sign(key32, many)
	if _, err = Verify(mm, key32, now, 0); !errors.Is(err, ErrMalformed) {
		t.Errorf("too many tags: %v", err)
	}
	// a signed message without sent_at is malformed (not "fresh by default")
	noSent, _ := json.Marshal(map[string]any{"event_id": "e", "event_type": EventType, "tags": []string{"pages"}, "occurred_at": now})
	raw, _ := json.Marshal(Envelope{Payload: string(noSent), Sig: mac(key32, noSent)})
	if _, err = Verify(raw, key32, now, 0); !errors.Is(err, ErrMalformed) {
		t.Errorf("no sent_at: %v", err)
	}
}

// occurred_at is when the outbox row was written; a relay that was down publishes later. Freshness is judged on
// sent_at only, so a backlog event is applied, while a captured old message is still refused.
func TestFreshnessIsJudgedOnSentAt(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	backlog := event(now.Add(-30*time.Minute), "page:1")
	backlog.SentAt = now
	msg, _ := Sign(key32, backlog)
	if ev, err := Verify(msg, key32, now, 0); err != nil || ev.EventID != "ev-1" {
		t.Errorf("an event that occurred 30 min ago but was sent now must apply: %v", err)
	}
	replay := event(now, "page:1")
	replay.SentAt = now.Add(-30 * time.Minute)
	rm, _ := Sign(key32, replay)
	if _, err := Verify(rm, key32, now, 0); !errors.Is(err, ErrStale) {
		t.Errorf("an old message with a fresh occurred_at must be refused: %v", err)
	}
	// Sign fills a zero SentAt with the current time
	zero := Event{EventID: "z", EventType: EventType, Tags: []string{"pages"}}
	zm, _ := Sign(key32, zero)
	var env Envelope
	_ = json.Unmarshal(zm, &env)
	if !strings.Contains(env.Payload, `"sent_at":"2`) {
		t.Errorf("sent_at not filled: %s", env.Payload)
	}
}

func TestVerifyUnsignedMode(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	bare, _ := json.Marshal(event(now, "pages"))
	if ev, err := Verify(bare, nil, now, 0); err != nil || ev.Tags[0] != "pages" {
		t.Errorf("bare event: %v", err)
	}
	// an envelope with a wrong signature is accepted too (the signature is not checked in this mode)
	msg, _ := Sign(key32, event(now, "pages"))
	if _, err := Verify(msg, nil, now, 0); err != nil {
		t.Errorf("envelope: %v", err)
	}
	// but the time window still applies
	if _, err := Verify(bare, nil, now.Add(time.Hour), 0); !errors.Is(err, ErrStale) {
		t.Errorf("stale: %v", err)
	}
	// without sent_at the dev mode falls back to occurred_at
	noSent, _ := json.Marshal(map[string]any{"event_id": "e", "event_type": EventType, "tags": []string{"pages"}, "occurred_at": now})
	if _, err := Verify(noSent, nil, now, 0); err != nil {
		t.Errorf("dev fallback: %v", err)
	}
	if _, err := Verify(noSent, nil, now.Add(time.Hour), 0); !errors.Is(err, ErrStale) {
		t.Errorf("dev fallback stale: %v", err)
	}
	noTimes, _ := json.Marshal(map[string]any{"event_id": "e", "event_type": EventType, "tags": []string{"pages"}})
	if _, err := Verify(noTimes, nil, now, 0); !errors.Is(err, ErrStale) {
		t.Errorf("no time at all: %v", err)
	}
}

func TestNewSubscriberValidation(t *testing.T) {
	c := New(Options{})
	if _, err := NewSubscriber(SubscriberOptions{Cache: c}); err == nil {
		t.Error("no client")
	}
	cl := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	defer cl.Close()
	if _, err := NewSubscriber(SubscriberOptions{Client: cl}); err == nil {
		t.Error("no cache")
	}
	if _, err := NewSubscriber(SubscriberOptions{Client: cl, Cache: c}); !errors.Is(err, ErrNoKey) {
		t.Errorf("blank key outside dev must be refused: %v", err)
	}
	s, err := NewSubscriber(SubscriberOptions{Client: cl, Cache: c, Key: key32})
	if err != nil || s.o.Channel != DefaultChannel || s.o.MaxAge != DefaultMaxAge || s.o.SecondDelay != DefaultSecondDelay {
		t.Errorf("defaults: %+v %v", s, err)
	}
	if err = s.Close(); err != nil { // before Start
		t.Error(err)
	}
	if _, err = NewSubscriber(SubscriberOptions{Client: cl, Cache: c, AllowUnsigned: true}); err != nil {
		t.Errorf("dev unsigned: %v", err)
	}
	// Start against a dead address fails fast and cleanly
	s2, _ := NewSubscriber(SubscriberOptions{Client: cl, Cache: c, Key: key32})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err = s2.Start(ctx); err == nil {
		t.Error("Start must fail when Redis is unreachable")
	}
	_ = s2.Close()
}

func TestReplayWindow(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	cur := now
	s := &Subscriber{o: SubscriberOptions{MaxAge: 10 * time.Minute, Now: func() time.Time { return cur }}, seen: map[string]time.Time{}}
	if !s.firstTime("a") || s.firstTime("a") {
		t.Error("second sight of an id inside the window must be refused")
	}
	cur = now.Add(11 * time.Minute)
	if !s.firstTime("a") {
		t.Error("after the window the id is new again (the event itself would be stale anyway)")
	}
	// the set stays bounded
	for i := 0; i < maxSeen*2; i++ {
		s.firstTime(strings.Repeat("x", 3) + string(rune('a'+i%26)) + time.Duration(i).String())
	}
	s.mu.Lock()
	n := len(s.seen)
	s.mu.Unlock()
	if n > maxSeen {
		t.Errorf("seen grew to %d", n)
	}
}

// ---- degraded cache (no Redis): the loader answers, concurrent callers share one load --------------------------

type page struct {
	N    int
	Name string
}

func TestGetWithoutRedisCallsTheLoader(t *testing.T) {
	for name, c := range map[string]*Cache{
		"nil client":         New(Options{Enabled: true}),
		"disabled":           New(Options{Client: FromClient(redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})), Enabled: false}),
		"typed nil via from": New(Options{Client: FromClient(nil), Enabled: true}),
	} {
		t.Run(name, func(t *testing.T) {
			if c.Active() {
				t.Fatal("must be inactive")
			}
			calls := 0
			v, st, err := Get(context.Background(), c, Spec{Resource: "page", Locale: "en"}, func(context.Context) (page, []string, error) {
				calls++
				return page{N: calls, Name: "x"}, []string{"pages"}, nil
			})
			if err != nil || st != Bypass || v.N != 1 || v.Name != "x" {
				t.Errorf("%+v %v %v", v, st, err)
			}
			if n, ierr := c.Invalidate(context.Background(), []string{"pages"}); n != 0 || ierr != nil {
				t.Errorf("Invalidate must be a no-op: %d %v", n, ierr)
			}
			if err = c.Flush(context.Background()); err != nil {
				t.Error(err)
			}
			if m, err := c.Members(context.Background(), "pages"); m != nil || err != nil {
				t.Error("Members")
			}
		})
	}
	var nilCache *Cache
	if nilCache.Active() {
		t.Error("nil cache is inactive")
	}
}

func TestGetErrorsAreReturnedAndNeverCached(t *testing.T) {
	c := New(Options{})
	boom := errors.New("db down")
	_, st, err := Get(context.Background(), c, Spec{Resource: "page", Locale: "en"}, func(context.Context) (page, []string, error) {
		return page{}, nil, boom
	})
	if !errors.Is(err, boom) || st != Bypass {
		t.Errorf("%v %v", st, err)
	}
	// a value that cannot be marshalled is an error too
	_, _, err = Get(context.Background(), c, Spec{Resource: "page", Locale: "en"}, func(context.Context) (chan int, []string, error) {
		return make(chan int), nil, nil
	})
	if err == nil {
		t.Error("unmarshalable value")
	}
}

func TestSingleflightSharesOneLoad(t *testing.T) {
	c := New(Options{})
	var calls atomic.Int32
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	load := func(context.Context) (map[string]int, []string, error) {
		if calls.Add(1) == 1 {
			started <- struct{}{}
		}
		<-release
		return map[string]int{"n": 1}, nil, nil
	}
	const n = 25
	results := make([]map[string]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, _, err := Get(context.Background(), c, Spec{Resource: "posts", Locale: "la", Params: map[string]string{"page": "1"}}, load)
			if err != nil {
				t.Error(err)
			}
			results[i] = v
		}(i)
	}
	<-started
	time.Sleep(100 * time.Millisecond) // let the others queue up behind the leader
	close(release)
	wg.Wait()
	if got := calls.Load(); got != 1 {
		t.Errorf("loader ran %d times, want 1", got)
	}
	// every caller owns its copy: mutating one must not touch another
	results[0]["n"] = 99
	for i := 1; i < n; i++ {
		if results[i]["n"] != 1 {
			t.Fatalf("result %d shares memory with result 0", i)
		}
	}
	// different parameters do not share
	var other atomic.Int32
	_, _, _ = Get(context.Background(), c, Spec{Resource: "posts", Locale: "la", Params: map[string]string{"page": "2"}}, func(context.Context) (int, []string, error) {
		other.Add(1)
		return 1, nil, nil
	})
	if other.Load() != 1 {
		t.Error("different params must load")
	}
}

func TestSingleflightLeaderCancelDoesNotFailFollowers(t *testing.T) {
	c := New(Options{})
	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	var calls atomic.Int32
	entered := make(chan struct{})
	load := func(ctx context.Context) (int, []string, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-ctx.Done()
			return 0, nil, ctx.Err()
		}
		return 7, nil, nil
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _, _ = Get(leaderCtx, c, Spec{Resource: "r", Locale: "la"}, load)
	}()
	<-entered
	var got int
	var gerr error
	fin := make(chan struct{})
	go func() {
		defer close(fin)
		got, _, gerr = Get(context.Background(), c, Spec{Resource: "r", Locale: "la"}, load)
	}()
	time.Sleep(100 * time.Millisecond)
	cancelLeader()
	<-done
	<-fin
	if gerr != nil || got != 7 {
		t.Errorf("follower got %d, %v", got, gerr)
	}
}

func TestInvalidSpecBypasses(t *testing.T) {
	c := New(Options{Client: FromClient(redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})), Enabled: true})
	if !c.Active() {
		t.Fatal("setup")
	}
	for _, sp := range []Spec{{Resource: "", Locale: "en"}, {Resource: "Bad:Name", Locale: "en"}, {Resource: "page", Locale: "en:x"}, {Resource: "page", Locale: ""}} {
		_, st, err := Get(context.Background(), c, sp, func(context.Context) (int, []string, error) { return 1, nil, nil })
		if err != nil || st != Bypass {
			t.Errorf("%+v: %v %v", sp, st, err)
		}
	}
	// Redis unreachable: still answered from the loader
	_, st, err := Get(context.Background(), c, Spec{Resource: "page", Locale: "en"}, func(context.Context) (int, []string, error) { return 1, nil, nil })
	if err != nil || st != Bypass {
		t.Errorf("unreachable redis: %v %v", st, err)
	}
}
