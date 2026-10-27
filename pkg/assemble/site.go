package assemble

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/entities"
	"github.com/materm-organization/materm-web-portal-backend/modules/content/readkit"
)

// DateFormats is v_public_locales.date_formats. There is ONE implementation of it (readkit.DateFormats), shared
// with the blog module, so a date reads the same in P-07, P-08 and resolved.posts.
type DateFormats = readkit.DateFormats

// LocaleInfo is one entry of P-01 (and of data.locales of P-05), the shape the frontend's LocaleInfo expects.
type LocaleInfo struct {
	Code         string      `json:"code"`
	Name         string      `json:"name"`
	NativeName   string      `json:"native_name"`
	ShortLabel   string      `json:"short_label"`
	Flag         *string     `json:"flag"`
	IsDefault    bool        `json:"is_default"`
	FallbackCode *string     `json:"fallback_code"`
	DateFormats  DateFormats `json:"date_formats"`
}

// LocaleInfos maps the rows of v_public_locales (already ordered by sort_order, code).
func LocaleInfos(rows []entities.PublicLocale) []LocaleInfo {
	out := make([]LocaleInfo, 0, len(rows))
	for _, r := range rows {
		li := LocaleInfo{Code: r.Code, Name: r.Name, NativeName: r.NativeName, ShortLabel: r.ShortLabel, Flag: r.Flag,
			IsDefault: r.IsDefault, FallbackCode: r.FallbackCode, DateFormats: DateFormats{Months: []string{}}}
		if b := r.DateFormats.Bytes(); len(b) > 0 {
			_ = json.Unmarshal(b, &li.DateFormats) // a malformed value leaves the formats empty: dates fall back to ISO
			if li.DateFormats.Months == nil {
				li.DateFormats.Months = []string{}
			}
		}
		out = append(out, li)
	}
	return out
}

// DateFormatFor returns the date formats of the first locale of chain that has any (the locale itself, its
// fallback, the default).
func DateFormatFor(locales []LocaleInfo, chain []string) DateFormats {
	for _, code := range chain {
		for _, l := range locales {
			if l.Code == code && l.DateFormats.Short != "" {
				return l.DateFormats
			}
		}
	}
	return DateFormats{}
}

// Settings is data.settings of P-02 / P-05: group -> key -> value. A localised setting is already one string
// (the view resolved the language), a media setting is the media id (or null), the rest is the JSON value.
type Settings map[string]map[string]json.RawMessage

// BuildSettings groups the rows of v_public_site_settings and lists the media ids of media settings.
func BuildSettings(rows []entities.PublicSiteSetting) (Settings, []int64) {
	out := Settings{}
	var ids []int64
	for _, r := range rows {
		g := out[r.GroupKey]
		if g == nil {
			g = map[string]json.RawMessage{}
			out[r.GroupKey] = g
		}
		switch {
		case r.ValueType == "media":
			if r.MediaAssetID != nil {
				g[r.Key] = json.RawMessage(strconv.FormatInt(*r.MediaAssetID, 10))
				ids = append(ids, *r.MediaAssetID)
			} else {
				g[r.Key] = json.RawMessage("null")
			}
		case len(r.Value) == 0:
			g[r.Key] = json.RawMessage("null")
		default:
			g[r.Key] = json.RawMessage(r.Value)
		}
	}
	return out, ids
}

// String returns the value of group.key when it is a non-empty JSON string, else "".
func (s Settings) String(group, key string) string {
	raw, ok := s[group][key]
	if !ok {
		return ""
	}
	var v string
	if json.Unmarshal(raw, &v) != nil {
		return ""
	}
	return strings.TrimSpace(v)
}

// MediaID returns the media id of group.key (a media setting), 0 when unset.
func (s Settings) MediaID(group, key string) int64 {
	raw, ok := s[group][key]
	if !ok {
		return 0
	}
	var v int64
	if json.Unmarshal(raw, &v) != nil || v < 1 {
		return 0
	}
	return v
}

// BaseURL is the public site URL without a trailing slash: setting site.base_url, else fallback
// (site.defaultBaseUrl of the configuration).
func (s Settings) BaseURL(fallback string) string {
	b := s.String("site", "base_url")
	if b == "" {
		b = strings.TrimSpace(fallback)
	}
	return strings.TrimRight(b, "/")
}
