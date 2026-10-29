package assemble

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// The Raw adapter for POSTS: a frozen `cms.posts` SNAPSHOT (cms_content_revisions.snapshot, schema_version 1,
// doc/04 section 5.2; the admin writes it in modules/cms/posts/services/snapshot.go) decoded into RawPost. The
// building of the P-08 body from it lives in modules/preview/services (it needs the blog composer, and
// pkg/assemble is imported BY modules/blog/services, so it cannot import it back).
//
// Shape relied on (the admin's Snapshot struct):
//
//	{ "post": { "category_id", "author_id", "hero_media_id", "teaser_media_id", "is_featured", ["published_at"] },
//	  "translations": [ { "locale_code", "slug", "title", "excerpt", "label", "location_label", "body",
//	                      "gallery_heading", "gallery_description", "hero_alt", "seo_title", "seo_description",
//	                      "og_image_id", "is_enabled" } ],
//	  "tag_ids": [ int ],
//	  "media": [ { "id", "media_id", "sort_order", "translations": [ { "locale_code", "alt", "caption" } ] } ] }
//
// Every member is optional here (a draft may be incomplete); `published_at` is not written by the admin today
// (the engine owns that column) and is read when it appears.

// RawPost is a decoded post snapshot.
type RawPost struct {
	Post struct {
		CategoryID    *int64     `json:"category_id"`
		AuthorID      *int64     `json:"author_id"`
		HeroMediaID   *int64     `json:"hero_media_id"`
		TeaserMediaID *int64     `json:"teaser_media_id"`
		IsFeatured    bool       `json:"is_featured"`
		PublishedAt   *time.Time `json:"published_at"`
	} `json:"post"`
	Translations []RawPostTranslation `json:"translations"`
	TagIDs       []int64              `json:"tag_ids"`
	Media        []RawPostMedia       `json:"media"`
}

// RawPostTranslation is one entry of translations[]. A missing is_enabled counts as enabled.
type RawPostTranslation struct {
	LocaleCode         string          `json:"locale_code"`
	Slug               string          `json:"slug"`
	Title              string          `json:"title"`
	Excerpt            *string         `json:"excerpt"`
	Label              *string         `json:"label"`
	LocationLabel      *string         `json:"location_label"`
	Body               json.RawMessage `json:"body"`
	GalleryHeading     *string         `json:"gallery_heading"`
	GalleryDescription *string         `json:"gallery_description"`
	HeroAlt            *string         `json:"hero_alt"`
	SEOTitle           *string         `json:"seo_title"`
	SEODescription     *string         `json:"seo_description"`
	OGImageID          *int64          `json:"og_image_id"`
	IsEnabled          *bool           `json:"is_enabled"`
}

// Enabled reports whether the translation is shown (a missing flag counts as enabled).
func (t RawPostTranslation) Enabled() bool { return t.IsEnabled == nil || *t.IsEnabled }

// RawPostMedia is one gallery row.
type RawPostMedia struct {
	ID           int64                     `json:"id"`
	MediaID      int64                     `json:"media_id"`
	SortOrder    int                       `json:"sort_order"`
	Translations []RawPostMediaTranslation `json:"translations"`
}

// RawPostMediaTranslation is the alt / caption of one gallery row in one language.
type RawPostMediaTranslation struct {
	LocaleCode string  `json:"locale_code"`
	Alt        *string `json:"alt"`
	Caption    *string `json:"caption"`
}

// ParsePostSnapshot decodes the snapshot of a `cms.posts` revision. A snapshot with no usable slug in any
// translation is refused: a post preview has no URL to stand on. The text never carries snapshot content.
func ParsePostSnapshot(b []byte) (*RawPost, error) {
	var r RawPost
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("%w: not decodable", ErrBadSnapshot)
	}
	slug := false
	for _, t := range r.Translations {
		if strings.TrimSpace(t.Slug) != "" {
			slug = true
		}
	}
	if !slug {
		return nil, fmt.Errorf("%w: no translation with a slug", ErrBadSnapshot)
	}
	sort.SliceStable(r.Media, func(i, j int) bool {
		if r.Media[i].SortOrder != r.Media[j].SortOrder {
			return r.Media[i].SortOrder < r.Media[j].SortOrder
		}
		return r.Media[i].ID < r.Media[j].ID
	})
	return &r, nil
}

// HasSlug reports whether some translation of the snapshot has this slug (the preview link's `?path` is
// /blog/<slug of the language the link was issued for>, and the editor may switch language afterwards).
func (r *RawPost) HasSlug(slug string) bool {
	if slug == "" {
		return false
	}
	for _, t := range r.Translations {
		if t.Slug == slug {
			return true
		}
	}
	return false
}

// PickTranslation returns the first ENABLED translation in chain order (what v_published_posts does), or nil.
func (r *RawPost) PickTranslation(chain []string) (idx int, t *RawPostTranslation) {
	for _, code := range chain {
		for i := range r.Translations {
			if r.Translations[i].LocaleCode == code && r.Translations[i].Enabled() && r.Translations[i].Slug != "" {
				return i, &r.Translations[i]
			}
		}
	}
	return -1, nil
}
