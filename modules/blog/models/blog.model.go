// Package models holds the DTOs of the blog module. The JSON shapes are those of the frontend loader
// (materm-web-portal-frontend src/lib/cms/types.ts: PostListItem, PostDetail, CategoryInfo, TagInfo), which
// win over doc/08 where the two differ (doc/08 section 2.3.5).
package models

import (
	"github.com/materm-organization/materm-web-portal-backend/pkg/media"
	"github.com/materm-organization/materm-web-portal-backend/pkg/pagination"
)

// ImageRef points into the media map. Alt is the alt text of this placement; nil means "use the asset's own
// alt in media[media_id]".
type ImageRef struct {
	MediaID int64   `json:"media_id"`
	Alt     *string `json:"alt"`
}

// MediaRef names an asset without a placement alt (seo.og_image).
type MediaRef struct {
	MediaID int64 `json:"media_id"`
}

// CategoryRef is the category of a post.
type CategoryRef struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// AuthorRef is the author of a post (the portal can read only the display name).
type AuthorRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// PostItem is an entry of P-07 and the common part of P-08.
type PostItem struct {
	ID             int64        `json:"id"`
	PublicID       string       `json:"public_id"`
	Slug           string       `json:"slug"`
	Href           string       `json:"href"`
	ResolvedLocale string       `json:"resolved_locale"`
	IsFallback     bool         `json:"is_fallback"`
	Title          string       `json:"title"`
	Excerpt        *string      `json:"excerpt"`
	Label          *string      `json:"label"`
	LocationLabel  *string      `json:"location_label"`
	Date           string       `json:"date"`
	DateTime       string       `json:"date_time"`
	Category       *CategoryRef `json:"category"`
	Author         *AuthorRef   `json:"author"`
	Image          *ImageRef    `json:"image"`
	IsFeatured     bool         `json:"is_featured"`
}

// PostsPage is the data of P-07; the controller writes it as {data: Items, meta: Meta, media: Media}.
type PostsPage struct {
	Items []PostItem      `json:"items"`
	Meta  pagination.Meta `json:"meta"`
	Media media.Map       `json:"media"`
}

// GalleryItem is a picture at the end of a post.
type GalleryItem struct {
	MediaID int64   `json:"media_id"`
	Alt     *string `json:"alt"`
	Caption *string `json:"caption"`
}

// Gallery is the picture strip of a post.
type Gallery struct {
	Heading     *string       `json:"heading"`
	Description *string       `json:"description"`
	Items       []GalleryItem `json:"items"`
}

// TagInfo is a tag (P-10 and the tags of a post).
type TagInfo struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// CategoryInfo is a category (P-09).
type CategoryInfo struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	SortOrder int    `json:"sort_order"`
}

// SEO is the metadata of a post page.
type SEO struct {
	Title       string    `json:"title"`
	Description *string   `json:"description"`
	OGImage     *MediaRef `json:"og_image"`
	Canonical   string    `json:"canonical"`
	Noindex     bool      `json:"noindex"`
}

// PostDetail is the data of P-08: flat (the post's fields sit directly in `data`, no `post` key).
type PostDetail struct {
	PostItem
	Locale    string         `json:"locale"`
	Body      map[string]any `json:"body"`
	Hero      *ImageRef      `json:"hero"`
	Gallery   Gallery        `json:"gallery"`
	Tags      []TagInfo      `json:"tags"`
	SEO       SEO            `json:"seo"`
	UpdatedAt string         `json:"updated_at"`
	Media     media.Map      `json:"media"`
}

// Outcome kinds of P-08.
const (
	KindPost     = "post"
	KindRedirect = "redirect"
	KindNotFound = "not_found"
)

// PostOutcome is what resolving a slug gives. A 404 and a 308 are VALUES (not errors) so that they can be
// cached for a short time like a post (doc/08 section 2.3.3).
type PostOutcome struct {
	Kind     string      `json:"kind"`
	Post     *PostDetail `json:"post,omitempty"`
	Location string      `json:"location,omitempty"`
}
