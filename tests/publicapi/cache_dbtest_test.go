//go:build dbtest

package publicapi

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// members counts the cache keys registered under a tag.
func members(t *testing.T, cache interface {
	Members(ctx context.Context, tag string) ([]string, error)
}, tag string) int {
	t.Helper()
	keys, err := cache.Members(context.Background(), tag)
	if err != nil {
		t.Fatal(err)
	}
	return len(keys)
}

// Every endpoint stores its answer under the tags of doc/08 section 2.8 (plus the additions listed in the
// report: pages on menus/site/page, settings where seo/contact/base_url are read, ui:<chain>).
func TestCachedResponsesCarryTheirTags(t *testing.T) {
	st, s, cache, _ := cachedStack(t)
	labID := ownerInt64(t, s, `select id from cms_pages where key = 'lab'`)
	albumID := ownerInt64(t, s, `select id from cms_albums where key = 'forest'`)
	faqID := ownerInt64(t, s, `select id from cms_faq_groups where key = 'contact'`)
	formID := ownerInt64(t, s, `select id from forms_forms where slug = 'contact'`)

	type tc struct {
		target string
		tags   []string
	}
	for _, c := range []tc{
		{"/api/v1/public/locales", []string{"locales"}},
		{"/api/v1/public/settings?locale=en", []string{"settings", "locales"}},
		{"/api/v1/public/ui-strings?locale=en", []string{"ui:en", "ui:la", "locales"}},
		{"/api/v1/public/menus/footer?locale=en", []string{"menu:footer", "pages", "locales"}},
		{"/api/v1/public/site?locale=en", []string{"settings", "locales", "pages", "ui:en", "ui:la", "menu:main", "menu:footer", "menu:social"}},
		{"/api/v1/public/sitemap", []string{"sitemap", "pages", "posts:list", "settings", "locales"}},
		{"/api/v1/public/pages/resolve?path=/lab&locale=en", []string{
			"pages", fmt.Sprintf("page:%d", labID), "settings", "locales", "posts:list", fmt.Sprintf("album:%d", albumID), "albums:list",
			fmt.Sprintf("faq:%d", faqID), fmt.Sprintf("form:%d", formID)}},
	} {
		if r := st.App.Get(t, c.target); r.Status != 200 {
			t.Fatalf("%s: %d %s", c.target, r.Status, r.Body)
		}
		for _, tag := range c.tags {
			if members(t, cache, tag) == 0 {
				t.Errorf("%s: no cache key under tag %q", c.target, tag)
			}
		}
	}
	// a tag the page does not depend on has no members from it
	if members(t, cache, "post:1") != 0 || members(t, cache, "menu:main") != 1 {
		t.Errorf("unexpected tag membership: post:1=%d menu:main=%d", members(t, cache, "post:1"), members(t, cache, "menu:main"))
	}
}

func TestCacheHitMissAndInvalidation(t *testing.T) {
	st, s, cache, r := cachedStack(t)
	ctx := context.Background()
	labID := ownerInt64(t, s, `select id from cms_pages where key = 'lab'`)
	target := "/api/v1/public/pages/resolve?path=/lab&locale=en"

	first := st.App.Get(t, target)
	if first.Status != 200 || first.Data()["page"].(map[string]any)["title"] != "Lab en" {
		t.Fatalf("first: %d %s", first.Status, first.Body)
	}
	// one entry in Redis, under the page resource and the locale
	keys, _ := cache.Members(ctx, fmt.Sprintf("page:%d", labID))
	if len(keys) != 1 || !strings.Contains(keys[0], ":page:en:") || !strings.HasPrefix(keys[0], r.Prefix) {
		t.Fatalf("cache keys: %v", keys)
	}

	// an edit that nobody announced: the cached answer still serves (a HIT), byte for byte (same ETag)
	ownerExec(t, s, `update cms_page_translations set title = 'Lab en (edited)' where locale_code = 'en' and page_id = $1`, labID)
	defer ownerExec(t, s, `update cms_page_translations set title = 'Lab en' where locale_code = 'en' and page_id = $1`, labID)
	hit := st.App.Get(t, target)
	if string(hit.Body) != string(first.Body) || hit.Header.Get("ETag") != first.Header.Get("ETag") {
		t.Fatal("the second answer must come from the cache, identical to the first")
	}

	// the conditional GET works on a cache hit too: a matching validator is a 304 without a body
	nm := st.App.Get(t, target, "If-None-Match: "+first.Header.Get("ETag"))
	if nm.Status != 304 || len(nm.Body) != 0 || nm.Header.Get("ETag") != first.Header.Get("ETag") || nm.Header.Get("Content-Language") != "en" {
		t.Errorf("304 on a hit: %d body=%d %v", nm.Status, len(nm.Body), nm.Header)
	}

	// another locale is another entry
	if la := st.App.Get(t, "/api/v1/public/pages/resolve?path=/lab&locale=la"); la.Data()["page"].(map[string]any)["title"] != "Lab la" {
		t.Errorf("la: %s", la.Body)
	}
	if keys, _ := cache.Members(ctx, fmt.Sprintf("page:%d", labID)); len(keys) != 2 {
		t.Errorf("one entry per locale expected, got %v", keys)
	}

	// the invalidation event of the relay (page:<id>) clears it; the next request reads the new title
	if n, err := cache.Invalidate(ctx, []string{fmt.Sprintf("page:%d", labID)}); err != nil || n != 2 {
		t.Fatalf("Invalidate: %d %v", n, err)
	}
	fresh := st.App.Get(t, target)
	if fresh.Data()["page"].(map[string]any)["title"] != "Lab en (edited)" || fresh.Header.Get("ETag") == first.Header.Get("ETag") {
		t.Errorf("after the invalidation: %s", fresh.Body)
	}

	// the "pages" tag (sent with every page change) clears every page response at once
	if n, _ := cache.Invalidate(ctx, []string{"pages"}); n == 0 {
		t.Error("the pages tag must clear the cached pages")
	}
	// settings changes clear pages, site, settings and the sitemap
	st.App.Get(t, "/api/v1/public/sitemap")
	st.App.Get(t, "/api/v1/public/site?locale=la")
	before := members(t, cache, "settings")
	if before < 2 {
		t.Fatalf("settings members: %d", before)
	}
	if _, err := cache.Invalidate(ctx, []string{"settings"}); err != nil || members(t, cache, "settings") != 0 {
		t.Errorf("settings tag not cleared: %v", err)
	}
}

// A 404 is not stored: a page scheduled for the future appears when its time comes, with no event at all.
func TestANotFoundIsNeverCached(t *testing.T) {
	st, s, cache, _ := cachedStack(t)
	target := "/api/v1/public/pages/resolve?path=/scheduled"
	if r := st.App.Get(t, target); r.Status != 404 {
		t.Fatalf("scheduled page before its time: %d", r.Status)
	}
	ownerExec(t, s, `update cms_pages set published_at = now() - interval '1 minute' where key = 'scheduled'`)
	defer ownerExec(t, s, `update cms_pages set published_at = now() + interval '30 days' where key = 'scheduled'`)
	if r := st.App.Get(t, target); r.Status != 200 {
		t.Fatalf("scheduled page after its time: %d %s", r.Status, r.Body)
	}
	_ = cache
}

// Redis down (or not configured): the database answers, never an error (doc/08 section 2.7 item 5).
func TestRedisDownStillAnswers(t *testing.T) {
	_, s := dbStack(t)
	repos := dbRepos(t, s)
	st := wire(t, repos, deadRedisCache(), dbMedia(), "")
	for _, target := range []string{
		"/api/v1/public/locales", "/api/v1/public/site?locale=en", "/api/v1/public/pages/resolve?path=/lab&locale=en", "/api/v1/public/sitemap",
	} {
		if r := st.App.Get(t, target); r.Status != 200 {
			t.Errorf("%s with Redis down: %d %s", target, r.Status, r.Body)
		}
	}
}
