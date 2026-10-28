// Package models holds the DTOs of the preview module (P-19, doc/08 section 3.9, openapi-portal.yaml).
package models

import "github.com/materm-organization/materm-web-portal-backend/pkg/assemble"

// Kinds of data.kind.
const (
	KindPage = "page"
	// KindPost is reserved: post previews arrive with the posts milestone (M5, doc/05 PT-18). TODO(M5): build
	// the P-08 shape from a `cms.posts` snapshot.
	KindPost = "post"
)

// PreviewData is `data` of GET /preview/resolve. Page is the same object P-06 answers as `data` (locale, page,
// sections, media), built from the frozen snapshot.
type PreviewData struct {
	Locale  string             `json:"locale"`
	Kind    string             `json:"kind"`
	Page    *assemble.PageData `json:"page,omitempty"`
	Preview Info               `json:"preview"`
}

// Info is data.preview. The function that consumes the token returns neither the revision id nor the expiry
// (the portal role cannot read cms_preview_tokens), so those members of the OpenAPI schema are not sent.
type Info struct {
	EntityType string             `json:"entity_type"`
	EntityID   int64              `json:"entity_id"`
	Locale     string             `json:"locale"`
	Warnings   []assemble.Warning `json:"warnings"`
}
