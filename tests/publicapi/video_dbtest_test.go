//go:build dbtest

package publicapi

import (
	"fmt"
	"net/http"
	"testing"
)

// hero_brand.background_video (release 2 of video support): the public page carries the video item (kind, mime, size, duration) next to the
// poster item; the og image stays the poster; an unpublished page, a trashed or not ready video leaks nothing; a video in an image slot (or an
// image in the video slot) never fails the page: the asset is left out of data.media.
func TestDBHeroBackgroundVideo(t *testing.T) {
	st, s := dbStack(t)
	vid := ownerInt64(t, s, `insert into media_assets (kind, status, storage_key, original_filename, mime_type, bytes, width, height, duration_ms)
		values ('video', 'ready', 'uploads/2026/hero loop.mp4', 'hero loop.mp4', 'video/mp4', 4200000, 1920, 1080, 12500) returning id`)
	trashed := ownerInt64(t, s, `insert into media_assets (kind, status, storage_key, original_filename, mime_type, bytes, width, height, duration_ms, deleted_at)
		values ('video', 'ready', 'uploads/2026/gone.mp4', 'gone.mp4', 'video/mp4', 1, 640, 360, 5000, now()) returning id`)
	notReady := ownerInt64(t, s, `insert into media_assets (kind, status, storage_key, original_filename, mime_type, bytes)
		values ('video', 'uploading', 'uploads/2026/pending.mp4', 'pending.mp4', 'video/mp4', 1) returning id`)
	poster := ownerInt64(t, s, `select id from media_assets where storage_key = 'static/images/site/home/hero-bg.png'`)
	photo := ownerInt64(t, s, `select id from media_assets where storage_key = 'uploads/2026/photo-b.webp'`)

	type pg struct {
		key, path, status string
		props             string
	}
	pages := []pg{
		{"vid-ok", "/vid-ok", "published", fmt.Sprintf(`{"title":{"la":"V","en":"V"},"background":{"media_id":%d,"decorative":true},"background_video":{"media_id":%d}}`, poster, vid)},
		{"vid-draft", "/vid-draft", "draft", fmt.Sprintf(`{"title":{"la":"D","en":"D"},"background":{"media_id":%d,"decorative":true},"background_video":{"media_id":%d}}`, poster, vid)},
		{"vid-trashed", "/vid-trashed", "published", fmt.Sprintf(`{"title":{"la":"T","en":"T"},"background":{"media_id":%d,"decorative":true},"background_video":{"media_id":%d}}`, poster, trashed)},
		{"vid-pending", "/vid-pending", "published", fmt.Sprintf(`{"title":{"la":"P","en":"P"},"background":{"media_id":%d,"decorative":true},"background_video":{"media_id":%d}}`, poster, notReady)},
		{"vid-wrong", "/vid-wrong", "published", fmt.Sprintf(`{"title":{"la":"W","en":"W"},"background":{"media_id":%d,"decorative":true},"background_video":{"media_id":%d}}`, vid, photo)},
	}
	t.Cleanup(func() {
		for _, p := range pages {
			ownerExec(t, s, `delete from cms_page_sections where page_id = (select id from cms_pages where key = $1)`, p.key)
			ownerExec(t, s, `delete from cms_page_translations where page_id = (select id from cms_pages where key = $1)`, p.key)
			ownerExec(t, s, `delete from cms_pages where key = $1`, p.key)
		}
		ownerExec(t, s, `delete from media_assets where id in ($1, $2, $3)`, vid, trashed, notReady)
	})
	for _, p := range pages {
		ownerExec(t, s, `insert into cms_pages (key, name, path, template, status, published_at, in_sitemap) values ($1, $1, $2, 'standard', $3, case when $3 = 'published' then now() - interval '1 day' end, false)`, p.key, p.path, p.status)
		ownerExec(t, s, `insert into cms_page_translations (page_id, locale_code, title, seo_title) select id, 'la', $1, $1 from cms_pages where key = $1`, p.key)
		ownerExec(t, s, `insert into cms_page_sections (page_id, section_type_id, variant, sort_order, props)
			select (select id from cms_pages where key = $1), (select id from cms_section_types where key = 'hero_brand' and is_current), 'normal', 1, $2::jsonb`, p.key, p.props)
	}
	resolve := func(path string) map[string]any {
		return get(t, st, "/api/v1/public/pages/resolve?path="+path+"&locale=la")
	}
	mediaOf := func(d map[string]any, id int64) map[string]any {
		m, _ := d["media"].(map[string]any)[fmt.Sprint(id)].(map[string]any)
		return m
	}

	// the happy path
	d := resolve("/vid-ok")
	hero := sectionByKey(t, d, "hero_brand")
	props := hero["props"].(map[string]any)
	if bv := props["background_video"].(map[string]any); bv["media_id"].(float64) != float64(vid) || len(bv) != 1 {
		t.Errorf("props.background_video: %v", bv)
	}
	if bg := props["background"].(map[string]any); bg["media_id"].(float64) != float64(poster) {
		t.Errorf("props.background (the poster): %v", bg)
	}
	v := mediaOf(d, vid)
	if v == nil || v["kind"] != "video" || v["mime"] != "video/mp4" || v["duration_ms"].(float64) != 12500 || v["width"].(float64) != 1920 || v["height"].(float64) != 1080 || v["bytes"].(float64) != 4200000 ||
		v["url"] != "https://files.example.test/uploads/2026/hero%20loop.mp4" {
		t.Errorf("video item: %v", v)
	}
	p := mediaOf(d, poster)
	if p == nil || p["kind"] != "image" {
		t.Errorf("poster item: %v", p)
	} else if _, has := p["duration_ms"]; has {
		t.Errorf("an image has no duration_ms: %v", p)
	}
	if og := d["page"].(map[string]any)["seo"].(map[string]any)["og_image"].(map[string]any); og["media_id"].(float64) != float64(poster) {
		t.Errorf("the og image / LCP hint stays the poster: %v", og)
	}

	// nothing leaks
	if r := st.App.Get(t, "/api/v1/public/pages/resolve?path=/vid-draft&locale=la"); r.Status != http.StatusNotFound {
		t.Errorf("a draft page: %d", r.Status)
	}
	for _, c := range []struct {
		path string
		id   int64
	}{{"/vid-trashed", trashed}, {"/vid-pending", notReady}} {
		dd := resolve(c.path)
		if mediaOf(dd, c.id) != nil {
			t.Errorf("%s: an unavailable video leaked into data.media: %v", c.path, dd["media"])
		}
		if mediaOf(dd, poster) == nil {
			t.Errorf("%s: the poster must still be there", c.path)
		}
	}

	// wrong kinds are defensive: the page answers 200 and the misplaced assets are left out
	dw := resolve("/vid-wrong")
	if mediaOf(dw, vid) != nil || mediaOf(dw, photo) != nil {
		t.Errorf("wrong kinds in the slots must be left out of data.media: %v", dw["media"])
	}
}
