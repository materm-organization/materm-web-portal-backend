//go:build dbtest

package publicapi

import (
	"context"
	"testing"
	"time"

	"github.com/materm-organization/materm-web-portal-backend/pkg/publiccache"
)

// P-17 on the real view v_active_redirects through the read-only role: locale precedence, the blank
// (all-locales) rows, chains, inactive / trashed rows, and the 404 for the rest.
func TestDBRedirectResolve(t *testing.T) {
	st, s, _, _ := cachedStack(t)
	ownerExec(t, s, `insert into cms_redirects (source_path, locale_code, target, status_code, is_active) values
	  ('/t-a', null, '/t-b', 301, true), ('/t-b', null, '/t-c', 302, true), ('/t-c', null, 'https://example.com/x', 308, true),
	  ('/t-off', null, '/t-b', 301, false), ('/t-loop1', null, '/t-loop2', 301, true), ('/t-loop2', null, '/t-loop1', 301, true)`)
	ownerExec(t, s, `insert into cms_redirects (source_path, locale_code, target, status_code, deleted_at) values ('/t-gone', null, '/x', 301, now())`)
	t.Cleanup(func() { ownerExec(t, s, `delete from cms_redirects where source_path like '/t-%'`) })

	d := get(t, st, "/api/v1/public/redirects/resolve?path=/old-blog&locale=la")
	if d["target"] != "/blog" || d["status_code"] != float64(301) || d["hops"] != float64(1) {
		t.Errorf("all-locales rule: %v", d)
	}
	d = get(t, st, "/api/v1/public/redirects/resolve?path=/OLD-BLOG/?x=1&locale=en")
	if d["target"] != "/blog?en=1" || d["status_code"] != float64(302) {
		t.Errorf("locale rule wins: %v", d)
	}
	d = get(t, st, "/api/v1/public/redirects/resolve?path=/t-a&locale=la")
	if d["target"] != "https://example.com/x" || d["status_code"] != float64(308) || d["hops"] != float64(3) {
		t.Errorf("chain: %v", d)
	}
	for _, p := range []string{"/t-off", "/t-gone", "/t-loop1", "/nothing-here"} {
		r := st.App.Get(t, "/api/v1/public/redirects/resolve?path="+p+"&locale=la")
		if r.Status != 404 || r.Code() != "NOT_FOUND" {
			t.Errorf("%s: %d %s", p, r.Status, r.Body)
		}
	}
}

// Found answers live under the tag "redirects" with the normal TTL, a negative one for at most 30 s; a
// "redirects" invalidation (what the relay publishes after an admin change) clears both.
func TestDBRedirectCacheAndInvalidation(t *testing.T) {
	st, s, cache, r := cachedStack(t)
	ctx := context.Background()
	t.Cleanup(func() { ownerExec(t, s, `delete from cms_redirects where source_path like '/t-%'`) })
	miss := "/api/v1/public/redirects/resolve?path=/t-cache&locale=la"

	if x := st.App.Get(t, miss); x.Status != 404 {
		t.Fatalf("before: %d", x.Status)
	}
	keys, _ := cache.Members(ctx, "redirects")
	if len(keys) != 1 {
		t.Fatalf("negative answer not cached under redirects: %v", keys)
	}
	if ttl := r.Client.PTTL(ctx, keys[0]).Val(); ttl <= 0 || ttl > 30*time.Second {
		t.Errorf("negative TTL %v, want 0 < ttl <= 30s", ttl)
	}

	// created without an event: the stored 404 still answers
	ownerExec(t, s, `insert into cms_redirects (source_path, target, status_code) values ('/t-cache', '/blog', 307)`)
	if x := st.App.Get(t, miss); x.Status != 404 {
		t.Errorf("stored 404 should still answer: %d", x.Status)
	}
	if n, err := cache.Invalidate(ctx, []string{"redirects"}); err != nil || n != 1 {
		t.Fatalf("Invalidate: %d %v", n, err)
	}
	if x := st.App.Get(t, miss); x.Status != 200 || x.Data()["status_code"] != float64(307) {
		t.Fatalf("after the invalidation: %d %s", x.Status, x.Body)
	}
	keys, _ = cache.Members(ctx, "redirects")
	if len(keys) != 1 {
		t.Fatalf("found answer not tagged: %v", keys)
	}
	if ttl := r.Client.PTTL(ctx, keys[0]).Val(); ttl <= 30*time.Second {
		t.Errorf("found answer must use the default TTL, got %v", ttl)
	}
}

// The signed cms.invalidate message of the relay with the tag "redirects" clears a cached redirect answer (the
// real Subscriber on the real Redis).
func TestDBRedirectClearedBySignedInvalidation(t *testing.T) {
	st, s, cache, r := cachedStack(t)
	ctx := context.Background()
	t.Cleanup(func() { ownerExec(t, s, `delete from cms_redirects where source_path like '/t-%'`) })
	key := []byte("redirect-test-key-0123456789abcdef0123456789")
	ch := r.Prefix + "chan"
	sub, err := publiccache.NewSubscriber(publiccache.SubscriberOptions{
		Client: r.Client, Cache: cache, Channel: ch, Key: key, SecondDelay: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err = sub.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sub.Close() })

	target := "/api/v1/public/redirects/resolve?path=/t-sub&locale=la"
	if x := st.App.Get(t, target); x.Status != 404 {
		t.Fatalf("before: %d", x.Status)
	}
	ownerExec(t, s, `insert into cms_redirects (source_path, target, status_code) values ('/t-sub', '/blog', 301)`)
	msg, err := publiccache.Sign(key, publiccache.Event{EventID: "redir-1", EventType: publiccache.EventType, Tags: []string{"redirects"}, OccurredAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Client.Publish(ctx, ch, msg).Err(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for sub.Stats().Applied < 1 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if x := st.App.Get(t, target); x.Status != 200 || x.Data()["target"] != "/blog" {
		t.Errorf("after the signed message: %d %s", x.Status, x.Body)
	}
}
