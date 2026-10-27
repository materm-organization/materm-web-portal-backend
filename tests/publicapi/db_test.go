//go:build dbtest

package publicapi

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func get(t *testing.T, st *stack, target string, hdr ...string) map[string]any {
	t.Helper()
	r := st.App.Get(t, target, hdr...)
	if r.Status != http.StatusOK {
		t.Fatalf("GET %s: %d %s", target, r.Status, r.Body)
	}
	d := r.Data()
	if d == nil {
		// P-01 answers an array
		return map[string]any{"_": r.JSON()["data"]}
	}
	return d
}

func sectionByKey(t *testing.T, d map[string]any, key string) map[string]any {
	t.Helper()
	for _, s := range d["sections"].([]any) {
		if m := s.(map[string]any); m["key"] == key {
			return m
		}
	}
	t.Fatalf("no %s section in %v", key, d["sections"])
	return nil
}

func TestDBSiteEndpoints(t *testing.T) {
	st, s := dbStack(t)

	// P-01
	locales := get(t, st, "/api/v1/public/locales")["_"].([]any)
	if len(locales) != 2 || locales[0].(map[string]any)["code"] != "la" || locales[0].(map[string]any)["is_default"] != true || locales[1].(map[string]any)["fallback_code"] != "la" {
		t.Errorf("locales: %v", locales)
	}
	if df := locales[0].(map[string]any)["date_formats"].(map[string]any); df["short"] != "{month}. {day}.{year}" || len(df["months"].([]any)) != 12 {
		t.Errorf("date_formats: %v", df)
	}

	// P-02: grouped, the media setting resolved through v_public_assets and the static prefix
	d := get(t, st, "/api/v1/public/settings?locale=en&group=brand,contact")
	brand := d["settings"].(map[string]any)["brand"].(map[string]any)
	if brand["name"] != "MATERM" || brand["logo_alt"] != "MATERM logo" || brand["emblem_media_id"] != nil || brand["logo_media_id"].(float64) < 1 {
		t.Errorf("brand: %v", brand)
	}
	if _, ok := d["settings"].(map[string]any)["footer"]; ok {
		t.Error("?group must filter")
	}
	logoID := fmt.Sprint(int64(brand["logo_media_id"].(float64)))
	logo := d["media"].(map[string]any)[logoID].(map[string]any)
	if logo["url"] != "/images/site/home/hero-bg.png" || logo["mime"] != "image/png" || logo["alt"] != "Hero background" {
		t.Errorf("logo media: %v", logo)
	}
	// the view decides what is public (is_public); the reference seed marks analytics ids public with NULL values
	all := get(t, st, "/api/v1/public/settings?locale=la")["settings"].(map[string]any)
	if an, ok := all["analytics"].(map[string]any); !ok || an["ga4_id"] != nil || an["gtm_id"] != nil {
		t.Errorf("analytics: %v", all["analytics"])
	}
	ownerExec(t, s, `update cms_site_settings set is_public = false where key = 'gtm_id'`)
	defer ownerExec(t, s, `update cms_site_settings set is_public = true where key = 'gtm_id'`)
	hidden := get(t, st, "/api/v1/public/settings?locale=la&group=analytics")["settings"].(map[string]any)["analytics"].(map[string]any)
	if _, ok := hidden["gtm_id"]; ok {
		t.Error("a setting that is not public must never be served")
	}

	// P-03
	ui := get(t, st, "/api/v1/public/ui-strings?locale=en")["strings"].(map[string]any)
	if len(ui) != 39 {
		t.Errorf("ui strings: %d, want 39", len(ui))
	}
	blog := get(t, st, "/api/v1/public/ui-strings?locale=en&ns=blog")["strings"].(map[string]any)
	if len(blog) != 7 {
		t.Errorf("blog strings: %d", len(blog))
	}

	// P-04
	m := get(t, st, "/api/v1/public/menus/main?locale=en")
	items := m["items"].([]any)
	if m["key"] != "main" || m["locale"] != "en" || len(items) != 5 {
		t.Fatalf("menu: %v", m)
	}
	for _, it := range items {
		if it.(map[string]any)["label"] == "Hidden draft" {
			t.Error("a menu item that points at an unpublished page must be hidden")
		}
	}
	if first := items[0].(map[string]any); first["href"] != "/" || first["active_match"] != "exact" || first["label"] != "Home" {
		t.Errorf("first item: %v", first)
	}
	soc := get(t, st, "/api/v1/public/menus/social?locale=en")["items"].([]any)
	if s0 := soc[0].(map[string]any); len(soc) != 4 || s0["open_in_new_tab"] != true || fmt.Sprint(s0["rel"]) != "[noopener noreferrer]" || s0["icon"] != "instagram" {
		t.Errorf("social: %v", soc)
	}

	// P-05
	site := get(t, st, "/api/v1/public/site?locale=en")
	menus := site["menus"].(map[string]any)
	if len(menus["main"].(map[string]any)["items"].([]any)) != 5 || len(menus["footer"].(map[string]any)["items"].([]any)) != 4 || len(menus["social"].(map[string]any)["items"].([]any)) != 4 {
		t.Errorf("site menus: %v", menus)
	}
	if len(site["locales"].([]any)) != 2 || len(site["ui"].(map[string]any)) == 0 {
		t.Errorf("site: %d locales", len(site["locales"].([]any)))
	}
	for k := range site["ui"].(map[string]any) {
		if !strings.HasPrefix(k, "site.") {
			t.Errorf("P-05 carries only the site namespace, got %q", k)
		}
	}
	if _, ok := site["settings"].(map[string]any)["brand"]; !ok {
		t.Error("site settings")
	}
}

func TestDBPageResolve(t *testing.T) {
	st, s := dbStack(t)
	skippedBefore := st.Assembler.Skipped()
	d := get(t, st, "/api/v1/public/pages/resolve?path=/lab&locale=en")
	page := d["page"].(map[string]any)
	if page["key"] != "lab" || page["path"] != "/lab" || page["title"] != "Lab en" || page["resolved_locale"] != "en" || page["is_fallback"] != false || page["is_home"] != false {
		t.Errorf("page: %v", page)
	}
	seo := page["seo"].(map[string]any)
	if seo["title"] != "Lab SEO en" || seo["description"] != "Lab description en" || seo["noindex"] != false {
		t.Errorf("seo: %v", seo)
	}
	if seo["canonical"] != "https://materm.example/lab" {
		t.Errorf("the site.base_url setting wins over the configured fallback: %v", seo["canonical"])
	}
	if seo["og_image"].(map[string]any)["media_id"].(float64) != 1 {
		t.Errorf("og image comes from the first hero: %v", seo["og_image"])
	}

	// order, unknown keys/versions skipped and counted, variants and anchors
	var keys []string
	for _, x := range d["sections"].([]any) {
		keys = append(keys, x.(map[string]any)["key"].(string))
	}
	if strings.Join(keys, ",") != "hero_article,blog_strip,gallery_cards,faq,contact_section,post_list" {
		t.Errorf("sections: %v", keys)
	}
	if got := st.Assembler.Skipped() - skippedBefore; got != 2 {
		t.Errorf("skipped sections counted: %d, want 2 (legacy_box@1, hero_brand@2)", got)
	}
	strip := sectionByKey(t, d, "blog_strip")
	if strip["anchor_id"] != "strip" || sectionByKey(t, d, "gallery_cards")["variant"] != "standard" || sectionByKey(t, d, "hero_article")["variant"] != nil {
		t.Error("variant / anchor_id")
	}
	// the title has an "en" value, the eyebrow only "la": the section used a fallback
	if strip["is_fallback"] != true || sectionByKey(t, d, "hero_article")["is_fallback"] != true {
		t.Error("is_fallback of a section with a leaf from another locale")
	}
	hero := sectionByKey(t, d, "hero_article")["props"].(map[string]any)
	meta := hero["meta_items"].([]any)
	if hero["title"] != "Lab en" || meta[0].(map[string]any)["text"] != "One" || meta[1].(map[string]any)["text"] != "Two" {
		t.Errorf("hero props: %v", hero)
	}

	// resolved data from the views
	posts := strip["resolved"].(map[string]any)["posts"].([]any)
	if len(posts) != 2 {
		t.Fatalf("blog strip posts: %v", posts)
	}
	p0, p1 := posts[0].(map[string]any), posts[1].(map[string]any)
	if p0["href"] != "/blog/camp-weekend" || p1["href"] != "/blog/music-night" || p0["title"] != "Camp weekend" {
		t.Errorf("posts (newest first, slug of the viewer's locale or its fallback): %v", posts)
	}
	for _, p := range posts {
		if p.(map[string]any)["title"] == "Future" || p.(map[string]any)["title"] == "Expired" || p.(map[string]any)["title"] == "Draft" {
			t.Errorf("hidden post served: %v", p)
		}
	}
	cards := sectionByKey(t, d, "gallery_cards")["resolved"].(map[string]any)["cards"].([]any)
	if len(cards) != 2 || cards[0].(map[string]any)["href"] != "/" {
		t.Errorf("cards: %v", cards)
	}
	if c := cards[0].(map[string]any)["caption"]; c == nil {
		t.Errorf("the first album item has a caption: %v", cards[0])
	}
	if faq := sectionByKey(t, d, "faq")["resolved"].(map[string]any)["items"].([]any); len(faq) != 4 {
		t.Errorf("faq items: %d", len(faq))
	}
	contact := sectionByKey(t, d, "contact_section")["resolved"].(map[string]any)
	form := contact["form"].(map[string]any)
	if form["slug"] != "contact" || len(form["fields"].([]any)) != 5 || form["honeypot_field"] != "company" {
		t.Errorf("form: %v", form)
	}
	if info := contact["info"].([]any); len(info) != 1 || info[0].(map[string]any)["href"] != "mailto:hello@materm.example" {
		t.Errorf("info: %v", info)
	}
	if cats := sectionByKey(t, d, "post_list")["resolved"].(map[string]any)["categories"].([]any); len(cats) != 5 {
		t.Errorf("categories: %v", cats)
	}

	// media: every media_id of props/resolved/seo is in the map with its URL built from the key
	media := d["media"].(map[string]any)
	if _, ok := media["1"]; !ok {
		t.Fatalf("media map: %v", media)
	}
	for _, id := range []int64{ownerInt64(t, s, `select id from media_assets where storage_key = 'uploads/2026/photo-b.webp'`)} {
		if it, ok := media[fmt.Sprint(id)].(map[string]any); ok && it["url"] != "https://files.example.test/uploads/2026/photo-b.webp" {
			t.Errorf("file service url: %v", it["url"])
		}
	}

	// the default locale of the same page: no fallback, the "la" strings
	la := get(t, st, "/api/v1/public/pages/resolve?path=/lab&locale=la")
	if la["page"].(map[string]any)["title"] != "Lab la" || sectionByKey(t, la, "hero_article")["is_fallback"] != false {
		t.Errorf("la page: %v", la["page"])
	}
}

func TestDBPageFallbackLocaleAndVisibility(t *testing.T) {
	st, _ := dbStack(t)

	// only translated in la: en is answered from la and says so
	d := get(t, st, "/api/v1/public/pages/resolve?path=/only-la&locale=en")
	page := d["page"].(map[string]any)
	if d["locale"] != "en" || page["resolved_locale"] != "la" || page["is_fallback"] != true || page["title"] != "Only la" {
		t.Errorf("fallback page: %v / %v", d["locale"], page)
	}
	// a page that is not in the sitemap is still served
	if r := st.App.Get(t, "/api/v1/public/pages/resolve?path=/nomap"); r.Status != 200 {
		t.Errorf("nomap: %d", r.Status)
	}

	// scheduled for the future, expired, draft, deleted-looking and unknown paths: all 404
	for _, p := range []string{"/scheduled", "/expired", "/draft", "/nope", "/lab/deeper"} {
		r := st.App.Get(t, "/api/v1/public/pages/resolve?path="+p)
		if r.Status != 404 || r.Code() != "NOT_FOUND" || r.Header.Get("Cache-Control") != "public, max-age=30" {
			t.Errorf("%s: %d %s %q", p, r.Status, r.Code(), r.Header.Get("Cache-Control"))
		}
	}

	// the home page of the base fixture: one hero section, the invisible FAQ section is not served
	home := get(t, st, "/api/v1/public/pages/resolve?path=/&locale=la")
	secs := home["sections"].([]any)
	if len(secs) != 1 || secs[0].(map[string]any)["key"] != "hero_brand" || home["page"].(map[string]any)["is_home"] != true {
		t.Errorf("home: %v", secs)
	}
	hp := secs[0].(map[string]any)["props"].(map[string]any)
	if hp["title"] != "Hello la" || hp["background"].(map[string]any)["alt"] != "" || hp["background"].(map[string]any)["decorative"] != true {
		t.Errorf("home hero props: %v", hp)
	}
}

func TestDBSitemap(t *testing.T) {
	st, _ := dbStack(t)
	d := get(t, st, "/api/v1/public/sitemap")
	if d["locale"] != "la" || d["base_url"] != "https://materm.example" {
		t.Errorf("sitemap head: %v %v", d["locale"], d["base_url"])
	}
	var paths []string
	for _, e := range d["entries"].([]any) {
		m := e.(map[string]any)
		paths = append(paths, m["kind"].(string)+" "+m["path"].(string))
		if _, err := time.Parse(time.RFC3339, m["lastmod"].(string)); err != nil {
			t.Errorf("lastmod %v", m["lastmod"])
		}
	}
	want := map[string]bool{
		"page /": true, "page /our-path": true, "page /blog": true, "page /gallery": true, "page /contact": true, "page /lab": true, "page /only-la": true,
		"post /blog/music-night-la": true, "post /blog/camp-weekend": true,
	}
	if len(paths) != len(want) {
		t.Errorf("entries (%d): %v", len(paths), paths)
	}
	for _, p := range paths {
		if !want[p] {
			t.Errorf("unexpected entry %q (noindex / not in sitemap / scheduled / expired / draft / future posts are excluded)", p)
		}
	}
	// pages before posts, each by id
	seenPost := false
	for _, e := range d["entries"].([]any) {
		kind := e.(map[string]any)["kind"]
		if kind == "post" {
			seenPost = true
		} else if seenPost {
			t.Errorf("a page after a post: %v", paths)
		}
	}
	// the sitemap ignores the locale of the request
	if a, b := st.App.Get(t, "/api/v1/public/sitemap?locale=en").Body, st.App.Get(t, "/api/v1/public/sitemap").Body; string(a) != string(b) {
		t.Error("the sitemap is always in the default locale")
	}
}
