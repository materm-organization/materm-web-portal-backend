//go:build dbtest

package readapi

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func albumID(t *testing.T, e *env, key string) int64 {
	t.Helper()
	var id int64
	ownerScalar(t, e.s, &id, `select id from cms_albums where key = $1`, key)
	return id
}

func TestAlbumsList(t *testing.T) {
	e := wire(t)
	r := e.app.Get(t, "/api/v1/public/albums?locale=en", nil)
	b := decode(t, r.Body)
	if r.Status != 200 || length(b["data"]) != 1 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	a := at(b, "data.0").(obj)
	cover := assetID(t, e, "uploads/2026/photo-b.webp")
	if num(a["id"]) != albumID(t, e, "forest") || a["key"] != "forest" || a["title"] != "Forest walk (en)" || a["description"] != "Album description" ||
		a["resolved_locale"] != "en" || num(at(a, "cover.media_id")) != cover || at(a, "cover.alt") != nil {
		t.Errorf("album: %v", a)
	}
	// the list envelope: data, meta and media side by side
	if at(b, "meta.total") != float64(1) || at(b, "meta.page") != float64(1) || at(b, "meta.total_page") != float64(1) {
		t.Errorf("meta: %v", b["meta"])
	}
	if str(at(b, fmt.Sprintf("media.%d.url", cover))) != "https://files.example.test/uploads/2026/photo-b.webp" {
		t.Errorf("media: %v", b["media"])
	}
	// Lao sees the same album with its Lao title
	if la := decode(t, e.app.Get(t, "/api/v1/public/albums?locale=la", nil).Body); at(la, "data.0.title") != "Forest walk (la)" {
		t.Errorf("la: %v", la["data"])
	}
}

func TestAlbumByKeyWithItemsAndMedia(t *testing.T) {
	e := wire(t)
	r := e.app.Get(t, "/api/v1/public/albums/forest?locale=en", nil)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	d := decode(t, r.Body)["data"].(obj)
	photoA, photoB := assetID(t, e, "uploads/2026/photo a.webp"), assetID(t, e, "uploads/2026/photo-b.webp")
	if d["locale"] != "en" || d["key"] != "forest" || d["title"] != "Forest walk (en)" || length(d["items"]) != 2 {
		t.Fatalf("album: %v", d)
	}
	i0, i1 := at(d, "items.0").(obj), at(d, "items.1").(obj)
	if num(i0["media_id"]) != photoA || i0["alt"] != "Item alt" || i0["caption_title"] != "Caption title" || i0["caption_description"] != "Caption description" || i0["sort_order"] != float64(1) || num(i0["id"]) == 0 {
		t.Errorf("item 1: %v", i0)
	}
	if num(i1["media_id"]) != photoB || i1["alt"] != nil || i1["caption_title"] != nil || i1["sort_order"] != float64(2) {
		t.Errorf("item 2 (nothing translated): %v", i1)
	}
	m := d["media"].(obj)
	for _, id := range []int64{photoA, photoB} {
		if _, ok := m[fmt.Sprint(id)]; !ok {
			t.Errorf("media %d missing", id)
		}
	}
	// the headers
	if r.Header.Get("ETag") == "" || r.Header.Get("Content-Language") != "en" {
		t.Errorf("%v", r.Header)
	}
	// the tags: the album, the locales
	if len(members(t, e, fmt.Sprintf("album:%d", albumID(t, e, "forest")))) != 1 {
		t.Error("album:<id> not emitted")
	}
	e.app.Get(t, "/api/v1/public/albums?locale=en", nil)
	if len(members(t, e, "albums:list")) != 1 {
		t.Error("albums:list not emitted")
	}
}

func TestUnpublishedAlbumIsHiddenEverywhere(t *testing.T) {
	e := wire(t)
	ownerExec(t, e.s, `
	  insert into cms_albums (key, status) values ('secret', 'draft');
	  insert into cms_album_translations (album_id, locale_code, title) select id, 'la', 'Secret' from cms_albums where key = 'secret';
	  insert into cms_albums (key, status, published_at) values ('later', 'published', now() + interval '10 days');
	  insert into cms_album_translations (album_id, locale_code, title) select id, 'la', 'Later' from cms_albums where key = 'later';
	  insert into cms_albums (key, status, published_at, unpublished_at) values ('gone', 'published', timestamptz '2026-01-01 00:00+00', timestamptz '2026-01-02 00:00+00');
	  insert into cms_album_translations (album_id, locale_code, title) select id, 'la', 'Gone' from cms_albums where key = 'gone';`)
	t.Cleanup(func() { ownerExec(t, e.s, `delete from cms_albums where key in ('secret', 'later', 'gone')`) })

	for _, key := range []string{"secret", "later", "gone", "missing", "Bad_Key", "1abc", strings.Repeat("a", 61)} {
		r := e.app.Get(t, "/api/v1/public/albums/"+key+"?locale=la", nil)
		if r.Status != 404 || at(decode(t, r.Body), "code") != "NOT_FOUND" || r.Header.Get("Cache-Control") != "public, max-age=30" {
			t.Errorf("%s: %d %s", key, r.Status, r.Body)
		}
	}
	if b := decode(t, e.app.Get(t, "/api/v1/public/albums?locale=la", nil).Body); length(b["data"]) != 1 {
		t.Errorf("the list shows only the published album: %v", b["data"])
	}
}

func TestFaqGroup(t *testing.T) {
	e := wire(t)
	r := e.app.Get(t, "/api/v1/public/faq/contact?locale=en", nil)
	d := decode(t, r.Body)["data"].(obj)
	if r.Status != 200 || d["locale"] != "en" || d["group_key"] != "contact" || d["resolved_locale"] != "en" || length(d["items"]) != 4 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	it := at(d, "items.0").(obj)
	if num(it["id"]) == 0 || it["question"] != "How do I get to Materm?" || at(it, "answer.type") != "doc" || at(it, "answer.content.0.type") != "paragraph" ||
		!strings.Contains(str(at(it, "answer.content.0.content.0.text")), "Phou Khao Khouay") {
		t.Errorf("item: %v", it)
	}
	// ordered by sort_order
	if at(d, "items.1.question") != "What are the check-in and opening times?" {
		t.Errorf("order: %v", at(d, "items.1.question"))
	}
	var gid int64
	ownerScalar(t, e.s, &gid, `select id from cms_faq_groups where key = 'contact'`)
	if len(members(t, e, fmt.Sprintf("faq:%d", gid))) != 1 {
		t.Error("faq:<id> not emitted")
	}
	for _, key := range []string{"nope", "Bad", "1x"} {
		if r := e.app.Get(t, "/api/v1/public/faq/"+key+"?locale=en", nil); r.Status != 404 {
			t.Errorf("%s: %d", key, r.Status)
		}
	}
}

func TestFaqHidesInvisibleItemsAndUnpublishedGroupsAndResolvesLinks(t *testing.T) {
	e := wire(t)
	ctx := context.Background()
	_ = ctx
	ownerExec(t, e.s, `
	  update cms_faq_items set is_visible = false where id = (select i.id from cms_faq_items i join cms_faq_groups g on g.id = i.group_id where g.key = 'contact' order by i.sort_order limit 1);
	  insert into cms_faq_groups (key, name, status) values ('draft-faq', 'Draft', 'draft');
	  insert into cms_faq_items (group_id, sort_order) select id, 1 from cms_faq_groups where key = 'draft-faq';
	  insert into cms_faq_item_translations (faq_item_id, locale_code, question, answer)
	    select i.id, 'la', 'Hidden?', '{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"x"}]}]}'::jsonb from cms_faq_items i join cms_faq_groups g on g.id = i.group_id where g.key = 'draft-faq';
	  insert into cms_faq_groups (key, name, status, published_at) values ('linky', 'Linky', 'published', timestamptz '2026-01-01 00:00+00');
	  insert into cms_faq_items (group_id, sort_order) select id, 1 from cms_faq_groups where key = 'linky';
	  insert into cms_faq_item_translations (faq_item_id, locale_code, question, answer)
	    select i.id, 'la', 'Where?', '{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"see "},{"type":"text","text":"this page","marks":[{"type":"link","attrs":{"page_id":2}}]},{"type":"text","text":" or "},{"type":"text","text":"a missing one","marks":[{"type":"link","attrs":{"page_id":999999}}]}]}]}'::jsonb
	    from cms_faq_items i join cms_faq_groups g on g.id = i.group_id where g.key = 'linky';`)
	t.Cleanup(func() {
		ownerExec(t, e.s, `
		  update cms_faq_items set is_visible = true;
		  delete from cms_faq_groups where key in ('draft-faq', 'linky');`)
	})

	// one of the four items of 'contact' is invisible now
	if d := decode(t, e.app.Get(t, "/api/v1/public/faq/contact?locale=en", nil).Body); length(at(d, "data.items")) != 3 {
		t.Errorf("an item with is_visible=false must be hidden: %v", at(d, "data.items"))
	}
	// an unpublished group is a 404
	if r := e.app.Get(t, "/api/v1/public/faq/draft-faq?locale=la", nil); r.Status != 404 {
		t.Errorf("draft group: %d", r.Status)
	}
	// links in answers: the page link is an href, the link to a page that is not published is dropped (text kept)
	var pagePath string
	ownerScalar(t, e.s, &pagePath, `select path from cms_pages where id = 2`)
	r := e.app.Get(t, "/api/v1/public/faq/linky?locale=en", nil)
	body := string(r.Body)
	if r.Status != 200 || !strings.Contains(body, `"href":"`+pagePath+`"`) || strings.Contains(body, "999999") || !strings.Contains(body, "a missing one") {
		t.Fatalf("links: %d %s", r.Status, body)
	}
	// the answer depends on the page's path: tagged pages
	if len(members(t, e, "pages")) != 1 {
		t.Error("a faq answer that links a page must be tagged pages")
	}
	// English is not translated: the Lao item is shown, the group says so
	if at(decode(t, r.Body), "data.resolved_locale") != "la" {
		t.Errorf("resolved_locale: %s", body)
	}
}
