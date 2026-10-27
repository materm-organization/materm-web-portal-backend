// Package models holds the DTOs of the faq module (src/lib/cms/types.ts: FaqGroup, FaqItem).
package models

import "github.com/materm-organization/materm-web-portal-backend/pkg/assemble"

// FaqItem is a question with its answer (rich text, profile lite, links resolved). It is the shape that
// `resolved.items` of a faq section carries as well: one type, built by assemble.BuildFAQItems.
type FaqItem = assemble.FAQItem

// FaqGroup is the data of P-13. ResolvedLocale is the group's language: the requested one when at least one
// item is written in it, otherwise the language of the first item (the view resolves item by item).
type FaqGroup struct {
	Locale         string    `json:"locale"`
	GroupKey       string    `json:"group_key"`
	ResolvedLocale string    `json:"resolved_locale"`
	Items          []FaqItem `json:"items"`
}
