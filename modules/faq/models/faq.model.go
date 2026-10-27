// Package models holds the DTOs of the faq module (src/lib/cms/types.ts: FaqGroup, FaqItem).
package models

// FaqItem is a question with its answer (rich text, profile lite, links resolved).
type FaqItem struct {
	ID       int64          `json:"id"`
	Question string         `json:"question"`
	Answer   map[string]any `json:"answer"`
}

// FaqGroup is the data of P-13. ResolvedLocale is the group's language: the requested one when at least one
// item is written in it, otherwise the language of the first item (the view resolves item by item).
type FaqGroup struct {
	Locale         string    `json:"locale"`
	GroupKey       string    `json:"group_key"`
	ResolvedLocale string    `json:"resolved_locale"`
	Items          []FaqItem `json:"items"`
}
