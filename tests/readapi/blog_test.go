//go:build dbtest

package readapi

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/portalapp"
)

func postID(t *testing.T, e *env, publishedAt string) int64 {
	t.Helper()
	var id int64
	ownerScalar(t, e.s, &id, `select id from cms_posts where published_at = $1::timestamptz`, publishedAt)
	return id
}

func assetID(t *testing.T, e *env, key string) int64 {
	t.Helper()
	var id int64
	ownerScalar(t, e.s, &id, `select id from media_assets where storage_key = $1`, key)
	return id
}

const (
	post1At = "2026-03-01 10:00+00"
	post2At = "2026-04-01 10:00+00"
)

func TestPostsListHidesUnpublishedAndShapesItemsLikeTheFrontendLoader(t *testing.T) {
	e := wire(t)
	r := e.app.Get(t, "/api/v1/public/posts?locale=en", nil)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	b := decode(t, r.Body)
	if b["success"] != true || length(b["data"]) != 2 {
		t.Fatalf("only the two live posts are listed (draft, future and expired are hidden): %s", r.Body)
	}
	// newest first
	p2, p1 := postID(t, e, post2At), postID(t, e, post1At)
	if num(at(b, "data.0.id")) != p2 || num(at(b, "data.1.id")) != p1 {
		t.Fatalf("order: %v %v", at(b, "data.0.id"), at(b, "data.1.id"))
	}
	// post 1: a real English translation
	it := at(b, "data.1").(obj)
	for k, want := range map[string]any{
		"slug": "music-night", "href": "/blog/music-night", "resolved_locale": "en", "is_fallback": false,
		"title": "Music night", "excerpt": "Excerpt en", "label": "Music Night", "location_label": "Phou Khao Khouay",
		"date": "Mar. 01.2026", "date_time": "2026-03-01T10:00:00Z", "is_featured": true,
	} {
		if it[k] != want {
			t.Errorf("%s = %v, want %v", k, it[k], want)
		}
	}
	if at(it, "category.key") != "music" || at(it, "category.name") != "Music" || at(it, "author.name") != "Materm Team" {
		t.Errorf("category/author: %v %v", it["category"], it["author"])
	}
	if str(it["public_id"]) == "" || num(at(it, "author.id")) == 0 {
		t.Errorf("ids: %v", it)
	}
	card := assetID(t, e, "uploads/2026/photo a.webp") // teaser image beats the hero on cards
	if num(at(it, "image.media_id")) != card || at(it, "image.alt") != nil {
		t.Errorf("image: %v", it["image"])
	}
	// post 2: no English translation: the Lao row is shown, marked as a fallback
	fb := at(b, "data.0").(obj)
	if fb["slug"] != "camp-weekend" || fb["is_fallback"] != true || fb["resolved_locale"] != "la" || fb["title"] != "Camp weekend" {
		t.Errorf("fallback item: %v", fb)
	}
	if fb["label"] != nil || fb["author"] != nil {
		t.Errorf("nullable members must be null: %v", fb)
	}
	// media is a top-level member beside data and meta; the URLs are built from file.path_ip and the static prefix
	if at(b, "meta.total") != float64(2) || at(b, "meta.page") != float64(1) || at(b, "meta.limit") != float64(12) || at(b, "meta.total_page") != float64(1) {
		t.Errorf("meta: %v", b["meta"])
	}
	mc := at(b, fmt.Sprintf("media.%d", card))
	if str(at(mc, "url")) != "https://files.example.test/uploads/2026/photo%20a.webp" || num(at(mc, "width")) != 1200 {
		t.Errorf("media of the card: %v", mc)
	}
	if _, ok := b["media"].(obj)[fmt.Sprint(assetID(t, e, "uploads/2026/photo-b.webp"))]; !ok {
		t.Errorf("media of post 2 missing: %v", b["media"])
	}
	// headers
	for h, want := range map[string]string{
		"Cache-Control":    "public, max-age=30, stale-while-revalidate=300, stale-if-error=86400",
		"Content-Language": "en",
	} {
		if r.Header.Get(h) != want {
			t.Errorf("%s = %q", h, r.Header.Get(h))
		}
	}
	if !strings.HasPrefix(r.Header.Get("ETag"), `W/"`) || r.Header.Get("Vary") != "" && !strings.Contains(r.Header.Get("Vary"), "Origin") {
		t.Errorf("ETag %q Vary %q (?locale was given: no Vary)", r.Header.Get("ETag"), r.Header.Get("Vary"))
	}
}

func TestPostsListFiltersAndPagination(t *testing.T) {
	e := wire(t)
	list := func(q string) obj {
		t.Helper()
		r := e.app.Get(t, "/api/v1/public/posts?locale=en&"+q, nil)
		if r.Status != 200 {
			t.Fatalf("%s: %d %s", q, r.Status, r.Body)
		}
		return decode(t, r.Body)
	}
	if b := list("category=music"); length(b["data"]) != 1 || at(b, "data.0.slug") != "music-night" {
		t.Errorf("category=music: %v", b["data"])
	}
	if b := list("category=camping"); length(b["data"]) != 1 || at(b, "data.0.slug") != "camp-weekend" {
		t.Errorf("category=camping: %v", b["data"])
	}
	// a well-formed key that names nothing: 200 and an empty list (not a 404)
	if b := list("category=nonexistent"); length(b["data"]) != 0 || at(b, "meta.total") != float64(0) || b["data"] == nil {
		t.Errorf("unknown category: %v %v", b["data"], b["meta"])
	}
	if b := list("tag=night"); length(b["data"]) != 1 || at(b, "data.0.slug") != "music-night" {
		t.Errorf("tag=night: %v", b["data"])
	}
	if b := list("tag=trek"); length(b["data"]) != 0 {
		t.Errorf("tag=trek: %v", b["data"])
	}
	if b := list("category=camping&tag=night"); length(b["data"]) != 0 {
		t.Errorf("category and tag are ANDed: %v", b["data"])
	}
	// a page past the end is a normal answer with the real meta
	if b := list("page=9&limit=3"); length(b["data"]) != 0 || at(b, "meta.total") != float64(2) || at(b, "meta.page") != float64(9) || at(b, "meta.total_page") != float64(1) {
		t.Errorf("past the end: %v %v", b["data"], b["meta"])
	}
	// invalid parameters are 422 with in:query paths
	for q, path := range map[string]string{"limit=2": "/limit", "limit=49": "/limit", "page=0": "/page", "category=Bad": "/category", "tag=-x": "/tag"} {
		r := e.app.Get(t, "/api/v1/public/posts?locale=en&"+q, nil)
		b := decode(t, r.Body)
		if r.Status != 422 || b["code"] != "VALIDATION_FAILED" || at(b, "errors.0.path") != path || at(b, "errors.0.in") != "query" {
			t.Errorf("%s: %d %s", q, r.Status, r.Body)
		}
		if r.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("%s: Cache-Control %q", q, r.Header.Get("Cache-Control"))
		}
	}
}

func TestPostsPagination(t *testing.T) {
	e := wire(t)
	// five extra live posts in an otherwise unused category, removed again afterwards
	ownerExec(t, e.s, `
	  insert into cms_posts (category_id, status, published_at)
	  select (select id from cms_categories where key = 'hiking'), 'published', timestamptz '2026-05-01 10:00+00' + (n || ' hours')::interval from generate_series(1, 5) n;
	  insert into cms_post_translations (post_id, locale_code, slug, title)
	  select p.id, 'la', 'paging-' || row_number() over (order by p.published_at), 'Paging post' from cms_posts p where p.category_id = (select id from cms_categories where key = 'hiking');`)
	t.Cleanup(func() {
		ownerExec(t, e.s, `delete from cms_posts where category_id = (select id from cms_categories where key = 'hiking')`)
	})

	page := func(n int) obj {
		r := e.app.Get(t, fmt.Sprintf("/api/v1/public/posts?locale=la&category=hiking&limit=3&page=%d", n), nil)
		if r.Status != 200 {
			t.Fatalf("page %d: %d %s", n, r.Status, r.Body)
		}
		return decode(t, r.Body)
	}
	p1, p2, p3 := page(1), page(2), page(3)
	if length(p1["data"]) != 3 || length(p2["data"]) != 2 || length(p3["data"]) != 0 {
		t.Fatalf("sizes %d %d %d", length(p1["data"]), length(p2["data"]), length(p3["data"]))
	}
	for _, p := range []obj{p1, p2, p3} {
		if at(p, "meta.total") != float64(5) || at(p, "meta.total_page") != float64(2) || at(p, "meta.limit") != float64(3) {
			t.Errorf("meta %v", p["meta"])
		}
	}
	// newest first, no overlap between pages
	seen := map[float64]bool{}
	var last string
	for _, p := range []obj{p1, p2} {
		for _, it := range p["data"].([]any) {
			id := it.(obj)["id"].(float64)
			if seen[id] {
				t.Errorf("post %v on two pages", id)
			}
			seen[id] = true
			if dt := str(it.(obj)["date_time"]); last != "" && dt > last {
				t.Errorf("not newest first: %s after %s", dt, last)
			} else {
				last = dt
			}
		}
	}
	// the unfiltered total sees them too, then not any more after the cleanup (checked by the other tests)
	r := e.app.Get(t, "/api/v1/public/posts?locale=la&limit=48", nil)
	if at(decode(t, r.Body), "meta.total") != float64(7) {
		t.Errorf("total with the extra posts: %s", r.Body)
	}
}

func TestPostBySlugFlatShapeWithBodySeoAndMedia(t *testing.T) {
	e := wire(t)
	r := e.app.Get(t, "/api/v1/public/posts/music-night?locale=en", nil)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	d := decode(t, r.Body)["data"].(obj)
	if _, nested := d["post"]; nested {
		t.Fatal("P-08 is flat: there is no data.post")
	}
	id := postID(t, e, post1At)
	hero, card, photoB := assetID(t, e, "static/images/site/home/hero-bg.png"), assetID(t, e, "uploads/2026/photo a.webp"), assetID(t, e, "uploads/2026/photo-b.webp")
	for k, want := range map[string]any{
		"locale": "en", "slug": "music-night", "href": "/blog/music-night", "title": "Music night", "is_fallback": false,
		"resolved_locale": "en", "date": "Mar. 01.2026",
	} {
		if d[k] != want {
			t.Errorf("%s = %v, want %v", k, d[k], want)
		}
	}
	if num(d["id"]) != id || str(d["updated_at"]) == "" {
		t.Errorf("id/updated_at: %v %v", d["id"], d["updated_at"])
	}
	if num(at(d, "hero.media_id")) != hero || at(d, "hero.alt") != "Hero alt" {
		t.Errorf("hero: %v", d["hero"])
	}
	if at(d, "gallery.heading") != "Gallery" || at(d, "gallery.description") != "Photos of the night" || length(at(d, "gallery.items")) != 1 ||
		num(at(d, "gallery.items.0.media_id")) != photoB || at(d, "gallery.items.0.alt") != "Gallery alt en" || at(d, "gallery.items.0.caption") != "Gallery caption en" {
		t.Errorf("gallery: %v", d["gallery"])
	}
	if length(d["tags"]) != 1 || at(d, "tags.0.key") != "night" || at(d, "tags.0.name") != "Night" || at(d, "tags.0.slug") != "night" {
		t.Errorf("tags: %v", d["tags"])
	}
	// SEO: own title/description, the og image, and a canonical on the DEFAULT locale's slug at the configured host
	if at(d, "seo.title") != "SEO title" || at(d, "seo.description") != "SEO description" || num(at(d, "seo.og_image.media_id")) != card ||
		at(d, "seo.canonical") != "https://materm.example/blog/music-night-la" || at(d, "seo.noindex") != false {
		t.Errorf("seo: %v", d["seo"])
	}
	// the body: the page link became an href, the inline image joined the media map, nothing is HTML
	var pagePath string
	ownerScalar(t, e.s, &pagePath, `select path from cms_pages where id = 2`)
	if !strings.Contains(string(r.Body), `"href":"`+pagePath+`"`) {
		t.Errorf("link to page 2 (%s) not resolved: %s", pagePath, at(d, "body"))
	}
	if at(d, "body.type") != "doc" || length(at(d, "body.content")) != 2 || at(d, "body.content.1.type") != "image" {
		t.Errorf("body: %v", d["body"])
	}
	m := d["media"].(obj)
	for _, want := range []int64{hero, card, photoB} {
		if _, ok := m[fmt.Sprint(want)]; !ok {
			t.Errorf("media %d missing from %v", want, m)
		}
	}
	if str(at(m, fmt.Sprintf("%d.url", hero))) != "/images/site/home/hero-bg.png" {
		t.Errorf("static media url: %v", at(m, fmt.Sprintf("%d", hero)))
	}
	if r.Header.Get("Content-Language") != "en" || r.Header.Get("ETag") == "" {
		t.Errorf("headers %v", r.Header)
	}
}

func TestPostFallbackLocaleAndAcceptLanguage(t *testing.T) {
	e := wire(t)
	// post 2 exists only in Lao: English visitors get it at its own slug, flagged
	r := e.app.Get(t, "/api/v1/public/posts/camp-weekend?locale=en", nil)
	d := decode(t, r.Body)["data"].(obj)
	if r.Status != 200 || d["is_fallback"] != true || d["resolved_locale"] != "la" || d["locale"] != "en" || d["title"] != "Camp weekend" {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	// no ?locale: Accept-Language decides, and the response varies on it
	r = e.app.Get(t, "/api/v1/public/posts/music-night", map[string]string{"Accept-Language": "en-US,en;q=0.9"})
	if r.Status != 200 || at(decode(t, r.Body), "data.locale") != "en" || !strings.Contains(r.Header.Get("Vary"), "Accept-Language") {
		t.Fatalf("%d vary=%q %s", r.Status, r.Header.Get("Vary"), r.Body)
	}
	// "lo" is the ISO code of Lao: announced as lo, stored as la
	r = e.app.Get(t, "/api/v1/public/posts/music-night-la?locale=lo", nil)
	if r.Status != 200 || at(decode(t, r.Body), "data.locale") != "la" || r.Header.Get("Content-Language") != "lo" {
		t.Fatalf("%d lang=%q %s", r.Status, r.Header.Get("Content-Language"), r.Body)
	}
}

// doc/08 section 2.3.3: step 1 the visitor's own slug, step 2 the same slug owned by another locale (308 to the
// visitor's slug), step 3 404.
func TestPostSlugResolutionIn3Steps(t *testing.T) {
	e := wire(t)

	// step 1: la sees post 1 under its Lao slug
	if r := e.app.Get(t, "/api/v1/public/posts/music-night-la?locale=la", nil); r.Status != 200 || at(decode(t, r.Body), "data.slug") != "music-night-la" {
		t.Fatalf("step 1: %d %s", r.Status, r.Body)
	}
	// step 2: the English slug asked in Lao -> 308 to the Lao slug, with the envelope and the Location header
	r := e.app.Get(t, "/api/v1/public/posts/music-night?locale=la", nil)
	if r.Status != 308 || r.Header.Get("Location") != "/blog/music-night-la" {
		t.Fatalf("308 expected: %d %q %s", r.Status, r.Header.Get("Location"), r.Body)
	}
	b := decode(t, r.Body)
	if b["success"] != true || at(b, "data.redirect.location") != "/blog/music-night-la" || at(b, "data.redirect.status") != float64(308) {
		t.Errorf("redirect envelope: %s", r.Body)
	}
	if r.Header.Get("Cache-Control") != "public, max-age=30" {
		t.Errorf("308 Cache-Control %q", r.Header.Get("Cache-Control"))
	}
	// and the other way round
	r = e.app.Get(t, "/api/v1/public/posts/music-night-la?locale=en", nil)
	if r.Status != 308 || r.Header.Get("Location") != "/blog/music-night" {
		t.Fatalf("en: %d %q", r.Status, r.Header.Get("Location"))
	}
	// following the redirect lands on the post
	if r := e.app.Get(t, "/api/v1/public/posts/music-night?locale=en", nil); r.Status != 200 {
		t.Fatalf("target: %d", r.Status)
	}
	// step 3: nothing owns the slug (also not draft, scheduled in the future, or expired posts)
	for _, slug := range []string{"no-such-post", "draft-post", "future-post", "expired-post", "Bad_Slug", "a--b", strings.Repeat("a", 101), "-x"} {
		r := e.app.Get(t, "/api/v1/public/posts/"+slug+"?locale=en", nil)
		b := decode(t, r.Body)
		if r.Status != 404 || b["code"] != "NOT_FOUND" || b["request_id"] == "" {
			t.Errorf("%s: %d %s", slug, r.Status, r.Body)
		}
		if r.Header.Get("Cache-Control") != "public, max-age=30" {
			t.Errorf("%s: 404 Cache-Control %q", slug, r.Header.Get("Cache-Control"))
		}
		if r.Header.Get("Location") != "" {
			t.Errorf("%s: Location on a 404", slug)
		}
	}
}

// A slug that two different posts own in two locales: the visitor's own post wins (step 1); a third locale's
// visitor is sent to the default locale's post (default first, doc/08 section 2.3.3).
func TestSameSlugOfTwoPostsPrefersTheVisitorsOwn(t *testing.T) {
	e := wire(t)
	ownerExec(t, e.s, `
	  insert into cms_posts (category_id, status, published_at) values ((select id from cms_categories where key = 'outdoor'), 'published', timestamptz '2026-02-01 10:00+00');
	  insert into cms_post_translations (post_id, locale_code, slug, title)
	  select id, 'en', 'camp-weekend', 'English camp weekend' from cms_posts where published_at = timestamptz '2026-02-01 10:00+00';`)
	t.Cleanup(func() {
		ownerExec(t, e.s, `delete from cms_posts where published_at = timestamptz '2026-02-01 10:00+00'`)
	})

	// English: the real English translation wins over the Lao post that English only sees as a fallback
	r := e.app.Get(t, "/api/v1/public/posts/camp-weekend?locale=en", nil)
	d := decode(t, r.Body)["data"].(obj)
	if r.Status != 200 || d["title"] != "English camp weekend" || d["is_fallback"] != false {
		t.Fatalf("en: %d %s", r.Status, r.Body)
	}
	// Lao: its own post under that slug
	r = e.app.Get(t, "/api/v1/public/posts/camp-weekend?locale=la", nil)
	if r.Status != 200 || at(decode(t, r.Body), "data.title") != "Camp weekend" {
		t.Fatalf("la: %d %s", r.Status, r.Body)
	}
}

func TestCategoriesAndTags(t *testing.T) {
	e := wire(t)
	r := e.app.Get(t, "/api/v1/public/categories?locale=en", nil)
	b := decode(t, r.Body)
	if r.Status != 200 || length(b["data"]) != 5 || at(b, "data.0.key") != "music" || at(b, "data.0.name") != "Music" || at(b, "data.0.slug") != "music" || at(b, "data.0.sort_order") != float64(1) {
		t.Fatalf("categories: %d %s", r.Status, r.Body)
	}
	if _, hasMeta := b["meta"]; hasMeta {
		t.Error("categories are not paged")
	}
	r = e.app.Get(t, "/api/v1/public/tags?locale=en", nil)
	b = decode(t, r.Body)
	if r.Status != 200 || length(b["data"]) != 2 || at(b, "data.0.key") != "night" || at(b, "data.1.key") != "trek" || at(b, "data.0.name") != "Night" {
		t.Fatalf("tags: %d %s", r.Status, r.Body)
	}
	// an unsupported locale code is a 422; an unknown but well-formed one is the default locale
	if r := e.app.Get(t, "/api/v1/public/categories?locale=Not_A_Locale", nil); r.Status != 422 || at(decode(t, r.Body), "errors.0.in") != "query" || at(decode(t, r.Body), "errors.0.path") != "/locale" {
		t.Errorf("bad locale: %d %s", r.Status, r.Body)
	}
	if r := e.app.Get(t, "/api/v1/public/categories?locale=fr", nil); r.Status != 200 || r.Header.Get("Content-Language") != "lo" {
		t.Errorf("unknown locale: %d %q", r.Status, r.Header.Get("Content-Language"))
	}
}

func TestBlogEndpointsNeedThePortalKey(t *testing.T) {
	e := wire(t)
	for _, p := range []string{"/api/v1/public/posts", "/api/v1/public/posts/music-night", "/api/v1/public/categories", "/api/v1/public/tags",
		"/api/v1/public/albums", "/api/v1/public/albums/forest", "/api/v1/public/faq/contact", "/api/v1/public/forms/contact"} {
		if r := e.app.Do(t, portalapp.Request{Path: p, NoKey: true}); r.Status != 401 {
			t.Errorf("%s without a key: %d", p, r.Status)
		}
	}
	for _, en := range e.app.Registry.Entries() {
		if en.Policy.String() != "portal-key" {
			t.Errorf("%s %s declares %s", en.Method, en.Path, en.Policy)
		}
	}
	if n := len(e.app.Registry.Entries()); n != 9 {
		t.Errorf("%d routes registered, want 9 (P-07..P-15):\n%s", n, e.app.Registry.Describe())
	}
}

func TestETagAnd304(t *testing.T) {
	e := wire(t)
	for _, p := range []string{"/api/v1/public/posts?locale=en", "/api/v1/public/posts/music-night?locale=en", "/api/v1/public/categories?locale=en",
		"/api/v1/public/tags?locale=en", "/api/v1/public/albums?locale=en", "/api/v1/public/albums/forest?locale=en",
		"/api/v1/public/faq/contact?locale=en", "/api/v1/public/forms/contact?locale=en"} {
		r1 := e.app.Get(t, p, nil)
		etag := r1.Header.Get("ETag")
		if r1.Status != 200 || etag == "" {
			t.Fatalf("%s: %d %q", p, r1.Status, etag)
		}
		r2 := e.app.Get(t, p, map[string]string{"If-None-Match": etag})
		if r2.Status != 304 || len(r2.Body) != 0 || r2.Header.Get("ETag") != etag {
			t.Errorf("%s: If-None-Match -> %d (%d bytes)", p, r2.Status, len(r2.Body))
		}
		// a cache hit is byte-identical: the ETag is stable across the cache
		r3 := e.app.Get(t, p, nil)
		if r3.Header.Get("ETag") != etag || string(r3.Body) != string(r1.Body) {
			t.Errorf("%s: the second answer differs", p)
		}
		if r4 := e.app.Get(t, p, map[string]string{"If-None-Match": `W/"0000000000000000"`}); r4.Status != 200 {
			t.Errorf("%s: stale validator -> %d", p, r4.Status)
		}
	}
}

// The cache tags of doc/08 section 2.8: what a response is stored under, so that the invalidation subscriber
// can drop it.
func TestCacheTagsAreEmittedAndInvalidationReachesTheEntries(t *testing.T) {
	e := wire(t)
	ctx := context.Background()
	id := postID(t, e, post1At)
	postTag := fmt.Sprintf("post:%d", id)

	e.app.Get(t, "/api/v1/public/posts?locale=en", nil)
	e.app.Get(t, "/api/v1/public/posts/music-night?locale=en", nil)
	e.app.Get(t, "/api/v1/public/posts/music-night-la?locale=en", nil) // the 308
	e.app.Get(t, "/api/v1/public/posts/nothing-here?locale=en", nil)   // the 404 is a cached value too
	e.app.Get(t, "/api/v1/public/categories?locale=en", nil)
	e.app.Get(t, "/api/v1/public/tags?locale=en", nil)

	if n := len(members(t, e, "posts:list")); n != 6 {
		t.Errorf("posts:list holds %d entries, want 6 (list, post, 308, 404, categories, tags)", n)
	}
	if n := len(members(t, e, postTag)); n != 2 {
		t.Errorf("%s holds %d entries, want 2 (the post and its 308)", postTag, n)
	}
	// the post depends on the settings (canonical, default og) and, through its body link, on the pages
	for _, tag := range []string{"settings", "pages", "locales"} {
		if len(members(t, e, tag)) == 0 {
			t.Errorf("no entry is tagged %q", tag)
		}
	}

	// proof that the second answer is served from the cache: change the title behind the cache's back
	ownerExec(t, e.s, `update cms_post_translations set title = 'Changed title' where post_id = `+fmt.Sprint(id)+` and locale_code = 'en'`)
	t.Cleanup(func() {
		ownerExec(t, e.s, `update cms_post_translations set title = 'Music night' where post_id = `+fmt.Sprint(id)+` and locale_code = 'en'`)
	})
	if got := at(decode(t, e.app.Get(t, "/api/v1/public/posts/music-night?locale=en", nil).Body), "data.title"); got != "Music night" {
		t.Fatalf("expected the cached answer, got %v", got)
	}
	// the invalidation message of the relay (tag post:<id>) drops it, and the next request reads the database
	if _, err := e.cache.Invalidate(ctx, []string{postTag}); err != nil {
		t.Fatal(err)
	}
	if n := len(members(t, e, postTag)); n != 0 {
		t.Errorf("%d entries survived the invalidation", n)
	}
	if got := at(decode(t, e.app.Get(t, "/api/v1/public/posts/music-night?locale=en", nil).Body), "data.title"); got != "Changed title" {
		t.Fatalf("after invalidation: %v", got)
	}
	// an unrelated entry is untouched: the 404 for "nothing-here" is still cached
	if len(members(t, e, "posts:list")) == 0 {
		t.Error("posts:list entries vanished with a post:<id> invalidation")
	}
	// the 404 is invalidated by the generic posts:list tag every post change carries
	if _, err := e.cache.Invalidate(ctx, []string{"posts:list"}); err != nil {
		t.Fatal(err)
	}
	if n := len(members(t, e, "posts:list")); n != 0 {
		t.Errorf("%d entries survived posts:list", n)
	}
}

// With Redis gone the portal still reads (cache.go: the database answers); only writes need Redis.
func TestReadsSurviveRedisDown(t *testing.T) {
	e := wire(t, wireOpts{DeadRedis: true})
	for _, p := range []string{"/api/v1/public/posts?locale=en", "/api/v1/public/posts/music-night?locale=en", "/api/v1/public/categories?locale=en",
		"/api/v1/public/albums/forest?locale=en", "/api/v1/public/faq/contact?locale=en", "/api/v1/public/forms/contact?locale=en"} {
		if r := e.app.Get(t, p, nil); r.Status != 200 {
			t.Errorf("%s with Redis down: %d %s", p, r.Status, r.Body)
		}
	}
	if r := e.app.Get(t, "/api/v1/public/posts/music-night-la?locale=en", nil); r.Status != 308 {
		t.Errorf("308 with Redis down: %d", r.Status)
	}
}

// The SEO fallback chain (title -> seo_title, description -> seo_description -> excerpt -> site default,
// og image -> og_image_id -> hero -> card -> site default, canonical on the default locale's slug).
func TestPostSEOFallbackChain(t *testing.T) {
	e := wire(t)
	photoB := assetID(t, e, "uploads/2026/photo-b.webp")

	// post 2 has no seo overrides: the title, the excerpt, the hero image
	d := decode(t, e.app.Get(t, "/api/v1/public/posts/camp-weekend?locale=en", nil).Body)["data"].(obj)
	if at(d, "seo.title") != "Camp weekend" || at(d, "seo.description") != "Camping excerpt" || num(at(d, "seo.og_image.media_id")) != photoB ||
		at(d, "seo.canonical") != "https://materm.example/blog/camp-weekend" {
		t.Errorf("seo of a post without overrides: %v", d["seo"])
	}
	if _, extra := at(d, "seo.og_image").(obj)["alt"]; extra {
		t.Error("seo.og_image is {media_id} only")
	}

	// a post with no excerpt, no seo text and no image: the site's default description, no og image
	ownerExec(t, e.s, `
	  insert into cms_posts (category_id, status, published_at) values ((select id from cms_categories where key = 'outdoor'), 'published', timestamptz '2026-02-02 10:00+00');
	  insert into cms_post_translations (post_id, locale_code, slug, title)
	  select id, 'la', 'bare-post', 'Bare post' from cms_posts where published_at = timestamptz '2026-02-02 10:00+00';`)
	t.Cleanup(func() {
		ownerExec(t, e.s, `delete from cms_posts where published_at = timestamptz '2026-02-02 10:00+00'`)
	})
	d = decode(t, e.app.Get(t, "/api/v1/public/posts/bare-post?locale=la", nil).Body)["data"].(obj)
	if !strings.Contains(str(at(d, "seo.description")), "Materm") || at(d, "seo.og_image") != nil || at(d, "image") != nil || at(d, "hero") != nil || at(d, "excerpt") != nil {
		t.Errorf("bare post: %v", d)
	}
	if length(at(d, "gallery.items")) != 0 || length(d["tags"]) != 0 || at(d, "body") != nil {
		t.Errorf("empty collections must be [] and a missing body null: %v", d)
	}

	// the site's default og image is the last resort
	ownerExec(t, e.s, fmt.Sprintf(`update cms_site_settings set media_asset_id = %d where group_key = 'seo' and key = 'default_og_media_id'`, photoB))
	t.Cleanup(func() {
		ownerExec(t, e.s, `update cms_site_settings set media_asset_id = null where group_key = 'seo' and key = 'default_og_media_id'`)
	})
	e2 := wire(t) // a cold cache
	d = decode(t, e2.app.Get(t, "/api/v1/public/posts/bare-post?locale=la", nil).Body)["data"].(obj)
	if num(at(d, "seo.og_image.media_id")) != photoB {
		t.Errorf("default og image: %v", d["seo"])
	}
}
