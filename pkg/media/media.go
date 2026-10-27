// Package media builds the public media objects of the API (doc/08 sections 2.3.1 and 2.9; OpenAPI
// MediaItem): the entry of data.media for one media_id, with a ready-to-use URL.
//
// URL rules (roadmap AS-3 / 03 P03-9): media_assets.storage_key is the path the file service answered with
// its leading "/" removed, and the public URL is file.path_ip + "/" + storage_key. A key that starts with the
// reserved prefix (media.staticPrefix, default "static/") is a first-day placeholder row: the image is a file
// in portal-frontend's public/ folder, so its URL is the relative "/<rest>". The URL is never stored in the
// database and never contains anything but the key's own path segments (which are escaped).
//
// The package does not import any module: callers give it plain Source values, or implement Lookup.
package media

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Config is the part of the environment this package needs.
type Config struct {
	// PathIP is file.path_ip: the base URL of the file service (http(s)://host[:port][/base]). Blank = no
	// file service configured: only static/ keys get a URL.
	PathIP string
	// StaticPrefix is media.staticPrefix (default "static/").
	StaticPrefix string
}

// Builder composes media URLs. It is immutable and safe for concurrent use.
type Builder struct {
	base   string // PathIP without a trailing slash, "" when unusable
	static string
}

// NewBuilder validates cfg. An unusable PathIP (not http(s) with a host) is treated as blank: media from the
// file service then has no URL and is left out of data.media rather than leaking a broken link.
func NewBuilder(cfg Config) *Builder {
	b := &Builder{static: cfg.StaticPrefix}
	if b.static == "" {
		b.static = "static/"
	}
	if !strings.HasSuffix(b.static, "/") {
		b.static += "/"
	}
	if u, err := url.Parse(strings.TrimSpace(cfg.PathIP)); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" {
		b.base = strings.TrimRight(u.String(), "/")
	}
	return b
}

// URL returns the public URL for a storage key. ok=false when no URL can be formed: the key is empty, absolute,
// contains "." / ".." / empty segments, or it is a file-service key and PathIP is not configured.
func (b *Builder) URL(storageKey string) (string, bool) {
	segs, ok := splitKey(storageKey)
	if !ok {
		return "", false
	}
	if strings.HasPrefix(storageKey, b.static) {
		rest := segs[len(strings.Split(strings.TrimSuffix(b.static, "/"), "/")):]
		if len(rest) == 0 {
			return "", false
		}
		return "/" + escapeJoin(rest), true
	}
	if b.base == "" {
		return "", false
	}
	return b.base + "/" + escapeJoin(segs), true
}

// splitKey checks a storage key and returns its path segments.
func splitKey(key string) ([]string, bool) {
	if key == "" || len(key) > 512 || strings.HasPrefix(key, "/") || strings.ContainsAny(key, "\\?#\x00") || strings.Contains(key, "://") {
		return nil, false
	}
	segs := strings.Split(key, "/")
	for _, s := range segs {
		if s == "" || s == "." || s == ".." {
			return nil, false
		}
	}
	return segs, true
}

func escapeJoin(segs []string) string {
	esc := make([]string, len(segs))
	for i, s := range segs {
		esc[i] = url.PathEscape(s)
	}
	return strings.Join(esc, "/")
}

// Focal is the focal point of an image (0..1 on each axis).
type Focal struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Variant is one rendition of an asset. The file service creates none today, so the list is empty until P2.
type Variant struct {
	Variant string `json:"variant"`
	Format  string `json:"format"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	URL     string `json:"url"`
}

// Item is the entry of data.media (OpenAPI MediaItem).
type Item struct {
	URL           string    `json:"url"`
	Width         *int      `json:"width"`
	Height        *int      `json:"height"`
	Mime          *string   `json:"mime"`
	Alt           *string   `json:"alt"`
	Caption       *string   `json:"caption"`
	Credit        *string   `json:"credit"`
	Blurhash      *string   `json:"blurhash"`
	DominantColor *string   `json:"dominant_color"`
	Focal         *Focal    `json:"focal"`
	Variants      []Variant `json:"variants"`
}

// Source is the input of Item: the columns of v_public_assets as plain values (a nil pointer = NULL).
type Source struct {
	ID            int64
	StorageKey    string
	Mime          string
	Width         *int
	Height        *int
	FocalX        *float64
	FocalY        *float64
	Blurhash      *string
	DominantColor *string
	Alt           *string
	Caption       *string
	Credit        *string
	// Variants is the jsonb of v_public_assets.variants: [{"variant","format","width","height","storage_key"}].
	Variants json.RawMessage
}

type rawVariant struct {
	Variant    string `json:"variant"`
	Format     string `json:"format"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	StorageKey string `json:"storage_key"`
}

// Item builds the data.media entry of one asset. ok=false when the asset has no usable URL; the caller leaves
// it out of the map (the component then shows the colour fill, doc/04 section 6.3). A variant without a URL is
// skipped, never the whole item.
func (b *Builder) Item(s Source) (Item, bool) {
	u, ok := b.URL(s.StorageKey)
	if !ok {
		return Item{}, false
	}
	it := Item{URL: u, Width: s.Width, Height: s.Height, Alt: s.Alt, Caption: s.Caption, Credit: s.Credit,
		Blurhash: s.Blurhash, DominantColor: s.DominantColor, Variants: []Variant{}}
	if s.Mime != "" {
		m := s.Mime
		it.Mime = &m
	}
	if s.FocalX != nil && s.FocalY != nil {
		it.Focal = &Focal{X: *s.FocalX, Y: *s.FocalY}
	}
	if len(s.Variants) > 0 {
		var vs []rawVariant
		if err := json.Unmarshal(s.Variants, &vs); err == nil {
			for _, v := range vs {
				if vu, vok := b.URL(v.StorageKey); vok {
					it.Variants = append(it.Variants, Variant{Variant: v.Variant, Format: v.Format, Width: v.Width, Height: v.Height, URL: vu})
				}
			}
		}
	}
	return it, true
}

// Map is data.media: media_id (as a decimal string) -> Item.
type Map map[string]Item

// Lookup loads assets by id for a locale (the view v_public_assets, which only holds ready, not deleted
// assets). Ids that do not exist are simply absent from the result.
type Lookup interface {
	AssetsByIDs(ctx context.Context, locale string, ids []int64) ([]Source, error)
}

// maxIDs bounds one lookup (a page has far fewer media).
const maxIDs = 500

// Resolve loads the assets for ids and returns the map. Assets without a usable URL are left out. Duplicate
// and non-positive ids are ignored.
func (b *Builder) Resolve(ctx context.Context, l Lookup, locale string, ids []int64) (Map, error) {
	ids = uniquePositive(ids)
	if len(ids) > maxIDs {
		return nil, fmt.Errorf("media: %d ids in one lookup (max %d)", len(ids), maxIDs)
	}
	out := Map{}
	if len(ids) == 0 {
		return out, nil
	}
	srcs, err := l.AssetsByIDs(ctx, locale, ids)
	if err != nil {
		return nil, err
	}
	for _, s := range srcs {
		if it, ok := b.Item(s); ok {
			out[strconv.FormatInt(s.ID, 10)] = it
		}
	}
	return out, nil
}

func uniquePositive(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id < 1 {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// CollectIDs walks any decoded JSON value (props, resolved data, seo, rich text) and returns every integer
// found under a key named "media_id", sorted ascending and without duplicates (doc/04 section 3.1 (c): the
// key is reserved for media_assets.id at any depth). Both json.Number and float64 are understood.
func CollectIDs(v any) []int64 {
	var ids []int64
	seen := map[int64]struct{}{}
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case map[string]any:
			for k, c := range t {
				if k == "media_id" {
					if id, ok := toID(c); ok {
						if _, dup := seen[id]; !dup {
							seen[id] = struct{}{}
							ids = append(ids, id)
						}
					}
					continue
				}
				walk(c)
			}
		case []any:
			for _, c := range t {
				walk(c)
			}
		}
	}
	walk(v)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func toID(v any) (int64, bool) {
	switch n := v.(type) {
	case json.Number:
		id, err := n.Int64()
		return id, err == nil && id >= 1
	case float64:
		id := int64(n)
		return id, float64(id) == n && id >= 1
	case int64:
		return n, n >= 1
	case int:
		return int64(n), n >= 1
	}
	return 0, false
}
