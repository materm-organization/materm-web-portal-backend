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

// RedirectData is `data` of P-17: the place the visitor goes to. Target is the final internal path ("/new",
// may carry a query string) or an absolute http(s) URL; StatusCode is the status of the LAST rule of the chain
// (301, 302, 307 or 308); Hops is how many rules were followed (1 to 3).
type RedirectData struct {
	Target     string `json:"target"`
	StatusCode int    `json:"status_code"`
	Hops       int    `json:"hops"`
}

// RedirectOutcome is what the response cache stores for P-17: a negative answer (Found false) is cached too,
// for a short time.
type RedirectOutcome struct {
	Found bool          `json:"found"`
	Data  *RedirectData `json:"data,omitempty"`
}
