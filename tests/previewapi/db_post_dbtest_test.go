//go:build dbtest

package previewapi

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	blogservices "github.com/materm-organization/materm-web-portal-backend/modules/blog/services"
	"github.com/materm-organization/materm-web-portal-backend/modules/content/repositories"
	previewservices "github.com/materm-organization/materm-web-portal-backend/modules/preview/services"
	"github.com/materm-organization/materm-web-portal-backend/pkg/media"
	"github.com/materm-organization/materm-web-portal-backend/pkg/publiccache"
	"github.com/materm-organization/materm-web-portal-backend/pkg/rediskeys"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/jsondiff"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/redistest"
)

// baselinePost is the image of a `cms.posts` revision exactly as the ADMIN backend's posts module writes it
// (modules/cms/posts/services/snapshot.go, Snapshot: post / translations / tag_ids / media, every translatable
// column but search_tsv and deleted_at), built from the live rows with SQL because the portal tests cannot call
// the admin's Go code.
const baselinePost = `
select jsonb_build_object(
  'post', jsonb_build_object('category_id', p.category_id, 'author_id', p.author_id, 'hero_media_id', p.hero_media_id,
                             'teaser_media_id', p.teaser_media_id, 'is_featured', p.is_featured),
  'translations', coalesce((select jsonb_agg(jsonb_build_object('locale_code', t.locale_code, 'slug', t.slug, 'title', t.title,
        'excerpt', t.excerpt, 'label', t.label, 'location_label', t.location_label, 'body', t.body,
        'gallery_heading', t.gallery_heading, 'gallery_description', t.gallery_description, 'hero_alt', t.hero_alt,
        'seo_title', t.seo_title, 'seo_description', t.seo_description, 'og_image_id', t.og_image_id, 'is_enabled', t.is_enabled)
        order by t.locale_code)
      from cms_post_translations t where t.post_id = p.id and t.deleted_at is null), '[]'::jsonb),
  'tag_ids', coalesce((select jsonb_agg(pt.tag_id order by pt.tag_id) from cms_post_tags pt where pt.post_id = p.id), '[]'::jsonb),
  'media', coalesce((select jsonb_agg(jsonb_build_object('id', pm.id, 'media_id', pm.media_id, 'sort_order', pm.sort_order,
        'translations', coalesce((select jsonb_agg(jsonb_build_object('locale_code', x.locale_code, 'alt', x.alt, 'caption', x.caption)
            order by x.locale_code) from cms_post_media_translations x where x.post_media_id = pm.id), '[]'::jsonb))
        order by pm.sort_order, pm.id)
      from cms_post_media pm where pm.post_id = p.id), '[]'::jsonb))::text
from cms_posts p where p.id = $1`

// issuePost does what the admin's POST /posts/{id}/preview-links does: a history revision of the post as it is now
// and a token bound to it.
func (e *dbEnv) issuePost(t *testing.T, seed string, postID int64, locale string, ttl time.Duration) (token string, tokenID int64) {
	t.Helper()
	var snap string
	if err := e.s.Owner.QueryRow(baselinePost, postID).Scan(&snap); err != nil {
		t.Fatal(err)
	}
	return e.issueSnapshot(t, seed, "cms.posts", postID, locale, ttl, snap)
}

func (e *dbEnv) postID(t *testing.T, where string) int64 {
	t.Helper()
	var id int64
	if err := e.s.Owner.QueryRow(`select id from cms_posts where ` + where).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// The snapshot the ADMIN writes, consumed through the real SQL function by the read-only role, renders to the same
// body as P-08 for the live post: category, tags, author, gallery, body with its link and image, SEO, dates.
func TestDBPostPreviewEqualsP08(t *testing.T) {
	e := dbSetup(t)
	st := e.stack(t)
	ctx := context.Background()
	blog := blogservices.NewBlogService(blogservices.Deps{
		Content: e.set, Media: media.NewBuilder(media.Config{}), Lookup: repositories.NewMediaLookup(e.set.Assets),
		Cache: publiccache.New(publiccache.Options{}), Locales: st.Provider,
	})
	id := e.postID(t, `published_at = timestamptz '2026-03-01 10:00+00'`)
	for loc, slug := range map[string]string{"la": "music-night-la", "en": "music-night"} {
		out, err := blog.Post(ctx, loc, slug)
		if err != nil || out.Post == nil {
			t.Fatalf("%s: the public post: %v %+v", loc, err, out)
		}
		want, _ := jsondiff.ToGeneric(out.Post)

		tok, _ := e.issuePost(t, "p8-"+loc, id, "", time.Hour)
		r := st.resolve(t, tok, "?locale="+loc+"&path=/blog/"+slug)
		if r.Status != http.StatusOK {
			t.Fatalf("%s: %d %s", loc, r.Status, r.Body)
		}
		if r.Data()["kind"] != "post" {
			t.Fatalf("%s: kind %v", loc, r.Data()["kind"])
		}
		got, _ := jsondiff.ToGeneric(r.Data()["post"])
		mask := func(p string) bool { return p == ".updated_at" || p == ".seo.noindex" }
		if diffs := jsondiff.Diff(want, got, jsondiff.Options{Mask: mask, Extras: map[string]bool{}}); len(diffs) > 0 {
			t.Fatalf("%s: %d differences from P-08:\n%s", loc, len(diffs), strings.Join(diffs, "\n"))
		}
		if seo := r.Data()["post"].(map[string]any)["seo"].(map[string]any); seo["noindex"] != true {
			t.Errorf("%s: a preview must be noindex", loc)
		}
		if w := r.Data()["preview"].(map[string]any)["warnings"].([]any); len(w) != 0 {
			t.Errorf("%s: unexpected warnings %v", loc, w)
		}
	}
	// the same link works for the other language's slug (the editor switched language)
	tok, _ := e.issuePost(t, "p8-switch", id, "la", time.Hour)
	if r := st.resolve(t, tok, "?locale=en&path=/blog/music-night-la"); r.Status != http.StatusOK || r.Data()["post"].(map[string]any)["slug"] != "music-night" {
		t.Errorf("language switch: %d %s", r.Status, r.Body)
	}
	if r := st.resolve(t, tok, "?path=/blog/camp-weekend"); r.Status != http.StatusUnauthorized {
		t.Errorf("another post's path: %d", r.Status)
	}
}

// A draft that was never published has no public page, but the link shows it; the snapshot is frozen at the moment
// the link was issued.
func TestDBPostPreviewOfADraftAndFrozen(t *testing.T) {
	e := dbSetup(t)
	st := e.stack(t)
	draft := e.postID(t, `status = 'draft'`)
	tok, _ := e.issuePost(t, "draft", draft, "", time.Hour)
	r := st.resolve(t, tok, "?locale=la")
	if r.Status != http.StatusOK {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	p := r.Data()["post"].(map[string]any)
	if p["title"] != "Draft" || p["slug"] != "draft-post" || p["public_id"] != "" {
		t.Errorf("draft: %v %v %v", p["title"], p["slug"], p["public_id"])
	}
	if p["category"] == nil {
		t.Error("the (published) category of the draft should be shown")
	}
	// it is not public
	if got, _ := e.set.Posts.FindBySlug(context.Background(), "la", "draft-post"); got != nil {
		t.Fatal("the fixture's draft became public: the test proves nothing")
	}

	e.exec(t, `update cms_post_translations set title = 'EDITED AFTER THE LINK' where post_id = $1 and locale_code = 'la'`, draft)
	defer e.exec(t, `update cms_post_translations set title = 'Draft' where post_id = $1 and locale_code = 'la'`, draft)
	if r = st.resolve(t, tok, "?locale=la"); r.Data()["post"].(map[string]any)["title"] != "Draft" {
		t.Errorf("the preview followed the live edit: %v", r.Data()["post"].(map[string]any)["title"])
	}
}

// Many calls, one token: no use limit; with the 10 s snapshot copy only the first call reaches the function; a
// token of a post must also be bound to its entity type (a page path is refused).
func TestDBPostPreviewMultiUseAndCache(t *testing.T) {
	e := dbSetup(t)
	id := e.postID(t, `published_at = timestamptz '2026-03-01 10:00+00'`)
	st := e.stack(t)
	tok, tid := e.issuePost(t, "post-multi", id, "en", time.Hour)
	for i := 0; i < 5; i++ {
		if r := st.resolve(t, tok, ""); r.Status != http.StatusOK {
			t.Fatalf("call %d: %d", i+1, r.Status)
		}
	}
	if n := e.uses(t, tid); n != 5 {
		t.Errorf("use_count %d, want 5", n)
	}

	rd := redistest.New(t)
	cached := e.stack(t, func(c *opts) {
		c.redis, c.keys = rd.Client, rediskeys.New(rd.Prefix)
		c.svc = func(d *previewservices.Deps) { d.Options.CacheTTL = 10 * time.Second }
	})
	tok2, tid2 := e.issuePost(t, "post-multi2", id, "en", time.Hour)
	for i := 0; i < 5; i++ {
		if r := cached.resolve(t, tok2, ""); r.Status != http.StatusOK {
			t.Fatalf("cached call %d: %d", i+1, r.Status)
		}
	}
	if n := e.uses(t, tid2); n != 1 {
		t.Errorf("with the snapshot copy use_count %d, want 1", n)
	}
	if r := st.resolve(t, tok, "?path=/our-path"); r.Status != http.StatusUnauthorized {
		t.Errorf("a post token with a page path: %d", r.Status)
	}
}
