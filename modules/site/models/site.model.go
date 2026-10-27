// Package models holds the DTOs of the site module (P-01 ... P-05, doc/08 section 2.3.5). The shapes are the
// ones the portal-frontend loaders type (src/lib/cms/types.ts), which win over the older prose of doc/08.
package models

import (
	"github.com/materm-organization/materm-web-portal-backend/pkg/assemble"
	"github.com/materm-organization/materm-web-portal-backend/pkg/media"
)

// LocaleInfo is one active language (P-01; data.locales of P-05).
type LocaleInfo = assemble.LocaleInfo

// Settings is group -> key -> value (localised values collapsed, media settings are media ids).
type Settings = assemble.Settings

// SettingsData is `data` of P-02.
type SettingsData struct {
	Locale   string    `json:"locale"`
	Settings Settings  `json:"settings"`
	Media    media.Map `json:"media"`
}

// UIStringsData is `data` of P-03: flat dotted keys -> text (ICU placeholders untouched).
type UIStringsData struct {
	Locale  string            `json:"locale"`
	Strings map[string]string `json:"strings"`
}

// MenuItem is one entry of a menu.
type MenuItem struct {
	ID           int64    `json:"id"`
	Label        string   `json:"label"`
	AriaLabel    *string  `json:"aria_label"`
	Href         *string  `json:"href"`
	Icon         *string  `json:"icon"`
	OpenInNewTab bool     `json:"open_in_new_tab"`
	Rel          []string `json:"rel"`
	ActiveMatch  string   `json:"active_match"`
}

// MenuData is a menu as it sits inside P-05.
type MenuData struct {
	Key   string     `json:"key"`
	Items []MenuItem `json:"items"`
}

// MenuResponse is `data` of P-04.
type MenuResponse struct {
	Locale string     `json:"locale"`
	Key    string     `json:"key"`
	Items  []MenuItem `json:"items"`
}

// SiteMenus is data.menus of P-05.
type SiteMenus struct {
	Main   MenuData `json:"main"`
	Footer MenuData `json:"footer"`
	Social MenuData `json:"social"`
}

// SiteData is `data` of P-05: everything the layout needs.
type SiteData struct {
	Locale   string            `json:"locale"`
	Locales  []LocaleInfo      `json:"locales"`
	Settings Settings          `json:"settings"`
	Menus    SiteMenus         `json:"menus"`
	UI       map[string]string `json:"ui"`
	Media    media.Map         `json:"media"`
}
