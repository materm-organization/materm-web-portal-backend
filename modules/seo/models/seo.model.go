// Package models holds the DTOs of the seo module (P-16).
package models

// SitemapEntry is one URL of the sitemap: kind is "page" or "post", path the site path in the default locale
// (post: "/blog/<slug>"), lastmod an RFC 3339 UTC instant.
type SitemapEntry struct {
	Kind     string `json:"kind"`
	EntityID int64  `json:"entity_id"`
	Path     string `json:"path"`
	Lastmod  string `json:"lastmod"`
}

// SitemapData is `data` of P-16. Locale is the default locale (the URLs are canonical), BaseURL the public site
// URL without a trailing slash.
type SitemapData struct {
	Locale  string         `json:"locale"`
	BaseURL string         `json:"base_url"`
	Entries []SitemapEntry `json:"entries"`
}
