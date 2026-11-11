package assemble

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/entities"
	"github.com/materm-organization/materm-web-portal-backend/pkg/media"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/seedfake"
)

// harness assembles ad-hoc sections over the seed (post 1, album 1, FAQ group "contact", form "contact", the
// five pages with ids 1..5) so that every resolver and every props rule has a focused test.
type harness struct {
	t  *testing.T
	db *seedfake.DB
	a  *Assembler
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	db := seedfake.Load(t)
	return &harness{t: t, db: db, a: assemblerOver(db.Set())}
}

func sec(key, props string) entities.PublishedPageSection {
	return entities.PublishedPageSection{SectionKey: key, SectionVersion: 1, PublicID: "sec-" + key, Props: entities.JSONB(props)}
}

func (h *harness) run(locale string, secs ...entities.PublishedPageSection) *PageResult {
	h.t.Helper()
	pg := &entities.PublishedPage{PageID: 9, PublicID: "page-9", Key: "t", Template: "standard", Path: "/t", Title: "T",
		UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), ResolvedLocale: locale}
	res, err := h.a.Page(context.Background(), PageInput{Locale: locale, Chain: h.db.Chain(locale), Page: pg, Sections: secs})
	if err != nil {
		h.t.Fatal(err)
	}
	return res
}

// obj decodes a raw JSON field of a section.
func obj(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return m
}

func TestUnknownSectionsAreSkippedAndCounted(t *testing.T) {
	h := newHarness(t)
	bad := sec("hero_brand", `[1,2]`) // not an object
	future := sec("hero_brand", `{}`)
	future.SectionVersion = 2
	res := h.run("en", sec("mystery", `{}`), future, bad, sec("intro_heading", `{"title":{"en":"Hello"}}`))
	if len(res.Data.Sections) != 1 || res.Data.Sections[0].Key != "intro_heading" {
		t.Fatalf("sections: %+v", res.Data.Sections)
	}
	if got := h.a.Skipped(); got != 3 {
		t.Errorf("skipped = %d, want 3", got)
	}
	if res.Data.Sections[0].Variant != nil || res.Data.Sections[0].AnchorID != nil {
		t.Error("variant and anchor_id must be null")
	}
	if string(res.Data.Sections[0].Resolved) != "{}" {
		t.Errorf("resolved = %s", res.Data.Sections[0].Resolved)
	}
}

func TestLeafFallbackAndNulls(t *testing.T) {
	h := newHarness(t)
	// en falls back to la; "" counts as missing; no value anywhere is null
	res := h.run("en", sec("intro_heading", `{"eyebrow":{"en":"","la":"Eyebrow la"},"title":{"en":"Title en","la":"Title la"},"description":{}}`))
	s := res.Data.Sections[0]
	p := obj(t, s.Props)
	if p["eyebrow"] != "Eyebrow la" || p["title"] != "Title en" || p["description"] != nil {
		t.Errorf("props: %v", p)
	}
	if !s.IsFallback {
		t.Error("is_fallback must be true when a leaf used another locale")
	}
	res = h.run("la", sec("intro_heading", `{"eyebrow":{"la":"x"},"title":{"la":"y"}}`))
	if res.Data.Sections[0].IsFallback {
		t.Error("is_fallback must be false in the requested locale")
	}
}

func TestImageAndLinkShapes(t *testing.T) {
	h := newHarness(t)
	props := `{
	  "title":{"en":"T"},
	  "cta":{"label":{"en":"Go"},"link":{"type":"page","page_id":2,"anchor":"top"}},
	  "background":{"media_id":1,"decorative":true}}`
	s := h.run("en", sec("hero_brand", props)).Data.Sections[0]
	p := obj(t, s.Props)
	cta := p["cta"].(map[string]any)
	if cta["label"] != "Go" || cta["href"] != "/our-path#top" || cta["open_in_new_tab"] != false {
		t.Errorf("cta: %v", cta)
	}
	if rel, _ := cta["rel"].([]any); rel == nil || len(rel) != 0 {
		t.Errorf("rel must be []: %v", cta["rel"])
	}
	if _, has := cta["link"]; has {
		t.Error("the link must be merged into the cta")
	}
	bg := p["background"].(map[string]any)
	if bg["alt"] != "" || bg["decorative"] != true || bg["media_id"].(float64) != 1 {
		t.Errorf("decorative image: %v", bg)
	}

	// a link to a page that is not published has href null; an external link keeps its flags
	props = `{"title":{"en":"T"},"cta":{"label":{"en":"Go"},"link":{"type":"page","page_id":999}},
	  "background":{"media_id":2,"alt":{"en":"A photo"}}}`
	p = obj(t, h.run("en", sec("hero_brand", props)).Data.Sections[0].Props)
	if cta := p["cta"].(map[string]any); cta["href"] != nil {
		t.Errorf("unpublished target: %v", cta)
	}
	if bg := p["background"].(map[string]any); bg["alt"] != "A photo" || bg["decorative"] != false {
		t.Errorf("image: %v", bg)
	}
	props = `{"title":{"en":"T"},"cta":{"label":{"en":"Go"},"link":{"type":"url","url":"https://example.com/x","open_in_new_tab":true,"rel":["nofollow"]}},
	  "background":{"media_id":2}}`
	p = obj(t, h.run("en", sec("hero_brand", props)).Data.Sections[0].Props)
	cta = p["cta"].(map[string]any)
	if cta["href"] != "https://example.com/x" || cta["open_in_new_tab"] != true || fmt.Sprint(cta["rel"]) != "[nofollow]" {
		t.Errorf("url link: %v", cta)
	}
	if bg := p["background"].(map[string]any); bg["alt"] != nil {
		t.Errorf("a missing alt of a normal image is null: %v", bg)
	}
}

func TestRichTextIsSanitisedAndLinksResolved(t *testing.T) {
	h := newHarness(t)
	doc := `{"type":"doc","content":[{"type":"paragraph","content":[
	  {"type":"text","text":"see "},
	  {"type":"text","text":"our path","marks":[{"type":"link","attrs":{"page_id":2}}]},
	  {"type":"text","text":" or "},
	  {"type":"text","text":"nowhere","marks":[{"type":"link","attrs":{"page_id":999}}]},
	  {"type":"text","text":"!","marks":[{"type":"highlight"}]}]},
	  {"type":"image","attrs":{"media_id":3}}]}`
	s := h.run("en", sec("intro_text", `{"text":{"en":`+doc+`}}`)).Data.Sections[0]
	b, _ := json.Marshal(obj(t, s.Props)["text"])
	out := string(b)
	if !strings.Contains(out, `"/our-path"`) {
		t.Errorf("page link not resolved: %s", out)
	}
	if strings.Contains(out, `"page_id"`) || strings.Contains(out, "highlight") || strings.Contains(out, `"image"`) {
		t.Errorf("not sanitised (lite profile): %s", out)
	}
	if !strings.Contains(out, "nowhere") {
		t.Errorf("the text of an unresolved link must stay: %s", out)
	}
}

func TestBlogStrip(t *testing.T) {
	h := newHarness(t)
	// manual: order of the editor, the same post twice, an unpublished post skipped, excerpt and image override
	props := `{"eyebrow":{"en":"E"},"title":{"en":"T"},"source":"manual","items":[
	  {"post_id":1,"excerpt":{"en":"Mine"},"image":{"media_id":5,"alt":{"en":"Alt 5"}}},
	  {"post_id":77},
	  {"post_id":1}]}`
	res := h.run("en", sec("blog_strip", props))
	r := obj(t, res.Data.Sections[0].Resolved)
	posts := r["posts"].([]any)
	if len(posts) != 2 {
		t.Fatalf("posts: %v", posts)
	}
	p0, p1 := posts[0].(map[string]any), posts[1].(map[string]any)
	if p0["excerpt"] != "Mine" || p0["image"].(map[string]any)["alt"] != "Alt 5" || p0["image"].(map[string]any)["media_id"].(float64) != 5 {
		t.Errorf("override: %v", p0)
	}
	if p1["excerpt"] == "Mine" || p1["image"].(map[string]any)["media_id"].(float64) != 16 {
		t.Errorf("second slice must use the post's own excerpt and card image: %v", p1)
	}
	if p0["href"] != "/blog/music-night-under-the-stars" || p0["date"] != "Sep. 05.2026" || p0["date_time"] != "2026-09-05T00:00:00Z" {
		t.Errorf("slice: %v", p0)
	}
	if !has(res.Tags, "posts:list") {
		t.Errorf("tags: %v", res.Tags)
	}
	if _, ok := res.Data.Media["5"]; !ok {
		t.Error("the override image must be in data.media")
	}

	// latest: newest first, limit, category
	res = h.run("la", sec("blog_strip", `{"eyebrow":{"la":"E"},"title":{"la":"T"},"source":"latest","limit":3}`))
	if posts := obj(t, res.Data.Sections[0].Resolved)["posts"].([]any); len(posts) != 1 {
		t.Errorf("latest: %v", posts)
	}
	res = h.run("la", sec("blog_strip", `{"eyebrow":{"la":"E"},"title":{"la":"T"},"source":"latest","category_id":2}`))
	if posts := obj(t, res.Data.Sections[0].Resolved)["posts"].([]any); len(posts) != 0 {
		t.Errorf("category filter: %v", posts)
	}
	if string(res.Data.Sections[0].Resolved) != `{"posts":[]}` {
		t.Errorf("resolved = %s", res.Data.Sections[0].Resolved)
	}
}

func has(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestGalleryCardsAndPhotoStoryFromAnAlbum(t *testing.T) {
	h := newHarness(t)
	res := h.run("en",
		sec("gallery_cards", `{"eyebrow":{"en":"E"},"title":{"en":"T"},"source":"album","album_id":1,"limit":2,"cards_link":{"type":"page","page_id":4}}`),
		sec("photo_story", `{"accessible_heading":{"en":"H"},"album_id":1,"rows":[{"layout":"wide-narrow","slots":[
		   {"album_item_id":1,"size":"wide"},{"album_item_id":2,"size":"narrow"},{"album_item_id":4242,"size":"tall"}]}]}`),
		sec("gallery_cards", `{"eyebrow":{"en":"E"},"title":{"en":"T"},"source":"album","album_id":999}`),
		sec("photo_story", `{"accessible_heading":{"en":"H"},"album_id":999,"rows":[]}`),
	)
	if len(res.Data.Sections) != 2 {
		t.Fatalf("sections with an unpublished album must be dropped: %d", len(res.Data.Sections))
	}
	cards := obj(t, res.Data.Sections[0].Resolved)["cards"].([]any)
	c0 := cards[0].(map[string]any)
	if len(cards) != 2 || c0["href"] != "/gallery" || c0["image"].(map[string]any)["decorative"] != false {
		t.Errorf("cards: %v", cards)
	}
	if c0["caption"].(map[string]any)["title"] != "Beneath the canopy" || c0["caption"].(map[string]any)["date"] != nil {
		t.Errorf("caption: %v", c0["caption"])
	}
	slots := obj(t, res.Data.Sections[1].Resolved)["slots"].(map[string]any)
	if len(slots) != 2 || slots["1"] == nil || slots["2"] == nil || slots["4242"] != nil {
		t.Errorf("slots: %v", slots)
	}
	for _, tag := range []string{"album:1", "albums:list"} {
		if !has(res.Tags, tag) {
			t.Errorf("missing tag %s in %v", tag, res.Tags)
		}
	}
}

func TestFAQAndContactSection(t *testing.T) {
	h := newHarness(t)
	res := h.run("en",
		sec("faq", `{"eyebrow":{"en":"E"},"title":{"en":"T"},"group_key":"contact","open_first":true}`),
		sec("faq", `{"eyebrow":{"en":"E"},"title":{"en":"T"},"group_key":"nope"}`),
		sec("contact_section", `{"eyebrow":{"en":"E"},"title":{"en":"T"},"form_slug":"missing","info_items":[
		   {"icon":"map_pin","label":{"en":"Visit"},"source":"contact.address"},
		   {"icon":"phone","label":{"en":"Call"},"source":"contact.phone"},
		   {"icon":"mail","label":{"en":"Mail"},"source":"contact.email"},
		   {"icon":"clock","label":{"en":"Hours"},"source":"contact.hours"},
		   {"icon":"mail","label":{"en":"Custom"},"source":"custom","value":{"en":"Ask at the gate"},"link":{"type":"url","url":"https://example.com"}},
		   {"icon":"mail","label":{"en":"Empty"},"source":"custom","value":{}}]}`),
	)
	if len(res.Data.Sections) != 2 {
		t.Fatalf("unknown FAQ group must drop the section: %d", len(res.Data.Sections))
	}
	items := obj(t, res.Data.Sections[0].Resolved)["items"].([]any)
	if len(items) != 4 || items[0].(map[string]any)["answer"].(map[string]any)["type"] != "doc" {
		t.Errorf("faq items: %v", items)
	}
	r := obj(t, res.Data.Sections[1].Resolved)
	if r["form"] != nil {
		t.Errorf("an unpublished/missing form is null: %v", r["form"])
	}
	info := r["info"].([]any)
	if len(info) != 5 {
		t.Fatalf("info: %v", info)
	}
	phone := info[1].(map[string]any)
	if phone["value"] != "+856 20 0000 0000" || phone["href"] != "tel:+8562000000000" {
		t.Errorf("phone: %v", phone)
	}
	if info[2].(map[string]any)["href"] != "mailto:hello@materm.example" || info[0].(map[string]any)["href"] != nil {
		t.Errorf("email/address: %v %v", info[2], info[0])
	}
	if c := info[4].(map[string]any); c["value"] != "Ask at the gate" || c["href"] != "https://example.com" {
		t.Errorf("custom: %v", c)
	}
	for _, tag := range []string{"faq:1", "settings"} {
		if !has(res.Tags, tag) {
			t.Errorf("missing tag %s in %v", tag, res.Tags)
		}
	}

	res = h.run("en", sec("contact_section", `{"eyebrow":{"en":"E"},"title":{"en":"T"},"form_slug":"contact","info_items":[]}`))
	form := obj(t, res.Data.Sections[0].Resolved)["form"].(map[string]any)
	if form["slug"] != "contact" || len(form["fields"].([]any)) != 5 || !has(res.Tags, "form:1") {
		t.Errorf("form: %v tags %v", form, res.Tags)
	}
}

func TestPostListCategoriesAndPageHead(t *testing.T) {
	h := newHarness(t)
	res := h.run("en", sec("post_list", `{"page_size":12}`))
	cats := obj(t, res.Data.Sections[0].Resolved)["categories"].([]any)
	if len(cats) != 5 || cats[0].(map[string]any)["key"] != "music" {
		t.Errorf("categories: %v", cats)
	}
	// SEO: canonical from the setting, description from the default, og image from the first hero
	res = h.run("en", sec("hero_article", `{"title":{"en":"T"},"background":{"media_id":7,"decorative":true}}`))
	seo := res.Data.Page.SEO
	if seo.Canonical != "https://materm.example/t" || seo.Description == nil || *seo.Description == "" || seo.OGImage == nil || seo.OGImage.MediaID != 7 {
		t.Errorf("seo: %+v", seo)
	}
	if res.Data.Page.UpdatedAt != "2026-01-01T00:00:00Z" {
		t.Errorf("updated_at: %s", res.Data.Page.UpdatedAt)
	}
	for _, tag := range []string{"page:9", "pages", "settings", "locales"} {
		if !has(res.Tags, tag) {
			t.Errorf("missing tag %s in %v", tag, res.Tags)
		}
	}
}

func TestEveryResolverHasASchemaAndEverySchemaAResolver(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"about_blocks@1", "blog_strip@1", "contact_section@1", "faq@1", "gallery_cards@1", "hero_article@1",
		"hero_brand@1", "intro_heading@1", "intro_text@1", "photo_story@1", "post_list@1", "teaser_collage@1"}
	if got := reg.Keys(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("registry: %v", got)
	}
	// spot checks of the compiled hints
	cases := map[string][]string{
		"hero_brand":    {"background.alt", "cta.label", "description", "title"},
		"post_list":     nil,
		"photo_story":   {"accessible_heading"},
		"about_blocks":  {"items[].body", "items[].date_label", "items[].image.alt", "items[].title"},
		"gallery_cards": {"cards[].caption.date", "cards[].caption.title", "cards[].image.alt", "cta.label", "description", "eyebrow", "row_label", "title"},
	}
	for key, paths := range cases {
		got, ok := reg.HintPaths(key, 1)
		if !ok || (fmt.Sprint(got) != fmt.Sprint(paths) && (len(got) != 0 || len(paths) != 0)) {
			t.Errorf("%s hints: %v, want %v", key, got, paths)
		}
	}
}

func TestLinkTargetsBatchPagesAndPosts(t *testing.T) {
	h := newHarness(t)
	set := h.db.Set()
	doc := map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{
		map[string]any{"type": "text", "text": "a", "marks": []any{map[string]any{"type": "link", "attrs": map[string]any{"page_id": json.Number("3")}}}},
		map[string]any{"type": "text", "text": "b", "marks": []any{map[string]any{"type": "link", "attrs": map[string]any{"post_id": json.Number("1")}}}},
		map[string]any{"type": "text", "text": "c", "marks": []any{map[string]any{"type": "link", "attrs": map[string]any{"post_id": json.Number("99")}}}},
	}}}}
	lt := newLinkTargets(set.Pages, set.Posts, "en")
	pageIDs, postIDs := collectDocLinks([]any{doc})
	if err := lt.ensure(context.Background(), pageIDs, postIDs); err != nil {
		t.Fatal(err)
	}
	r := lt.resolver()
	if p, ok := r.PageHref(3); !ok || p != "/blog" {
		t.Errorf("page 3: %q %v", p, ok)
	}
	if p, ok := r.PostHref(1); !ok || p != "/blog/music-night-under-the-stars" {
		t.Errorf("post 1: %q %v", p, ok)
	}
	if _, ok := r.PostHref(99); ok {
		t.Error("an unpublished post has no href")
	}
	if h.a.Registry() == nil {
		t.Error("registry")
	}
}

// The media kind rule of the assembler: an asset that sits only in slots of the wrong kind is dropped from the map (with the warning), one that
// is also used correctly stays, and the schema marks the video node.
func TestDropWrongKinds(t *testing.T) {
	a := &Assembler{}
	mk := func() media.Map {
		return media.Map{"1": {Kind: "video"}, "2": {Kind: "image"}, "3": {Kind: "video"}, "4": {Kind: "image"}}
	}
	w := &Warnings{}
	m := mk()
	slots := []slotUse{
		{id: 1, video: false, section: 0}, // a video in an image slot: dropped
		{id: 2, video: true, section: 0},  // a picture in the video slot: dropped
		{id: 3, video: true, section: 1},  // fine
		{id: 4, video: false, section: 1}, // fine
	}
	dropped := a.dropWrongKinds(PageInput{Warnings: w}, m, slots, map[int64]struct{}{})
	if _, ok := m["1"]; ok || len(dropped) != 2 || m["3"].Kind != "video" || m["4"].Kind != "image" {
		t.Errorf("map %v dropped %v", m, dropped)
	}
	if _, ok := m["2"]; ok {
		t.Errorf("a picture in the video slot stays: %v", m)
	}
	if got := w.List(); len(got) != 2 || got[0].Code != WarnMediaKindMismatch || got[0].Path != "/sections/0" {
		t.Errorf("warnings %v", got)
	}
	// used correctly somewhere else too: stays
	m = mk()
	a.dropWrongKinds(PageInput{}, m, []slotUse{{id: 1, video: false}, {id: 1, video: true}}, map[int64]struct{}{})
	if _, ok := m["1"]; !ok {
		t.Error("an asset that is also used in a video slot stays")
	}
	m = mk()
	a.dropWrongKinds(PageInput{}, m, []slotUse{{id: 1, video: false}}, map[int64]struct{}{1: {}})
	if _, ok := m["1"]; !ok {
		t.Error("an asset that resolved data uses stays")
	}
}

func TestSchemaMarksTheVideoNode(t *testing.T) {
	var schema map[string]any
	raw, err := os.ReadFile("schemas/hero_brand.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		JSONSchema map[string]any `json:"json_schema"`
	}
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	schema = doc.JSONSchema
	n, err := compileSchema(schema)
	if err != nil {
		t.Fatal(err)
	}
	bv := n.props["background_video"]
	if bv == nil || bv.kind != kObject || bv.props["media_id"] == nil || bv.props["media_id"].kind != kMedia || !bv.props["media_id"].video {
		t.Fatalf("background_video node: %+v", bv)
	}
	if n.props["background"].kind != kImage {
		t.Errorf("background stays an image placement")
	}
}
