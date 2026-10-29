package previewapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	previewrepos "github.com/materm-organization/materm-web-portal-backend/modules/preview/repositories"
	previewservices "github.com/materm-organization/materm-web-portal-backend/modules/preview/services"
)

// ---- post snapshots (the image of doc/04 section 5.2 as the admin's posts module writes it) ------------------------

type m = map[string]any

func doc(text string) m {
	return m{"type": "doc", "content": []any{m{"type": "paragraph", "content": []any{m{"type": "text", "text": text}}}}}
}

// postSnapshot is a full snapshot: two languages with different slugs, a category that the bundle publishes,
// a hero, a gallery row. Callers change what they test.
func postSnapshot() m {
	return m{
		"post": m{"category_id": 1, "author_id": nil, "hero_media_id": 16, "teaser_media_id": nil, "is_featured": false},
		"translations": []any{
			m{"locale_code": "en", "slug": "draft-title-en", "title": "Draft title", "excerpt": "Draft excerpt", "label": "Draft label",
				"location_label": "Somewhere", "body": doc("Draft body en"), "gallery_heading": "Photos", "gallery_description": "Some photos",
				"hero_alt": "Hero alt en", "seo_title": "SEO en", "seo_description": "SEO description", "og_image_id": nil, "is_enabled": true},
			m{"locale_code": "la", "slug": "draft-title-la", "title": "Draft title la", "excerpt": "Draft excerpt la", "label": nil,
				"location_label": nil, "body": doc("Draft body la"), "gallery_heading": nil, "gallery_description": nil,
				"hero_alt": nil, "seo_title": nil, "seo_description": nil, "og_image_id": nil, "is_enabled": true},
		},
		"tag_ids": []any{},
		"media": []any{
			m{"id": 7, "media_id": 3, "sort_order": 2, "translations": []any{m{"locale_code": "en", "alt": "Second", "caption": "Cap 2"}}},
			m{"id": 6, "media_id": 2, "sort_order": 1, "translations": []any{m{"locale_code": "la", "alt": "Alt la", "caption": nil}, m{"locale_code": "en", "alt": "First", "caption": "Cap 1"}}},
		},
	}
}

func (st *stack) issuePost(seed string, entityID int64, locale string, snap any) string {
	tok := makeToken(seed)
	var loc *string
	if locale != "" {
		loc = &locale
	}
	st.Tokens.add(tok, previewrepos.Consumed{EntityType: "cms.posts", EntityID: entityID, LocaleCode: loc, Snapshot: mustJSON(snap)}, time.Hour)
	return tok
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func postOf(t *testing.T, r interface{ Data() map[string]any }) map[string]any {
	t.Helper()
	p, ok := r.Data()["post"].(map[string]any)
	if !ok {
		t.Fatalf("no data.post in %v", r.Data())
	}
	return p
}

// The answer has the shape of P-08 inside data.post, is marked noindex, and carries the usual preview info.
func TestPostPreviewShape(t *testing.T) {
	st := newStack(t)
	tok := st.issuePost("post-shape", 41, "en", postSnapshot())
	r := st.resolve(t, tok, "")
	if r.Status != http.StatusOK {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	d := r.Data()
	if d["kind"] != "post" || d["locale"] != "en" {
		t.Fatalf("kind/locale: %v %v", d["kind"], d["locale"])
	}
	if _, has := d["page"]; has {
		t.Error("a post preview carries no data.page")
	}
	p := postOf(t, r)
	for k, want := range map[string]any{
		"id": float64(41), "slug": "draft-title-en", "href": "/blog/draft-title-en", "title": "Draft title", "excerpt": "Draft excerpt",
		"label": "Draft label", "location_label": "Somewhere", "resolved_locale": "en", "is_fallback": false, "locale": "en",
	} {
		if p[k] != want {
			t.Errorf("post.%s = %v, want %v", k, p[k], want)
		}
	}
	// a draft that was never published: the date is the render time, not empty
	if p["date"] == "" || p["date_time"] == "" || p["updated_at"] == "" {
		t.Errorf("dates: %v %v %v", p["date"], p["date_time"], p["updated_at"])
	}
	seo := p["seo"].(map[string]any)
	if seo["noindex"] != true || seo["title"] != "SEO en" || seo["description"] != "SEO description" {
		t.Errorf("seo: %v", seo)
	}
	// the canonical URL is on the DEFAULT locale's slug (la)
	if c, _ := seo["canonical"].(string); !strings.HasSuffix(c, "/blog/draft-title-la") {
		t.Errorf("canonical %q", c)
	}
	if hero := p["hero"].(map[string]any); hero["media_id"] != float64(16) || hero["alt"] != "Hero alt en" {
		t.Errorf("hero: %v", hero)
	}
	if cat := p["category"].(map[string]any); cat["key"] == nil || cat["name"] == nil {
		t.Errorf("category: %v", p["category"])
	}
	body := p["body"].(map[string]any)
	if !strings.Contains(string(mustJSON(body)), "Draft body en") {
		t.Errorf("body: %v", body)
	}
	// the gallery is ordered by sort_order and takes alt / caption from the first language of the chain that has a row
	g := p["gallery"].(map[string]any)
	items := g["items"].([]any)
	if g["heading"] != "Photos" || len(items) != 2 {
		t.Fatalf("gallery: %v", g)
	}
	if first := items[0].(map[string]any); first["media_id"] != float64(2) || first["alt"] != "First" || first["caption"] != "Cap 1" {
		t.Errorf("first picture: %v", first)
	}
	if _, ok := p["media"].(map[string]any); !ok {
		t.Errorf("media map: %v", p["media"])
	}
	info := d["preview"].(map[string]any)
	if info["entity_type"] != "cms.posts" || info["entity_id"] != float64(41) || info["locale"] != "en" {
		t.Errorf("preview info: %v", info)
	}
	if _, ok := info["warnings"].([]any); !ok {
		t.Errorf("warnings must be an array: %v", info["warnings"])
	}
	for h, want := range map[string]string{"Cache-Control": "no-store"} {
		if r.Header.Get(h) != want {
			t.Errorf("%s = %q", h, r.Header.Get(h))
		}
	}
	if !strings.Contains(r.Header.Get("X-Robots-Tag"), "noindex") || r.Header.Get("ETag") != "" {
		t.Errorf("headers: %v", r.Header)
	}
}

// ?locale picks the language of the answer; the link's language is the default; a language without a row falls
// back along the chain and says so.
func TestPostPreviewLocale(t *testing.T) {
	st := newStack(t)
	tok := st.issuePost("post-loc", 41, "en", postSnapshot())
	if p := postOf(t, st.resolve(t, tok, "?locale=la")); p["slug"] != "draft-title-la" || p["title"] != "Draft title la" || p["resolved_locale"] != "la" {
		t.Errorf("la: %v", p["slug"])
	}
	// only the default language has a row: en falls back to it
	only := postSnapshot()
	only["translations"] = only["translations"].([]any)[1:]
	tok2 := st.issuePost("post-fb", 41, "en", only)
	p := postOf(t, st.resolve(t, tok2, ""))
	if p["resolved_locale"] != "la" || p["is_fallback"] != true || p["slug"] != "draft-title-la" {
		t.Errorf("fallback: %v %v %v", p["resolved_locale"], p["is_fallback"], p["slug"])
	}
	// a disabled translation in the chain is skipped; with nothing else enabled the draft still renders (it is the editor's own work)
	dis := postSnapshot()
	trs := dis["translations"].([]any)
	trs[0].(m)["is_enabled"] = false
	if p := postOf(t, st.resolve(t, st.issuePost("post-dis", 41, "en", dis), "")); p["resolved_locale"] != "la" {
		t.Errorf("disabled en: %v", p["resolved_locale"])
	}
	trs[1].(m)["is_enabled"] = false
	if r := st.resolve(t, st.issuePost("post-dis2", 41, "en", dis), ""); r.Status != http.StatusOK {
		t.Errorf("all disabled: %d %s", r.Status, r.Body)
	}
}

// ?path is /blog/<slug of ANY language of the post> (the editor may switch language after the link was issued);
// another post, a page path or a bare /blog is the one 401.
func TestPostPreviewPath(t *testing.T) {
	st := newStack(t)
	tok := st.issuePost("post-path", 41, "en", postSnapshot())
	for _, ok := range []string{"/blog/draft-title-en", "/blog/draft-title-la"} {
		if r := st.resolve(t, tok, "?path="+ok); r.Status != http.StatusOK {
			t.Errorf("%s: %d %s", ok, r.Status, r.Body)
		}
		if r := st.resolve(t, tok, "?locale=la&path="+ok); r.Status != http.StatusOK {
			t.Errorf("la %s: %d", ok, r.Status)
		}
	}
	for _, bad := range []string{"/blog/other-post", "/our-path", "/blog", "/blog/", "/blog/draft-title-en/x", "/draft-title-en", "/blog/draft-title"} {
		r := st.resolve(t, tok, "?path="+bad)
		if r.Status != http.StatusUnauthorized || r.Code() != "INVALID_TOKEN" {
			t.Errorf("%q: %d %s", bad, r.Status, r.Body)
		}
	}
	if r := st.resolve(t, tok, ""); r.Status != http.StatusOK {
		t.Errorf("no ?path: %d", r.Status)
	}
}

// Draft tolerance: the smallest usable snapshot renders; what is not published is a warning with a JSON pointer
// into the snapshot, never a failure.
func TestPostPreviewWarningsAndSparseSnapshots(t *testing.T) {
	st := newStack(t)
	min := m{"post": m{}, "translations": []any{m{"locale_code": "en", "slug": "bare", "title": "Bare"}}}
	r := st.resolve(t, st.issuePost("post-min", 5, "", min), "?locale=en")
	if r.Status != http.StatusOK {
		t.Fatalf("minimal snapshot: %d %s", r.Status, r.Body)
	}
	p := postOf(t, r)
	if p["hero"] != nil || p["category"] != nil || p["image"] != nil || p["excerpt"] != nil {
		t.Errorf("missing parts must be null: hero %v category %v image %v", p["hero"], p["category"], p["image"])
	}
	if items := p["gallery"].(map[string]any)["items"].([]any); len(items) != 0 {
		t.Errorf("gallery items: %v", items)
	}
	if tags := p["tags"].([]any); len(tags) != 0 {
		t.Errorf("tags: %v", tags)
	}

	// an unpublished category, a tag and links to pages / posts that are not published
	snap := postSnapshot()
	snap["post"].(m)["category_id"] = 9999
	snap["tag_ids"] = []any{77}
	snap["translations"].([]any)[0].(m)["body"] = m{"type": "doc", "content": []any{m{"type": "paragraph", "content": []any{
		m{"type": "text", "text": "a page", "marks": []any{m{"type": "link", "attrs": m{"page_id": 9998}}}},
		m{"type": "text", "text": "a post", "marks": []any{m{"type": "link", "attrs": m{"post_id": 9997}}}},
	}}}}
	r = st.resolve(t, st.issuePost("post-warn", 41, "en", snap), "")
	if r.Status != http.StatusOK {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if postOf(t, r)["category"] != nil {
		t.Error("an unpublished category must not be shown")
	}
	got := map[string]string{}
	for _, w := range r.Data()["preview"].(map[string]any)["warnings"].([]any) {
		w := w.(map[string]any)
		got[w["code"].(string)] = w["path"].(string)
	}
	want := map[string]string{
		"reference_not_published": "", // category AND tag: both use this code; the last one wins in this map
		"link_page_not_published": "/translations/0/body",
		"post_not_published":      "/translations/0/body",
	}
	for code, path := range want {
		g, ok := got[code]
		if !ok || (path != "" && g != path) {
			t.Errorf("warning %s: got %q (present %v), want %q", code, g, ok, path)
		}
	}
	if !strings.Contains(string(r.Body), "/post/category_id") || !strings.Contains(string(r.Body), "/tag_ids/0") {
		t.Errorf("the category and the tag are reported by pointer: %s", r.Body)
	}
	if strings.Contains(string(r.Body), "9999") || strings.Contains(string(r.Body), "9998") {
		t.Errorf("a warning must not quote ids/values from the snapshot: %s", r.Body)
	}
}

// A published post is previewed with its live public id and date; a bad snapshot is never quoted back.
func TestPostPreviewBrokenSnapshot(t *testing.T) {
	st := newStack(t)
	for name, snap := range map[string]string{
		"not json":   `SECRET-CONTENT{`,
		"no slug":    `{"post":{},"translations":[{"locale_code":"en","slug":"","title":"SECRET-TITLE"}]}`,
		"wrong type": `{"post":{"category_id":"SECRET-ID"}}`,
	} {
		tok := makeToken("post-broken-" + name)
		st.Tokens.add(tok, previewrepos.Consumed{EntityType: "cms.posts", EntityID: 3, Snapshot: []byte(snap)}, time.Hour)
		r := st.resolve(t, tok, "")
		if r.Status != http.StatusInternalServerError {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
		if strings.Contains(string(r.Body), "SECRET") {
			t.Errorf("%s: the answer quotes the snapshot: %s", name, r.Body)
		}
	}
}

// The live row of a PUBLISHED post lends its public id and the date it went live (the snapshot holds neither);
// an editor-set published_at in the snapshot wins.
func TestPostPreviewUsesTheLiveRow(t *testing.T) {
	st := newStack(t)
	snap := postSnapshot()
	p := postOf(t, st.resolve(t, st.issuePost("post-live", 1, "en", snap), ""))
	if p["public_id"] == "" || p["public_id"] == nil || p["date_time"] != "2026-09-05T00:00:00Z" {
		t.Errorf("live row: public_id %v date_time %v", p["public_id"], p["date_time"])
	}
	snap["post"].(m)["published_at"] = "2027-01-02T03:04:05Z"
	p = postOf(t, st.resolve(t, st.issuePost("post-live2", 1, "en", snap), ""))
	if p["date_time"] != "2027-01-02T03:04:05Z" {
		t.Errorf("snapshot date: %v", p["date_time"])
	}
	// never published: no public id
	if p = postOf(t, st.resolve(t, st.issuePost("post-live3", 4242, "en", postSnapshot()), "")); p["public_id"] != "" {
		t.Errorf("an unpublished post has no public id: %v", p["public_id"])
	}
}

// With no post renderer wired (Deps.Posts nil) a post token is answered like any unusable token.
func TestPostTokenWithoutRendererIsUnusable(t *testing.T) {
	st := newStack(t, func(o *opts) {
		o.svc = func(d *previewservices.Deps) { d.Posts = nil }
	})
	r := st.resolve(t, st.issuePost("post-off", 41, "en", postSnapshot()), "")
	if r.Status != http.StatusUnauthorized || r.Code() != "INVALID_TOKEN" {
		t.Errorf("%d %s", r.Status, r.Body)
	}
}
