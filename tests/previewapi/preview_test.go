package previewapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/entities"
	previewrepos "github.com/materm-organization/materm-web-portal-backend/modules/preview/repositories"
	previewservices "github.com/materm-organization/materm-web-portal-backend/modules/preview/services"
	"github.com/materm-organization/materm-web-portal-backend/pkg/assemble"
	"github.com/materm-organization/materm-web-portal-backend/pkg/logs"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/apitest"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/jsondiff"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/seedfake"
)

// pageMask ignores what a preview cannot share with the live page: generated ids, the render time, and noindex
// (a preview is always noindex).
func pageMask(path string) bool {
	return path == ".page.public_id" || path == ".page.updated_at" || path == ".page.seo.noindex" ||
		(strings.HasPrefix(path, ".sections[") && strings.HasSuffix(path, "].id"))
}

// publicPage is what P-06 answers for a bundled page, from the same fixture as the snapshot.
func (st *stack) publicPage(t *testing.T, loc, key string) (any, int64) {
	t.Helper()
	set := st.DB.Set()
	page, err := set.Pages.FindByKey(context.Background(), loc, key)
	if err != nil {
		t.Fatal(err)
	}
	secs, _ := set.Pages.Sections(context.Background(), page.PageID)
	res, err := st.Public.Page(context.Background(), assemble.PageInput{Locale: loc, Chain: st.DB.Chain(loc), Page: page, Sections: secs})
	if err != nil {
		t.Fatal(err)
	}
	g, err := jsondiff.ToGeneric(res.Data)
	if err != nil {
		t.Fatal(err)
	}
	return g, page.PageID
}

// A valid token renders the frozen snapshot through the SAME assembler as P-06: for the five bundled pages in
// both locales the preview's data.page equals the public body.
func TestPreviewEqualsThePublicPage(t *testing.T) {
	st := newStack(t)
	for _, loc := range []string{"la", "en"} {
		for _, key := range seedfake.PageKeys {
			t.Run(loc+"/"+key, func(t *testing.T) {
				want, id := st.publicPage(t, loc, key)
				tok := st.issue(loc+key, id, "", snapshotOf(t, key))
				r := st.resolve(t, tok, "?locale="+loc)
				if r.Status != http.StatusOK {
					t.Fatalf("%d %s", r.Status, r.Body)
				}
				d := r.Data()
				if d["kind"] != "page" || d["locale"] != loc {
					t.Fatalf("kind/locale: %v %v", d["kind"], d["locale"])
				}
				got, _ := jsondiff.ToGeneric(d["page"])
				o := jsondiff.Options{Mask: pageMask, Extras: map[string]bool{}}
				if diffs := jsondiff.Diff(want, got, o); len(diffs) > 0 {
					t.Fatalf("%d differences from P-06:\n%s", len(diffs), strings.Join(diffs, "\n"))
				}
				if seo := got.(map[string]any)["page"].(map[string]any)["seo"].(map[string]any); seo["noindex"] != true {
					t.Errorf("a preview must be noindex, got %v", seo["noindex"])
				}
				info := d["preview"].(map[string]any)
				if info["entity_type"] != "cms.pages" || info["entity_id"].(float64) != float64(id) || info["locale"] != loc {
					t.Errorf("preview info: %v", info)
				}
				if _, ok := info["warnings"].([]any); !ok {
					t.Errorf("warnings must be an array, got %v", info["warnings"])
				}
			})
		}
	}
}

func TestResponseHeaders(t *testing.T) {
	st := newStack(t)
	tok := st.issue("h", 1, "", snapshotOf(t, "our-path"))
	check := func(name string, r *apitest.Response) {
		t.Helper()
		h := r.Header
		if h.Get("Cache-Control") != "no-store" {
			t.Errorf("%s: Cache-Control %q", name, h.Get("Cache-Control"))
		}
		if rt := h.Get("X-Robots-Tag"); !strings.Contains(rt, "noindex") || !strings.Contains(rt, "nofollow") {
			t.Errorf("%s: X-Robots-Tag %q", name, rt)
		}
		if !strings.Contains(h.Get("Vary"), "X-Preview-Token") {
			t.Errorf("%s: Vary %q", name, h.Get("Vary"))
		}
		if h.Get("ETag") != "" {
			t.Errorf("%s: a preview carries no ETag", name)
		}
	}
	check("200", st.resolve(t, tok, ""))
	check("401 unknown", st.resolve(t, makeToken("nope"), ""))
	check("422 missing", st.resolve(t, "", ""))
	// negotiating by Accept-Language adds it to Vary
	r := st.resolve(t, tok, "", "Accept-Language: en")
	if !strings.Contains(r.Header.Get("Vary"), "Accept-Language") || r.Header.Get("Content-Language") != "en" {
		t.Errorf("Accept-Language: Vary %q, Content-Language %q", r.Header.Get("Vary"), r.Header.Get("Content-Language"))
	}
}

var requestIDRe = regexp.MustCompile(`"request_id":"[^"]*"`)

// Unknown, expired, revoked, bound to another page, or an entity this portal cannot preview: one answer.
func TestUnusableTokensAreIndistinguishable(t *testing.T) {
	st := newStack(t)
	snap := snapshotOf(t, "our-path")
	good := st.issue("good", 2, "", snap)
	expired := st.issue("expired", 2, "", snap)
	st.Tokens.entries[hashHex(expired)].expires = time.Now().Add(-time.Second)
	revoked := st.issue("revoked", 2, "", snap)
	st.Tokens.entries[hashHex(revoked)].revoked = true
	post := makeToken("post")
	st.Tokens.add(post, previewrepos.Consumed{EntityType: "cms.albums", EntityID: 1, Snapshot: []byte(`{"album":{}}`)}, time.Hour)

	if r := st.resolve(t, good, ""); !okStatus(r) {
		t.Fatalf("control: %d %s", r.Status, r.Body)
	}
	var first []byte
	for name, r := range map[string]*apitest.Response{
		"unknown":      st.resolve(t, makeToken("unknown"), ""),
		"expired":      st.resolve(t, expired, ""),
		"revoked":      st.resolve(t, revoked, ""),
		"wrong path":   st.resolve(t, good, "?path=/blog"),
		"wrong entity": st.resolve(t, post, ""),
	} {
		if r.Status != http.StatusUnauthorized || r.Code() != "INVALID_TOKEN" {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
			continue
		}
		body := requestIDRe.ReplaceAll(r.Body, nil)
		if first == nil {
			first = body
		} else if !bytes.Equal(first, body) {
			t.Errorf("%s answers differently:\n%s\nvs\n%s", name, body, first)
		}
		if r.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("%s: Cache-Control %q", name, r.Header.Get("Cache-Control"))
		}
	}
	// the right path is accepted
	if r := st.resolve(t, good, "?path=/our-path"); !okStatus(r) {
		t.Errorf("matching ?path: %d %s", r.Status, r.Body)
	}
}

func hashHex(token string) string {
	return fmt.Sprintf("%x", sha256sum(token))
}

// The token is read from the header only: a query token neither authenticates nor interferes.
func TestTokenOnlyFromTheHeader(t *testing.T) {
	st := newStack(t)
	tok := st.issue("q", 2, "", snapshotOf(t, "our-path"))
	r := st.App.Get(t, "/api/v1/preview/resolve?token="+tok+"&X-Preview-Token="+tok)
	if r.Status != http.StatusUnprocessableEntity {
		t.Errorf("a query token must not count: %d %s", r.Status, r.Body)
	}
	if st.Tokens.calls != 0 {
		t.Errorf("the token store was consulted %d times", st.Tokens.calls)
	}
	if r := st.resolve(t, tok, "?token=garbage"); !okStatus(r) {
		t.Errorf("a stray ?token must be ignored: %d", r.Status)
	}
}

func TestTokenShapeIs422(t *testing.T) {
	st := newStack(t)
	for name, tok := range map[string]string{"short": "abc", "long": strings.Repeat("a", 257), "chars": strings.Repeat("a", 40) + " ;<>"} {
		r := st.resolve(t, tok, "")
		if r.Status != http.StatusUnprocessableEntity || r.Code() != "VALIDATION_FAILED" {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
		if strings.Contains(string(r.Body), tok) {
			t.Errorf("%s: the answer echoes the token", name)
		}
	}
	if r := st.resolve(t, "", ""); r.Status != http.StatusUnprocessableEntity {
		t.Errorf("missing: %d", r.Status)
	}
	if r := st.resolve(t, makeToken("x"), "?locale=%24%24"); r.Status != http.StatusUnprocessableEntity {
		t.Errorf("bad locale: %d", r.Status)
	}
	if r := st.resolve(t, makeToken("x"), "?path="+strings.Repeat("a", 301)); r.Status != http.StatusUnprocessableEntity {
		t.Errorf("long path: %d", r.Status)
	}
	if r := st.App.Get(t, "/api/v1/preview/resolve", tokenHeader+": "+makeToken("x"), "X-Portal-Key: -"); r.Status != http.StatusUnauthorized || r.Code() != "UNAUTHENTICATED" {
		t.Errorf("no portal key: %d %s", r.Status, r.Body)
	}
}

// A token can be used any number of times until it expires (max_uses is NULL).
func TestMultiUse(t *testing.T) {
	st := newStack(t)
	snap := snapshotOf(t, "our-path")
	tok := st.issue("m", 2, "", snap)
	for i := 0; i < 25; i++ {
		if r := st.resolve(t, tok, ""); !okStatus(r) {
			t.Fatalf("call %d: %d", i+1, r.Status)
		}
	}
	if n := st.Tokens.usesOf(st.Tokens.entries[hashHex(tok)]); n != 25 {
		t.Errorf("uses %d, want 25 (no snapshot cache here)", n)
	}
}

// The locale: ?locale beats the locale the link was issued for, which beats Accept-Language.
func TestLocaleNegotiation(t *testing.T) {
	st := newStack(t)
	tok := st.issue("loc", 2, "en", snapshotOf(t, "our-path"))
	cases := []struct{ query, accept, want string }{
		{"", "", "en"},           // the link's locale
		{"?locale=la", "", "la"}, // explicit wins
		{"", "la", "en"},         // the link beats the header
		{"?locale=xx", "", "la"}, // unknown code -> default
		{"?locale=lo", "", "la"}, // lo is la
	}
	for _, c := range cases {
		var h []string
		if c.accept != "" {
			h = append(h, "Accept-Language: "+c.accept)
		}
		r := st.resolve(t, tok, c.query, h...)
		if r.Status != 200 || r.Data()["locale"] != c.want {
			t.Errorf("%q %q: %d locale %v, want %s", c.query, c.accept, r.Status, r.Data()["locale"], c.want)
		}
	}
	noLoc := st.issue("loc2", 2, "", snapshotOf(t, "our-path"))
	if r := st.resolve(t, noLoc, "", "Accept-Language: en"); r.Data()["locale"] != "en" {
		t.Errorf("Accept-Language without a link locale: %v", r.Data()["locale"])
	}
}

// ---- draft tolerance --------------------------------------------------------------------------------------------

func snapshotJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func sectionKeys(d map[string]any) []string {
	var out []string
	for _, s := range d["page"].(map[string]any)["sections"].([]any) {
		out = append(out, s.(map[string]any)["key"].(string))
	}
	return out
}

// A section flagged is_visible=false is not rendered; order follows sort_order; unknown types are left out with a
// warning.
func TestHiddenAndUnknownSections(t *testing.T) {
	st := newStack(t)
	snap := snapshotJSON(t, map[string]any{
		"page":         map[string]any{"key": "lab", "name": "Lab", "template": "standard", "path": "/lab"},
		"translations": []any{map[string]any{"locale_code": "la", "title": "Lab", "is_enabled": true}},
		"sections": []any{
			map[string]any{"section_type": "intro_heading", "section_version": 1, "sort_order": 3, "is_visible": true, "props": map[string]any{"title": map[string]any{"la": "third"}}},
			map[string]any{"section_type": "intro_heading", "section_version": 1, "sort_order": 1, "is_visible": false, "props": map[string]any{"title": map[string]any{"la": "HIDDEN"}}},
			map[string]any{"section_type": "intro_text", "section_version": 1, "sort_order": 2, "props": map[string]any{}}, // is_visible missing = visible
			map[string]any{"section_type": "legacy_box", "section_version": 1, "sort_order": 4, "is_visible": true, "props": map[string]any{"x": 1}},
		},
	})
	tok := st.issue("hid", 9, "", snap)
	r := st.resolve(t, tok, "")
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if got := fmt.Sprint(sectionKeys(r.Data())); got != "[intro_text intro_heading]" {
		t.Errorf("sections %s, want [intro_text intro_heading]", got)
	}
	if strings.Contains(string(r.Body), "HIDDEN") {
		t.Error("the hidden section's content was rendered")
	}
	w := r.Data()["preview"].(map[string]any)["warnings"].([]any)
	if len(w) != 1 || w[0].(map[string]any)["code"] != "section_type_unknown" || w[0].(map[string]any)["path"] != "/sections/3" {
		t.Errorf("warnings: %v", w)
	}
}

// Every registered section type renders from empty props, and a page with no translation, no sections or
// nothing but the path still answers 200.
func TestSparseSnapshotsRenderInsteadOfFailing(t *testing.T) {
	st := newStack(t)
	reg := st.Public.Registry()
	var secs []any
	n := 0
	for _, id := range reg.Keys() {
		key, ver, _ := strings.Cut(id, "@")
		var v int
		_, _ = fmt.Sscan(ver, &v)
		n++
		secs = append(secs, map[string]any{"section_type": key, "section_version": v, "sort_order": n, "is_visible": true})              // no props member at all
		secs = append(secs, map[string]any{"section_type": key, "section_version": v, "sort_order": 100 + n, "props": map[string]any{}}) // empty props
		secs = append(secs, map[string]any{"section_type": key, "sort_order": 200 + n, "props": nil})                                    // no version, null props
	}
	cases := map[string]any{
		"all types, no props": map[string]any{"page": map[string]any{"path": "/x"}, "sections": secs},
		"only a path":         map[string]any{"page": map[string]any{"path": "/x"}},
		"null members":        map[string]any{"page": map[string]any{"path": "/x"}, "translations": nil, "sections": nil},
		"translation without enabled flag or title": map[string]any{"page": map[string]any{"path": "/x", "name": "N"}, "translations": []any{map[string]any{"locale_code": "la"}}},
		"only disabled translations":                map[string]any{"page": map[string]any{"path": "/x", "name": "N"}, "translations": []any{map[string]any{"locale_code": "la", "title": "T", "is_enabled": false}}},
	}
	for name, snap := range cases {
		t.Run(name, func(t *testing.T) {
			tok := st.issue(name, 5, "", snapshotJSON(t, snap))
			r := st.resolve(t, tok, "")
			if r.Status != 200 {
				t.Fatalf("%d %s", r.Status, r.Body)
			}
			if r.Data()["page"].(map[string]any)["page"].(map[string]any)["path"] != "/x" {
				t.Errorf("page: %v", r.Data()["page"])
			}
		})
	}
	// an unusable snapshot is a server problem, not a bad token, and the answer carries none of it
	bad := st.issue("bad", 5, "", []byte(`{"page":{"path":123}}`))
	if r := st.resolve(t, bad, ""); r.Status != http.StatusInternalServerError || strings.Contains(string(r.Body), "123") {
		t.Errorf("malformed snapshot: %d %s", r.Status, r.Body)
	}
	nopath := st.issue("nopath", 5, "", []byte(`{"page":{"key":"k"}}`))
	if r := st.resolve(t, nopath, ""); r.Status != http.StatusInternalServerError {
		t.Errorf("snapshot without a path: %d", r.Status)
	}
}

// An album/FAQ/form that is not published keeps the section with an empty resolved and a warning (the public
// page drops the section); a link to an unpublished page and a manual post that is not published are reported.
func TestUnpublishedReferencesAreWarnings(t *testing.T) {
	st := newStack(t)
	snap := snapshotJSON(t, map[string]any{
		"page": map[string]any{"path": "/x", "name": "X"},
		"sections": []any{
			map[string]any{"section_type": "faq", "section_version": 1, "sort_order": 1, "props": map[string]any{"group_key": "does-not-exist"}},
			map[string]any{"section_type": "gallery_cards", "section_version": 1, "sort_order": 2, "props": map[string]any{"source": "album", "album_id": 9999}},
			map[string]any{"section_type": "blog_strip", "section_version": 1, "sort_order": 3, "props": map[string]any{"source": "manual", "items": []any{map[string]any{"post_id": 9999}}}},
			map[string]any{"section_type": "hero_brand", "section_version": 1, "sort_order": 4, "props": map[string]any{
				"title": map[string]any{"la": "T"}, "cta": map[string]any{"label": map[string]any{"la": "Go"}, "link": map[string]any{"type": "page", "page_id": 9999}}}},
		},
	})
	tok := st.issue("refs", 5, "", snap)
	r := st.resolve(t, tok, "")
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if got := fmt.Sprint(sectionKeys(r.Data())); got != "[faq gallery_cards blog_strip hero_brand]" {
		t.Errorf("the sections must be kept: %s", got)
	}
	var codes []string
	for _, w := range r.Data()["preview"].(map[string]any)["warnings"].([]any) {
		m := w.(map[string]any)
		codes = append(codes, fmt.Sprintf("%s@%s", m["code"], m["path"]))
	}
	want := []string{"link_page_not_published@/sections/3", "reference_not_published@/sections/0", "reference_not_published@/sections/1", "post_not_published@/sections/2/props/items/0/post_id"}
	for _, w := range want {
		if !contains(codes, w) {
			t.Errorf("missing warning %s in %v", w, codes)
		}
	}
	// the public path leaves such a section out
	for _, s := range r.Data()["page"].(map[string]any)["sections"].([]any) {
		if m := s.(map[string]any); m["key"] == "faq" && len(m["resolved"].(map[string]any)) != 0 {
			t.Errorf("faq resolved: %v", m["resolved"])
		}
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// A section that uses a shared section shows the PUBLISHED content of the shared section; one that is not
// published is left out with a warning.
func TestSharedSections(t *testing.T) {
	id := int64(7)
	pub := entities.JSONB(`{"title":{"la":"From the shared section"}}`)
	var variant = "wide"
	st := newStack(t, func(o *opts) {
		o.shared = &fakeShared{rows: []entities.PublishedPageSection{
			{PageID: 1, SectionID: 1, SectionKey: "intro_heading", SectionVersion: 1, Variant: &variant, Props: pub, SharedSectionID: &id},
		}}
	})
	snap := snapshotJSON(t, map[string]any{
		"page": map[string]any{"path": "/x"},
		"sections": []any{
			map[string]any{"section_type": "intro_heading", "section_version": 1, "sort_order": 1, "shared_section_id": 7, "props": map[string]any{}},
			map[string]any{"section_type": "intro_heading", "section_version": 1, "sort_order": 2, "shared_section_id": 8, "props": map[string]any{}},
		},
	})
	tok := st.issue("sh", 5, "", snap)
	r := st.resolve(t, tok, "")
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	secs := r.Data()["page"].(map[string]any)["sections"].([]any)
	if len(secs) != 1 || !strings.Contains(string(r.Body), "From the shared section") || secs[0].(map[string]any)["variant"] != "wide" {
		t.Errorf("sections: %v", secs)
	}
	w := r.Data()["preview"].(map[string]any)["warnings"].([]any)
	if len(w) != 1 || w[0].(map[string]any)["code"] != "shared_section_unavailable" || w[0].(map[string]any)["path"] != "/sections/1" {
		t.Errorf("warnings: %v", w)
	}
}

// ---- rate limit -------------------------------------------------------------------------------------------------

func TestRateLimit(t *testing.T) {
	st := newStack(t, func(o *opts) {
		o.svc = func(d *previewservices.Deps) { d.Options = previewservices.Options{TokenPerMinute: 3, KeyPerMinute: 5} }
	})
	snap := snapshotOf(t, "our-path")
	a, b := st.issue("a", 2, "", snap), st.issue("b", 2, "", snap)
	for i := 1; i <= 3; i++ {
		if r := st.resolve(t, a, ""); !okStatus(r) {
			t.Fatalf("call %d: %d", i, r.Status)
		}
	}
	r := st.resolve(t, a, "")
	if r.Status != http.StatusTooManyRequests || r.Code() != "RATE_LIMITED" || r.Header.Get("Retry-After") == "" || r.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("4th call of one token: %d %s retry-after %q", r.Status, r.Body, r.Header.Get("Retry-After"))
	}
	if st.Tokens.calls != 3 {
		t.Errorf("a limited call must not reach the token store: %d calls", st.Tokens.calls)
	}
	// another token is unaffected by the first one's counter, but the portal key's total (5) applies to all
	if r := st.resolve(t, b, ""); !okStatus(r) { // key count 5
		t.Errorf("other token: %d", r.Status)
	}
	if r := st.resolve(t, b, ""); r.Status != http.StatusTooManyRequests {
		t.Errorf("past the per-key ceiling: %d", r.Status)
	}
	// keys carry hashes only
	for k := range st.Limiter.counts {
		if strings.Contains(k, a) || strings.Contains(k, b) {
			t.Errorf("the token is in a Redis key: %s", k)
		}
		if !strings.HasPrefix(k, "test-preview:rl:preview") {
			t.Errorf("unexpected key family %s", k)
		}
	}
}

// A limiter that fails does not block a preview (a read that already needs a secret token).
func TestLimiterFailureFailsOpen(t *testing.T) {
	st := newStack(t)
	st.Limiter.fail = true
	tok := st.issue("fo", 2, "", snapshotOf(t, "our-path"))
	if r := st.resolve(t, tok, ""); !okStatus(r) {
		t.Errorf("fail-open: %d %s", r.Status, r.Body)
	}
}

// ---- logs -------------------------------------------------------------------------------------------------------

// Neither the token, nor its hash, nor snapshot content reaches the log, whatever the outcome.
func TestTokenNeverReachesTheLog(t *testing.T) {
	var buf bytes.Buffer
	restore := logs.Capture(&buf)
	defer restore()

	st := newStack(t, func(o *opts) {
		o.svc = func(d *previewservices.Deps) { d.Options = previewservices.Options{TokenPerMinute: 1} }
	})
	good := st.issue("log-good", 2, "", snapshotOf(t, "our-path"))
	broken := st.issue("log-broken", 2, "", []byte(`{"page":{"path":"SECRET-PATH-IN-A-BROKEN-SNAPSHOT","name":5}}`))
	post := makeToken("log-post")
	st.Tokens.add(post, previewrepos.Consumed{EntityType: "cms.albums", EntityID: 1, Snapshot: []byte(`{}`)}, time.Hour)
	unknown := makeToken("log-unknown")

	st.resolve(t, good, "?token="+good+"&path=/our-path")
	st.resolve(t, good, "")   // 429
	st.resolve(t, broken, "") // 500, logged with its cause
	st.resolve(t, post, "")   // unsupported entity, warned
	st.resolve(t, unknown, "")
	st.resolve(t, "short", "")
	logs.Sync()

	out := buf.String()
	if !strings.Contains(out, `"request"`) {
		t.Fatalf("the access log line was not captured: %q", out)
	}
	for name, secret := range map[string]string{"good": good, "broken": broken, "post": post, "unknown": unknown,
		"hash": hashHex(good), "hash32": hashHex(good)[:32], "snapshot": "SECRET-PATH-IN-A-BROKEN-SNAPSHOT"} {
		if strings.Contains(out, secret) {
			t.Errorf("%s leaked into the log", name)
		}
	}
	if strings.Contains(out, "X-Preview-Token") || strings.Contains(out, "?token") {
		t.Error("a header name or a query string reached the log")
	}
}
