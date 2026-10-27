//go:build dbtest

package publicapi

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/repositories"
	"github.com/materm-organization/materm-web-portal-backend/pkg/media"
	"github.com/materm-organization/materm-web-portal-backend/pkg/publiccache"
	"github.com/materm-organization/materm-web-portal-backend/pkg/rediskeys"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/redistest"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/schemadb"
)

// The scratch schema (reference SQL + seeds + fixture) is built once for the whole package and dropped here.
func TestMain(m *testing.M) {
	code := m.Run()
	schemadb.Teardown()
	os.Exit(code)
}

// extraFixture adds what the endpoints need beyond pkg/testutil/schemadb/fixture.sql: pages in every lifecycle
// state, a page with sections of every kind (including types the code does not know), a page that exists in one
// language only, and a media setting.
const extraFixture = `
set cms.live_write = 'on';

insert into cms_section_types (key, version, name, json_schema, is_current, is_active)
values ('legacy_box', 1, 'Legacy box', '{"type":"object"}', true, true),
       ('hero_brand', 2, 'Hero brand v2', '{"type":"object"}', false, true);

insert into cms_pages (key, name, path, template, status, published_at, unpublished_at, in_sitemap) values
  ('lab',       'Lab',       '/lab',       'standard', 'published', now() - interval '1 day',   null,                      true),
  ('only-la',   'Only la',   '/only-la',   'standard', 'published', now() - interval '1 day',   null,                      true),
  ('nomap',     'No map',    '/nomap',     'standard', 'published', now() - interval '1 day',   null,                      false),
  ('scheduled', 'Scheduled', '/scheduled', 'standard', 'published', now() + interval '30 days', null,                      true),
  ('expired',   'Expired',   '/expired',   'standard', 'published', now() - interval '10 days', now() - interval '1 day',  true),
  ('draft',     'Draft',     '/draft',     'standard', 'draft',     null,                      null,                      true);

insert into cms_page_translations (page_id, locale_code, title, seo_title, seo_description)
select p.id, l.code, initcap(p.key) || ' ' || l.code, case when p.key = 'lab' then 'Lab SEO ' || l.code end, case when p.key = 'lab' then 'Lab description ' || l.code end
from cms_pages p cross join (values ('la'), ('en')) l(code)
where p.key in ('lab', 'nomap', 'scheduled', 'expired', 'draft');
insert into cms_page_translations (page_id, locale_code, title)
select p.id, 'la', 'Only la' from cms_pages p where p.key = 'only-la';

insert into cms_page_sections (page_id, section_type_id, variant, sort_order, anchor_id, props)
select (select id from cms_pages where key = 'lab'), t.id, s.variant, s.n, s.anchor, s.props::jsonb
from (values
  (1, 'hero_article', 1, null, null, '{"title":{"la":"Lab la","en":"Lab en"},"meta_items":[{"text":{"la":"One","en":""}},{"text":{"la":"Two"}}],"background":{"media_id":1,"decorative":true},"animate_in":true}'),
  (2, 'legacy_box', 1, null, null, '{"anything":true}'),
  (3, 'hero_brand', 2, null, null, '{"title":{"la":"future"}}'),
  (4, 'blog_strip', 1, null, 'strip', '{"eyebrow":{"la":"Blog"},"title":{"la":"Latest","en":"Latest en"},"source":"latest","limit":4}'),
  (5, 'gallery_cards', 1, 'standard', null, '{"eyebrow":{"la":"Gallery"},"title":{"la":"Cards"},"source":"album","album_id":1,"limit":2,"cards_link":{"type":"page","page_id":1}}'),
  (6, 'faq', 1, 'soft', null, '{"eyebrow":{"la":"FAQ"},"title":{"la":"Questions"},"group_key":"contact","open_first":true}'),
  (7, 'contact_section', 1, null, null, '{"eyebrow":{"la":"Contact"},"title":{"la":"Write"},"form_slug":"contact","info_items":[{"icon":"mail","label":{"la":"Email"},"source":"contact.email"}]}'),
  (8, 'post_list', 1, null, null, '{"page_size":12}')
) s(n, key, version, variant, anchor, props)
join cms_section_types t on t.key = s.key and t.version = s.version;

insert into cms_page_sections (page_id, section_type_id, sort_order, props)
select (select id from cms_pages where key = 'scheduled'), t.id, 1, '{"eyebrow":{"la":"x"},"title":{"la":"y"}}'::jsonb
from cms_section_types t where t.key = 'intro_heading' and t.is_current;

-- a menu item that points at a draft page: hidden by v_published_menu_items
insert into cms_menu_items (menu_id, sort_order, link_type, page_id, active_match)
select m.id, 99, 'page', p.id, 'prefix' from cms_menus m, cms_pages p where m.key = 'main' and p.key = 'draft';
insert into cms_menu_item_translations (menu_item_id, locale_code, label)
select i.id, l.code, 'Hidden draft' from cms_menu_items i cross join (values ('la'), ('en')) l(code) where i.sort_order = 99;

-- a media setting: the brand logo is the hero background image
update cms_site_settings set media_asset_id = (select id from media_assets where storage_key = 'static/images/site/home/hero-bg.png')
where group_key = 'brand' and key = 'logo_media_id';
`

var fixtureOnce sync.Once

// shared returns the scratch schema with extraFixture applied (once).
func shared(t testing.TB) *schemadb.Schema {
	t.Helper()
	s := schemadb.Shared(t)
	fixtureOnce.Do(func() {
		if _, err := s.Owner.ExecContext(context.Background(), extraFixture); err != nil {
			t.Fatalf("extra fixture: %v", err)
		}
	})
	return s
}

// ownerExec runs SQL as the owner of the schema with cms.live_write on (the way the publish engine writes).
func ownerExec(t testing.TB, s *schemadb.Schema, query string, args ...any) {
	t.Helper()
	ctx := context.Background()
	conn, err := s.Owner.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `set cms.live_write = 'on'`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// ownerInt64 runs a one-value query as the owner.
func ownerInt64(t testing.TB, s *schemadb.Schema, query string, args ...any) int64 {
	t.Helper()
	var v int64
	if err := s.Owner.QueryRowContext(context.Background(), query, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return v
}

// dbStack wires the three modules over the scratch database through the read-only role, with the response
// cache OFF (every request reads the database).
func dbStack(t testing.TB) (*stack, *schemadb.Schema) {
	t.Helper()
	s := shared(t)
	repos := repositories.New(s.GormRO(t))
	return wire(t, repos, publiccache.New(publiccache.Options{}), media.NewBuilder(media.Config{PathIP: "https://files.example.test"}), "https://fallback.example.test"), s
}

// cachedStack wires the modules with a real response cache on a random prefix of the scratch Redis.
func cachedStack(t testing.TB) (*stack, *schemadb.Schema, *publiccache.Cache, *redistest.Redis) {
	t.Helper()
	s := shared(t)
	r := redistest.New(t)
	cache := publiccache.New(publiccache.Options{Client: r.Client, Keys: rediskeys.New(r.Prefix), Enabled: true, TTL: time.Minute})
	repos := repositories.New(s.GormRO(t))
	return wire(t, repos, cache, media.NewBuilder(media.Config{PathIP: "https://files.example.test"}), ""), s, cache, r
}

// deadRedisCache is a cache whose Redis is unreachable (nothing listens on port 1): every operation fails.
func deadRedisCache() *publiccache.Cache {
	c := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 100 * time.Millisecond, MaxRetries: -1})
	return publiccache.New(publiccache.Options{Client: c, Keys: rediskeys.New("materm-test-dead:"), Enabled: true})
}

func dbRepos(t testing.TB, s *schemadb.Schema) *repositories.Set {
	return repositories.New(s.GormRO(t))
}

func dbMedia() *media.Builder {
	return media.NewBuilder(media.Config{PathIP: "https://files.example.test"})
}
