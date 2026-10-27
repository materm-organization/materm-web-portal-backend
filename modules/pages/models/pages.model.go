// Package models holds the DTOs of the pages module (P-06, doc/08 section 2.3.1).
package models

import "github.com/materm-organization/materm-web-portal-backend/pkg/assemble"

// PageData is `data` of GET /public/pages/resolve: locale, page (with seo), ordered sections and the media map.
type PageData = assemble.PageData
