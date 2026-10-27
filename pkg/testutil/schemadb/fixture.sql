-- Minimal published content on top of the reference seeds (001, 004, 005, 006), so that EVERY one of the 21
-- public views returns at least one row and every column of every read-struct is scanned at least once.
-- Run as cms_migrator with cms.live_write = on (bulk load of already-published rows, see cms_guard_live_write).
set cms.live_write = 'on';

-- ---- media ----------------------------------------------------------------------------------------------------
insert into media_assets (kind, status, storage_key, original_filename, mime_type, bytes, width, height, focal_x, focal_y, blurhash, dominant_color)
values ('image', 'ready', 'static/images/site/home/hero-bg.png', 'hero-bg.png', 'image/png', 1000, 3456, 2048, 0.5, 0.4, 'LEHV6nWB2yk8pyo0adR*.7kCMdnj', '#112233'),
       ('image', 'ready', 'uploads/2026/photo a.webp', 'photo a.webp', 'image/webp', 2000, 1200, 800, null, null, null, null),
       ('image', 'ready', 'uploads/2026/photo-b.webp', 'photo-b.webp', 'image/webp', 2100, 1200, 800, null, null, null, null),
       ('image', 'uploading', 'uploads/pending.png', 'pending.png', 'image/png', 1, null, null, null, null, null, null);
insert into media_assets (kind, status, storage_key, original_filename, mime_type, bytes, deleted_at)
values ('image', 'ready', 'uploads/deleted.png', 'deleted.png', 'image/png', 1, now());

insert into media_asset_translations (asset_id, locale_code, alt, caption, credit)
select id, 'en', 'Hero background', 'A caption', 'Photo credit' from media_assets where storage_key = 'static/images/site/home/hero-bg.png';
insert into media_asset_translations (asset_id, locale_code, alt)
select id, 'la', 'Hero background (la)' from media_assets where storage_key = 'static/images/site/home/hero-bg.png';
insert into media_asset_variants (asset_id, variant_key, format, width, height, bytes, storage_key)
select id, 'w640', 'webp', 640, 379, 100, 'variants/hero-bg-640.webp' from media_assets where storage_key = 'static/images/site/home/hero-bg.png';

-- ---- taxonomy, authors, posts ---------------------------------------------------------------------------------------
insert into cms_tags (key) values ('night'), ('trek');
insert into cms_tag_translations (tag_id, locale_code, name, slug)
select t.id, l.code, initcap(t.key), t.key from cms_tags t cross join (values ('la'), ('en')) l(code);
insert into cms_authors (display_name) values ('Materm Team');

-- post 1: both locales, different slugs per locale (308 case), body with a link and an image, a gallery, a tag
insert into cms_posts (category_id, author_id, hero_media_id, teaser_media_id, is_featured, status, published_at)
select (select id from cms_categories where key = 'music'), (select id from cms_authors limit 1),
       (select id from media_assets where storage_key = 'static/images/site/home/hero-bg.png'),
       (select id from media_assets where storage_key = 'uploads/2026/photo a.webp'),
       true, 'published', timestamptz '2026-03-01 10:00+00';
-- post 2: only the default locale (en falls back to la), newer
insert into cms_posts (category_id, hero_media_id, status, published_at)
select (select id from cms_categories where key = 'camping'), (select id from media_assets where storage_key = 'uploads/2026/photo-b.webp'),
       'published', timestamptz '2026-04-01 10:00+00';
-- post 3: draft (must never be visible)
insert into cms_posts (category_id, status) select (select id from cms_categories where key = 'music'), 'draft';

insert into cms_post_translations (post_id, locale_code, slug, title, excerpt, label, location_label, body, gallery_heading, gallery_description, hero_alt, seo_title, seo_description, og_image_id)
select p.id, v.code, v.slug, v.title, v.excerpt, 'Music Night', 'Phou Khao Khouay', v.body::jsonb, 'Gallery', 'Photos of the night', 'Hero alt', 'SEO title', 'SEO description',
       (select id from media_assets where storage_key = 'uploads/2026/photo a.webp')
from cms_posts p
cross join (values
  ('la', 'music-night-la', 'Music night (la)', 'Excerpt la', '{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Body la"}]}]}'),
  ('en', 'music-night', 'Music night', 'Excerpt en', '{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Body en "},{"type":"text","text":"link","marks":[{"type":"link","attrs":{"page_id":2}}]}]},{"type":"image","attrs":{"media_id":2,"alt":"inline"}}]}')
) v(code, slug, title, excerpt, body)
where p.published_at = timestamptz '2026-03-01 10:00+00';
insert into cms_post_translations (post_id, locale_code, slug, title, excerpt)
select p.id, 'la', 'camp-weekend', 'Camp weekend', 'Camping excerpt' from cms_posts p where p.published_at = timestamptz '2026-04-01 10:00+00';
insert into cms_post_translations (post_id, locale_code, slug, title)
select p.id, 'la', 'draft-post', 'Draft' from cms_posts p where p.status = 'draft';

insert into cms_post_tags (post_id, tag_id)
select p.id, t.id from cms_posts p, cms_tags t where p.published_at = timestamptz '2026-03-01 10:00+00' and t.key = 'night';

insert into cms_post_media (post_id, media_id, sort_order)
select p.id, m.id, 1 from cms_posts p, media_assets m
where p.published_at = timestamptz '2026-03-01 10:00+00' and m.storage_key = 'uploads/2026/photo-b.webp';
insert into cms_post_media_translations (post_media_id, locale_code, alt, caption)
select pm.id, 'en', 'Gallery alt en', 'Gallery caption en' from cms_post_media pm;

-- ---- albums -------------------------------------------------------------------------------------------------------
insert into cms_albums (key, cover_media_id, status, published_at)
select 'forest', (select id from media_assets where storage_key = 'uploads/2026/photo-b.webp'), 'published', timestamptz '2026-02-01 10:00+00';
insert into cms_album_translations (album_id, locale_code, title, description)
select a.id, l.code, 'Forest walk (' || l.code || ')', 'Album description' from cms_albums a cross join (values ('la'), ('en')) l(code);
insert into cms_album_items (album_id, media_id, sort_order, layout_hint)
select a.id, m.id, s.n, 'wide' from cms_albums a, (values (1, 'uploads/2026/photo a.webp'), (2, 'uploads/2026/photo-b.webp')) s(n, k)
join media_assets m on m.storage_key = s.k;
insert into cms_album_item_translations (album_item_id, locale_code, caption_title, caption_description, alt)
select i.id, 'en', 'Caption title', 'Caption description', 'Item alt' from cms_album_items i where i.sort_order = 1;

-- ---- page sections ------------------------------------------------------------------------------------------------
insert into cms_page_sections (page_id, section_type_id, variant, sort_order, anchor_id, props)
select (select id from cms_pages where key = 'home'), (select id from cms_section_types where key = 'hero_brand' and is_current), 'normal', 1, 'hero',
       '{"title":{"la":"Hello la","en":"Hello en"},"background":{"media_id":1,"alt":{"la":"","en":""},"decorative":true},"show_logo":true,"animate_in":true}'::jsonb;
insert into cms_page_sections (page_id, section_type_id, sort_order, props, is_visible)
select (select id from cms_pages where key = 'home'), (select id from cms_section_types where key = 'faq' and is_current), 2, '{"group_key":"contact"}'::jsonb, false;

-- ---- collections ----------------------------------------------------------------------------------------------------
insert into cms_collections (key, name, json_schema) values ('events', 'Events', '{"type":"object"}'::jsonb);
insert into cms_collection_entries (collection_id, data, status, published_at)
select id, '{"date":"2026-05-01"}'::jsonb, 'published', timestamptz '2026-01-01 00:00+00' from cms_collections where key = 'events';
insert into cms_collection_entry_translations (entry_id, collection_id, locale_code, title, slug, data)
select e.id, e.collection_id, 'la', 'Opening day', 'opening-day', '{"place":"camp"}'::jsonb from cms_collection_entries e;

-- ---- redirects ------------------------------------------------------------------------------------------------------
insert into cms_redirects (source_path, locale_code, target, status_code) values ('/old-blog', null, '/blog', 301), ('/old-blog', 'en', '/blog?en=1', 302);

-- ---- posts that must NEVER be visible: scheduled in the future, and time-boxed out --------------------------
insert into cms_posts (category_id, status, published_at)
select (select id from cms_categories where key = 'music'), 'published', now() + interval '30 days';
insert into cms_post_translations (post_id, locale_code, slug, title)
select id, 'la', 'future-post', 'Future' from cms_posts where published_at > now();
insert into cms_posts (category_id, status, published_at, unpublished_at)
select (select id from cms_categories where key = 'music'), 'published', timestamptz '2026-01-01 00:00+00', timestamptz '2026-01-02 00:00+00';
insert into cms_post_translations (post_id, locale_code, slug, title)
select id, 'la', 'expired-post', 'Expired' from cms_posts where unpublished_at is not null;
