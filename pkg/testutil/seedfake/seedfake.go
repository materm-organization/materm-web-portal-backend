// Package seedfake is an in-memory twin of the public views of the CMS database, filled from the raw seed of
// materm-web-portal-frontend (src/content/cms-seed/source + exports): the very inputs of the frontend's own
// generator (scripts/cms/build-seed.mjs), whose output is the set of bodies the frontend was built against.
//
// Set returns a *repositories.Set (the read layer of modules/content) whose repositories answer from memory
// with the row-level fallback chain of the SQL views (requested -> fallback_code -> default). Tests wire the
// real services, controllers and routes over it: no database, no Redis, and the answers can be compared
// with the bundled bodies (src/content/cms-seed/data).
//
// It is test support: nothing in production imports it.
package seedfake

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/entities"
	"github.com/materm-organization/materm-web-portal-backend/modules/content/repositories"
)

// DB holds the parsed seed.
type DB struct {
	// Dir is materm-web-portal-frontend/src/content/cms-seed.
	Dir string

	ref      refData
	pages    []pageSrc
	posts    []postSrc
	albums   []albumSrc
	manifest []manifestItem
	menus    map[string]menuSrc
	ui       uiSrc
	locales  []localeSrc
}

type localeSrc struct {
	Code         string          `json:"code"`
	Name         string          `json:"name"`
	NativeName   string          `json:"native_name"`
	ShortLabel   string          `json:"short_label"`
	Flag         *string         `json:"flag"`
	IsDefault    bool            `json:"is_default"`
	IsActive     bool            `json:"is_active"`
	FallbackCode *string         `json:"fallback_code"`
	DateFormats  json.RawMessage `json:"date_formats"`
	SortOrder    int             `json:"sort_order"`
}

type refData struct {
	Locales    []localeSrc  `json:"locales"`
	Settings   []settingSrc `json:"settings"`
	Categories []struct {
		Key          string `json:"key"`
		SortOrder    int    `json:"sort_order"`
		Translations map[string]struct {
			Name string `json:"name"`
			Slug string `json:"slug"`
		} `json:"translations"`
	} `json:"categories"`
	FAQGroups []struct {
		Key   string `json:"key"`
		Items []struct {
			ID           int64 `json:"id"`
			SortOrder    int   `json:"sort_order"`
			Translations map[string]struct {
				Question string `json:"question"`
				Answer   string `json:"answer"`
			} `json:"translations"`
		} `json:"items"`
	} `json:"faq_groups"`
	Forms []formSrc `json:"forms"`
}

type settingSrc struct {
	Group     string          `json:"group"`
	Key       string          `json:"key"`
	ValueType string          `json:"value_type"`
	Localized bool            `json:"localized"`
	Value     json.RawMessage `json:"value"`
}

type formSrc struct {
	Slug               string          `json:"slug"`
	HoneypotField      string          `json:"honeypot_field"`
	FieldOrder         []string        `json:"field_order"`
	JSONSchema         json.RawMessage `json:"json_schema"`
	SuccessRedirectURL *string         `json:"success_redirect_url"`
	Translations       map[string]struct {
		Title              string          `json:"title"`
		Description        string          `json:"description"`
		SubmitLabel        string          `json:"submit_label"`
		SendingLabel       string          `json:"sending_label"`
		SuccessTitle       string          `json:"success_title"`
		SuccessDescription string          `json:"success_description"`
		SuccessAgainLabel  string          `json:"success_again_label"`
		Messages           json.RawMessage `json:"messages"`
	} `json:"translations"`
	FieldTranslations map[string]map[string]struct {
		Label        string          `json:"label"`
		Placeholder  *string         `json:"placeholder"`
		OptionLabels json.RawMessage `json:"option_labels"`
	} `json:"field_translations"`
}

type pageSrc struct {
	Page struct {
		ID        int64  `json:"id"`
		Key       string `json:"key"`
		Template  string `json:"template"`
		Path      string `json:"path"`
		IsHome    bool   `json:"is_home"`
		Noindex   bool   `json:"noindex"`
		InSitemap bool   `json:"in_sitemap"`
		Status    string `json:"status"`
	} `json:"page"`
	Translations []struct {
		LocaleCode       string  `json:"locale_code"`
		Title            string  `json:"title"`
		SEOTitle         *string `json:"seo_title"`
		SEOTitleAbsolute bool    `json:"seo_title_absolute"`
		SEODescription   *string `json:"seo_description"`
		OGImageID        *int64  `json:"og_image_id"`
		CanonicalPath    *string `json:"canonical_path"`
		IsEnabled        bool    `json:"is_enabled"`
	} `json:"translations"`
	Sections []struct {
		SectionType    string          `json:"section_type"`
		SectionVersion int             `json:"section_version"`
		Variant        *string         `json:"variant"`
		SortOrder      int             `json:"sort_order"`
		IsVisible      bool            `json:"is_visible"`
		AnchorID       *string         `json:"anchor_id"`
		Props          json.RawMessage `json:"props"`
	} `json:"sections"`
}

type postSrc struct {
	Post struct {
		ID            int64  `json:"id"`
		CategoryID    int64  `json:"category_id"`
		HeroMediaID   int64  `json:"hero_media_id"`
		TeaserMediaID *int64 `json:"teaser_media_id"`
		IsFeatured    bool   `json:"is_featured"`
		Status        string `json:"status"`
		PublishedAt   string `json:"published_at"`
	} `json:"post"`
	Translations []struct {
		LocaleCode    string  `json:"locale_code"`
		Slug          string  `json:"slug"`
		Title         string  `json:"title"`
		Excerpt       *string `json:"excerpt"`
		Label         *string `json:"label"`
		LocationLabel *string `json:"location_label"`
		IsEnabled     *bool   `json:"is_enabled"`
	} `json:"translations"`
}

type albumSrc struct {
	Album struct {
		ID           int64  `json:"id"`
		Key          string `json:"key"`
		CoverMediaID int64  `json:"cover_media_id"`
		Status       string `json:"status"`
	} `json:"album"`
	Translations []struct {
		LocaleCode  string  `json:"locale_code"`
		Title       string  `json:"title"`
		Description *string `json:"description"`
	} `json:"translations"`
	Items []struct {
		ID           int64 `json:"id"`
		MediaID      int64 `json:"media_id"`
		SortOrder    int   `json:"sort_order"`
		Translations []struct {
			LocaleCode         string  `json:"locale_code"`
			CaptionTitle       *string `json:"caption_title"`
			CaptionDescription *string `json:"caption_description"`
			Alt                *string `json:"alt"`
		} `json:"translations"`
	} `json:"items"`
}

type manifestItem struct {
	MediaID    int64             `json:"media_id"`
	StorageKey string            `json:"storage_key"`
	Kind       string            `json:"kind"`
	MimeType   string            `json:"mime_type"`
	Width      *int              `json:"width"`
	Height     *int              `json:"height"`
	Alt        map[string]string `json:"alt"`
}

type menuSrc struct {
	Key   string `json:"key"`
	Items []struct {
		SortOrder    int     `json:"sort_order"`
		LinkType     string  `json:"link_type"`
		PageID       *int64  `json:"page_id"`
		URL          *string `json:"url"`
		Icon         *string `json:"icon"`
		OpenInNewTab bool    `json:"open_in_new_tab"`
		ActiveMatch  string  `json:"active_match"`
		IsVisible    bool    `json:"is_visible"`
		Translations []struct {
			LocaleCode string  `json:"locale_code"`
			Label      string  `json:"label"`
			AriaLabel  *string `json:"aria_label"`
		} `json:"translations"`
	} `json:"items"`
}

type uiSrc struct {
	Keys []struct {
		Key       string            `json:"key"`
		Namespace string            `json:"namespace"`
		Values    map[string]string `json:"values"`
	} `json:"keys"`
}

// PageKeys are the five known pages of the seed.
var PageKeys = []string{"home", "our-path", "blog", "gallery", "contact"}

// Dir finds materm-web-portal-frontend/src/content/cms-seed next to the repository ("" when absent).
func Dir() string {
	wd, _ := os.Getwd()
	for dir := wd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			d := filepath.Join(filepath.Dir(dir), "materm-web-portal-frontend", "src", "content", "cms-seed")
			if _, err := os.Stat(filepath.Join(d, "data", "manifest.json")); err == nil {
				return d
			}
			return ""
		}
		if dir == filepath.Dir(dir) {
			return ""
		}
	}
}

// ReadJSON reads a JSON file or fails the test.
func ReadJSON(t testing.TB, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // test support: a fixed path under the sibling repository
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// Load parses the seed. It skips the test when the frontend repository is not next to this one.
func Load(t testing.TB) *DB {
	t.Helper()
	dir := Dir()
	if dir == "" {
		t.Skip("materm-web-portal-frontend/src/content/cms-seed is not next to this repository: golden comparison skipped")
	}
	d := &DB{Dir: dir, menus: map[string]menuSrc{}}
	ReadJSON(t, filepath.Join(dir, "source", "reference.json"), &d.ref)
	for _, l := range d.ref.Locales {
		if l.IsActive {
			d.locales = append(d.locales, l)
		}
	}
	sort.Slice(d.locales, func(i, j int) bool { return d.locales[i].SortOrder < d.locales[j].SortOrder })
	for _, k := range PageKeys {
		var p pageSrc
		ReadJSON(t, filepath.Join(dir, "source", "page."+k+".json"), &p)
		d.pages = append(d.pages, p)
	}
	var post postSrc
	ReadJSON(t, filepath.Join(dir, "source", "post.music-night-under-the-stars.json"), &post)
	d.posts = []postSrc{post}
	var album albumSrc
	ReadJSON(t, filepath.Join(dir, "source", "album.forest-photo-story.json"), &album)
	d.albums = []albumSrc{album}
	ReadJSON(t, filepath.Join(dir, "exports", "media-manifest.json"), &d.manifest)
	ReadJSON(t, filepath.Join(dir, "source", "menus.json"), &d.menus)
	ReadJSON(t, filepath.Join(dir, "exports", "ui-strings.json"), &d.ui)
	return d
}

// DefaultLocale is the code of the default locale.
func (d *DB) DefaultLocale() string {
	for _, l := range d.locales {
		if l.IsDefault {
			return l.Code
		}
	}
	return ""
}

// Chain is the fallback chain of a locale: itself, its fallback_code, the default.
func (d *DB) Chain(code string) []string {
	byCode := map[string]localeSrc{}
	for _, l := range d.locales {
		byCode[l.Code] = l
	}
	cur, ok := byCode[code]
	if !ok {
		cur = byCode[d.DefaultLocale()]
	}
	var out []string
	for {
		for _, c := range out {
			if c == cur.Code {
				return d.withDefault(out)
			}
		}
		out = append(out, cur.Code)
		if cur.FallbackCode == nil {
			break
		}
		cur = byCode[*cur.FallbackCode]
	}
	return d.withDefault(out)
}

func (d *DB) withDefault(out []string) []string {
	for _, c := range out {
		if c == d.DefaultLocale() {
			return out
		}
	}
	return append(out, d.DefaultLocale())
}

// pid is a deterministic UUID-shaped id (the golden comparison masks public ids).
func pid(label string) string {
	var h uint64 = 1469598103934665603
	for i := 0; i < len(label); i++ {
		h ^= uint64(label[i])
		h *= 1099511628211
	}
	return fmt.Sprintf("00000000-0000-7000-8000-%012x", h&0xffffffffffff)
}

// Set returns the repositories over the seed.
func (d *DB) Set() *repositories.Set {
	return &repositories.Set{
		Locales: localeRepo{d}, UIStrings: uiRepo{d}, Settings: settingRepo{d}, Menus: menuRepo{d}, Pages: pageRepo{d},
		Taxonomy: taxRepo{d}, Posts: postRepo{d}, Albums: albumRepo{d}, FAQ: faqRepo{d}, Forms: formRepo{d},
		Collections: collRepo{}, SEO: seoRepo{d}, Assets: assetRepo{d},
	}
}

// ---- locales / ui strings / settings ---------------------------------------------------------------------------------

type localeRepo struct{ d *DB }

func (r localeRepo) ListActive(context.Context) ([]entities.PublicLocale, error) {
	var out []entities.PublicLocale
	for _, l := range r.d.locales {
		out = append(out, entities.PublicLocale{Code: l.Code, Name: l.Name, NativeName: l.NativeName, ShortLabel: l.ShortLabel, Flag: l.Flag,
			TextDirection: "ltr", IsDefault: l.IsDefault, FallbackCode: l.FallbackCode, DateFormats: entities.JSONB(l.DateFormats), SortOrder: l.SortOrder})
	}
	return out, nil
}

type uiRepo struct{ d *DB }

func (r uiRepo) List(_ context.Context, locale string, namespaces []string) ([]entities.PublicUIString, error) {
	var out []entities.PublicUIString
	for _, k := range r.d.ui.Keys {
		if len(namespaces) > 0 && !contains(namespaces, k.Namespace) {
			continue
		}
		for _, code := range r.d.Chain(locale) {
			if v, ok := k.Values[code]; ok {
				ns := k.Namespace
				out = append(out, entities.PublicUIString{RequestedLocale: locale, Key: k.Key, Namespace: &ns, Value: v, ResolvedLocale: code})
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

type settingRepo struct{ d *DB }

func (r settingRepo) List(_ context.Context, locale string, groups []string) ([]entities.PublicSiteSetting, error) {
	var out []entities.PublicSiteSetting
	for _, s := range r.d.ref.Settings {
		if len(groups) > 0 && !contains(groups, s.Group) {
			continue
		}
		row := entities.PublicSiteSetting{RequestedLocale: locale, GroupKey: s.Group, Key: s.Key, ValueType: s.ValueType}
		switch {
		case s.ValueType == "media":
			var id *int64
			_ = json.Unmarshal(s.Value, &id)
			row.MediaAssetID = id
		case s.Localized:
			var m map[string]string
			_ = json.Unmarshal(s.Value, &m)
			var picked *string
			for _, c := range r.d.Chain(locale) {
				if v, ok := m[c]; ok && v != "" {
					vv := v
					picked = &vv
					break
				}
			}
			if picked == nil {
				continue
			}
			b, _ := json.Marshal(*picked)
			row.Value = b
		default:
			if string(s.Value) != "null" {
				row.Value = entities.JSONB(s.Value)
			}
		}
		out = append(out, row)
	}
	return out, nil
}

// ---- menus -------------------------------------------------------------------------------------------------------

type menuRepo struct{ d *DB }

func (r menuRepo) Items(_ context.Context, locale, key string) ([]entities.PublishedMenuItem, error) {
	m, ok := r.d.menus[key]
	if !ok {
		return []entities.PublishedMenuItem{}, nil
	}
	out := []entities.PublishedMenuItem{}
	for _, it := range m.Items {
		if !it.IsVisible {
			continue
		}
		var href *string
		if it.LinkType == "page" {
			if it.PageID == nil {
				continue
			}
			row, ok := r.d.pageByID(locale, *it.PageID)
			if !ok {
				continue // a page link to an unpublished page is hidden
			}
			p := row.Path
			href = &p
		} else {
			href = it.URL
		}
		item := entities.PublishedMenuItem{RequestedLocale: locale, MenuKey: key, ItemID: int64(it.SortOrder), SortOrder: it.SortOrder,
			LinkType: it.LinkType, Href: href, PageID: it.PageID, OpenInNewTab: it.OpenInNewTab, Icon: it.Icon, ActiveMatch: it.ActiveMatch}
		found := false
		for _, code := range r.d.Chain(locale) {
			for _, tr := range it.Translations {
				if tr.LocaleCode == code {
					item.Label, item.AriaLabel, item.ResolvedLocale = tr.Label, tr.AriaLabel, code
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			continue
		}
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].SortOrder < out[j].SortOrder })
	return out, nil
}

// ---- pages -------------------------------------------------------------------------------------------------------

type pageRepo struct{ d *DB }

func (d *DB) pageRow(locale string, p pageSrc) (entities.PublishedPage, bool) {
	if p.Page.Status != "published" {
		return entities.PublishedPage{}, false
	}
	for _, code := range d.Chain(locale) {
		for _, t := range p.Translations {
			if t.LocaleCode == code && t.IsEnabled {
				return entities.PublishedPage{
					RequestedLocale: locale, PageID: p.Page.ID, PublicID: pid(fmt.Sprintf("page:%d", p.Page.ID)), Key: p.Page.Key,
					Template: p.Page.Template, IsHome: p.Page.IsHome, Noindex: p.Page.Noindex, InSitemap: p.Page.InSitemap,
					UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Path: p.Page.Path, ResolvedLocale: code, IsFallback: code != locale,
					Title: t.Title, SEOTitle: t.SEOTitle, SEOTitleAbsolute: t.SEOTitleAbsolute, SEODescription: t.SEODescription,
					OGImageID: t.OGImageID, CanonicalPath: t.CanonicalPath,
				}, true
			}
		}
	}
	return entities.PublishedPage{}, false
}

func (d *DB) pageByID(locale string, id int64) (entities.PublishedPage, bool) {
	for _, p := range d.pages {
		if p.Page.ID == id {
			return d.pageRow(locale, p)
		}
	}
	return entities.PublishedPage{}, false
}

func (r pageRepo) FindByPath(_ context.Context, locale, path string) (*entities.PublishedPage, error) {
	for _, p := range r.d.pages {
		if p.Page.Path == path {
			if row, ok := r.d.pageRow(locale, p); ok {
				return &row, nil
			}
		}
	}
	return nil, repositories.ErrNotFound
}

func (r pageRepo) FindByKey(_ context.Context, locale, key string) (*entities.PublishedPage, error) {
	for _, p := range r.d.pages {
		if p.Page.Key == key {
			if row, ok := r.d.pageRow(locale, p); ok {
				return &row, nil
			}
		}
	}
	return nil, repositories.ErrNotFound
}

func (r pageRepo) ByIDs(_ context.Context, locale string, ids []int64) ([]entities.PublishedPage, error) {
	out := []entities.PublishedPage{}
	for _, id := range ids {
		if row, ok := r.d.pageByID(locale, id); ok {
			out = append(out, row)
		}
	}
	return out, nil
}

func (r pageRepo) Sections(_ context.Context, pageID int64) ([]entities.PublishedPageSection, error) {
	for _, p := range r.d.pages {
		if p.Page.ID != pageID || p.Page.Status != "published" {
			continue
		}
		var out []entities.PublishedPageSection
		for i, s := range p.Sections {
			if !s.IsVisible {
				continue
			}
			out = append(out, entities.PublishedPageSection{
				PageID: pageID, SectionID: int64(i + 1), PublicID: pid(fmt.Sprintf("section:%s:%d", p.Page.Key, i+1)),
				SectionKey: s.SectionType, SectionVersion: s.SectionVersion, Variant: s.Variant, SortOrder: s.SortOrder,
				AnchorID: s.AnchorID, Props: entities.JSONB(s.Props),
			})
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].SortOrder < out[j].SortOrder })
		return out, nil
	}
	return []entities.PublishedPageSection{}, nil
}

// ---- taxonomy / posts ----------------------------------------------------------------------------------------------

type taxRepo struct{ d *DB }

func (r taxRepo) Categories(_ context.Context, locale string) ([]entities.PublishedCategory, error) {
	out := []entities.PublishedCategory{}
	for i, c := range r.d.ref.Categories {
		for _, code := range r.d.Chain(locale) {
			if tr, ok := c.Translations[code]; ok {
				out = append(out, entities.PublishedCategory{RequestedLocale: locale, CategoryID: int64(i + 1), Key: c.Key, SortOrder: c.SortOrder,
					Name: tr.Name, Slug: tr.Slug, ResolvedLocale: code})
				break
			}
		}
	}
	return out, nil
}

func (r taxRepo) CategoryByKey(ctx context.Context, locale, key string) (*entities.PublishedCategory, error) {
	cats, _ := r.Categories(ctx, locale)
	for i := range cats {
		if cats[i].Key == key {
			return &cats[i], nil
		}
	}
	return nil, repositories.ErrNotFound
}

func (r taxRepo) Tags(context.Context, string) ([]entities.PublishedTag, error) {
	return []entities.PublishedTag{}, nil
}

type postRepo struct{ d *DB }

func (d *DB) postCard(locale string, po postSrc) (entities.PublishedPostCard, bool) {
	if po.Post.Status != "published" {
		return entities.PublishedPostCard{}, false
	}
	for _, code := range d.Chain(locale) {
		for _, t := range po.Translations {
			if t.LocaleCode != code || (t.IsEnabled != nil && !*t.IsEnabled) {
				continue
			}
			pub, _ := time.Parse(time.RFC3339, po.Post.PublishedAt)
			card := po.Post.HeroMediaID
			if po.Post.TeaserMediaID != nil {
				card = *po.Post.TeaserMediaID
			}
			cat := po.Post.CategoryID
			hero := po.Post.HeroMediaID
			return entities.PublishedPostCard{
				RequestedLocale: locale, PostID: po.Post.ID, PublicID: pid(fmt.Sprintf("post:%d", po.Post.ID)), CategoryID: &cat,
				HeroMediaID: &hero, TeaserMediaID: po.Post.TeaserMediaID, CardMediaID: &card, IsFeatured: po.Post.IsFeatured,
				PublishedAt: &pub, UpdatedAt: pub, ResolvedLocale: code, IsFallback: code != locale,
				Slug: t.Slug, Title: t.Title, Excerpt: t.Excerpt, Label: t.Label, LocationLabel: t.LocationLabel,
			}, true
		}
	}
	return entities.PublishedPostCard{}, false
}

func (r postRepo) ByIDs(_ context.Context, locale string, ids []int64) ([]entities.PublishedPostCard, error) {
	out := []entities.PublishedPostCard{}
	for _, po := range r.d.posts {
		if !containsID(ids, po.Post.ID) {
			continue
		}
		if c, ok := r.d.postCard(locale, po); ok {
			out = append(out, c)
		}
	}
	return out, nil
}

func containsID(ids []int64, id int64) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func (r postRepo) List(_ context.Context, f repositories.PostFilter, limit, offset int) ([]entities.PublishedPostCard, int64, error) {
	var all []entities.PublishedPostCard
	for _, po := range r.d.posts {
		c, ok := r.d.postCard(f.Locale, po)
		if !ok || (f.CategoryID != nil && *c.CategoryID != *f.CategoryID) {
			continue
		}
		all = append(all, c)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].PublishedAt.After(*all[j].PublishedAt) })
	total := int64(len(all))
	if offset > len(all) {
		offset = len(all)
	}
	all = all[offset:]
	if limit > 0 && limit < len(all) {
		all = all[:limit]
	}
	if all == nil {
		all = []entities.PublishedPostCard{}
	}
	return all, total, nil
}

func (r postRepo) FindBySlug(_ context.Context, locale, slug string) (*entities.PublishedPost, error) {
	for _, po := range r.d.posts {
		if c, ok := r.d.postCard(locale, po); ok && c.Slug == slug {
			return &entities.PublishedPost{PublishedPostCard: c}, nil
		}
	}
	return nil, repositories.ErrNotFound
}

func (r postRepo) FindByID(_ context.Context, locale string, id int64) (*entities.PublishedPost, error) {
	for _, po := range r.d.posts {
		if po.Post.ID == id {
			if c, ok := r.d.postCard(locale, po); ok {
				return &entities.PublishedPost{PublishedPostCard: c}, nil
			}
		}
	}
	return nil, repositories.ErrNotFound
}

func (r postRepo) FindSlugOwner(_ context.Context, slug, _ string) (*entities.PublishedPostSlug, error) {
	for _, po := range r.d.posts {
		for _, t := range po.Translations {
			if t.Slug == slug {
				return &entities.PublishedPostSlug{PostID: po.Post.ID, LocaleCode: t.LocaleCode, Slug: slug}, nil
			}
		}
	}
	return nil, repositories.ErrNotFound
}

func (r postRepo) Media(context.Context, string, int64) ([]entities.PublishedPostMedia, error) {
	return []entities.PublishedPostMedia{}, nil
}

func (r postRepo) Tags(context.Context, string, []int64) ([]entities.PublishedPostTag, error) {
	return []entities.PublishedPostTag{}, nil
}

// ---- albums / faq / forms -------------------------------------------------------------------------------------------

type albumRepo struct{ d *DB }

func (r albumRepo) head(locale string, a albumSrc) (entities.PublishedAlbum, bool) {
	if a.Album.Status != "published" {
		return entities.PublishedAlbum{}, false
	}
	for _, code := range r.d.Chain(locale) {
		for _, t := range a.Translations {
			if t.LocaleCode == code {
				cover := a.Album.CoverMediaID
				return entities.PublishedAlbum{RequestedLocale: locale, AlbumID: a.Album.ID, PublicID: pid(fmt.Sprintf("album:%d", a.Album.ID)),
					Key: a.Album.Key, CoverMediaID: &cover, Title: t.Title, Description: t.Description, ResolvedLocale: code}, true
			}
		}
	}
	return entities.PublishedAlbum{}, false
}

func (r albumRepo) List(_ context.Context, locale string) ([]entities.PublishedAlbum, error) {
	out := []entities.PublishedAlbum{}
	for _, a := range r.d.albums {
		if h, ok := r.head(locale, a); ok {
			out = append(out, h)
		}
	}
	return out, nil
}

func (r albumRepo) FindByKey(_ context.Context, locale, key string) (*entities.PublishedAlbum, error) {
	for _, a := range r.d.albums {
		if a.Album.Key == key {
			if h, ok := r.head(locale, a); ok {
				return &h, nil
			}
		}
	}
	return nil, repositories.ErrNotFound
}

func (r albumRepo) Items(_ context.Context, locale string, albumID int64, limit int) ([]entities.PublishedAlbumItem, error) {
	out := []entities.PublishedAlbumItem{}
	for _, a := range r.d.albums {
		if a.Album.ID != albumID {
			continue
		}
		if _, ok := r.head(locale, a); !ok {
			continue
		}
		items := append(a.Items[:0:0], a.Items...)
		sort.SliceStable(items, func(i, j int) bool { return items[i].SortOrder < items[j].SortOrder })
		for _, it := range items {
			row := entities.PublishedAlbumItem{RequestedLocale: locale, AlbumID: albumID, AlbumItemID: it.ID, PublicID: pid(fmt.Sprintf("item:%d", it.ID)),
				MediaID: it.MediaID, SortOrder: it.SortOrder}
			found := false
			for _, code := range r.d.Chain(locale) {
				for _, tr := range it.Translations {
					if tr.LocaleCode == code {
						row.CaptionTitle, row.CaptionDescription, row.Alt = tr.CaptionTitle, tr.CaptionDescription, tr.Alt
						found = true
						break
					}
				}
				if found {
					break
				}
			}
			out = append(out, row)
		}
	}
	if limit > 0 && limit < len(out) {
		out = out[:limit]
	}
	return out, nil
}

type faqRepo struct{ d *DB }

func (r faqRepo) Items(_ context.Context, locale, group string) ([]entities.PublishedFAQItem, error) {
	out := []entities.PublishedFAQItem{}
	for gi, g := range r.d.ref.FAQGroups {
		if g.Key != group {
			continue
		}
		for _, it := range g.Items {
			for _, code := range r.d.Chain(locale) {
				if tr, ok := it.Translations[code]; ok {
					doc, _ := json.Marshal(map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph",
						"content": []any{map[string]any{"type": "text", "text": tr.Answer}}}}})
					out = append(out, entities.PublishedFAQItem{RequestedLocale: locale, GroupKey: g.Key, GroupID: int64(gi + 1), FAQItemID: it.ID,
						SortOrder: it.SortOrder, Question: tr.Question, Answer: doc, ResolvedLocale: code})
					break
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].SortOrder < out[j].SortOrder })
	return out, nil
}

type formRepo struct{ d *DB }

func (r formRepo) Find(_ context.Context, locale, slug string) (*entities.PublishedForm, error) {
	for i, fm := range r.d.ref.Forms {
		if fm.Slug != slug {
			continue
		}
		for _, code := range r.d.Chain(locale) {
			tr, ok := fm.Translations[code]
			if !ok {
				continue
			}
			ho := fm.HoneypotField
			return &entities.PublishedForm{
				RequestedLocale: locale, FormID: int64(i + 1), Slug: slug, JSONSchema: entities.JSONB(fm.JSONSchema),
				FieldOrder: fm.FieldOrder, HoneypotField: &ho, SuccessRedirectURL: fm.SuccessRedirectURL,
				Title: tr.Title, Description: &tr.Description, SubmitLabel: tr.SubmitLabel, SendingLabel: &tr.SendingLabel,
				SuccessTitle: &tr.SuccessTitle, SuccessDescription: &tr.SuccessDescription, SuccessAgainLabel: &tr.SuccessAgainLabel,
				Messages: entities.JSONB(tr.Messages), ResolvedLocale: code,
			}, nil
		}
	}
	return nil, repositories.ErrNotFound
}

func (r formRepo) Fields(_ context.Context, locale, slug string) ([]entities.PublishedFormField, error) {
	out := []entities.PublishedFormField{}
	for _, fm := range r.d.ref.Forms {
		if fm.Slug != slug {
			continue
		}
		for _, key := range fm.FieldOrder {
			for _, code := range r.d.Chain(locale) {
				if tr, ok := fm.FieldTranslations[key][code]; ok {
					out = append(out, entities.PublishedFormField{RequestedLocale: locale, FormSlug: slug, FieldKey: key, Label: tr.Label,
						Placeholder: tr.Placeholder, OptionLabels: entities.JSONB(tr.OptionLabels), ResolvedLocale: code})
					break
				}
			}
		}
	}
	return out, nil
}

type collRepo struct{}

func (collRepo) Entries(context.Context, string, string, int, int) ([]entities.PublishedCollectionEntry, int64, error) {
	return []entities.PublishedCollectionEntry{}, 0, nil
}

// ---- seo / assets -----------------------------------------------------------------------------------------------------

type seoRepo struct{ d *DB }

func (r seoRepo) Sitemap(_ context.Context, locale string) ([]entities.SitemapEntry, error) {
	out := []entities.SitemapEntry{}
	blogPath := ""
	for _, p := range r.d.pages {
		row, ok := r.d.pageRow(locale, p)
		if !ok {
			continue
		}
		if p.Page.Key == "blog" {
			blogPath = row.Path
		}
		if !row.Noindex && row.InSitemap {
			lm := row.UpdatedAt
			out = append(out, entities.SitemapEntry{RequestedLocale: locale, Kind: "page", EntityID: row.PageID, Path: row.Path, Lastmod: &lm})
		}
	}
	for _, po := range r.d.posts {
		if c, ok := r.d.postCard(locale, po); ok && blogPath != "" {
			lm := c.UpdatedAt
			out = append(out, entities.SitemapEntry{RequestedLocale: locale, Kind: "post", EntityID: c.PostID, Path: blogPath + "/" + c.Slug, Lastmod: &lm})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

func (r seoRepo) Redirect(context.Context, string, string) (*entities.ActiveRedirect, error) {
	return nil, repositories.ErrNotFound
}

type assetRepo struct{ d *DB }

func (r assetRepo) ByIDs(_ context.Context, locale string, ids []int64) ([]entities.PublicAsset, error) {
	out := []entities.PublicAsset{}
	for _, it := range r.d.manifest {
		if !containsID(ids, it.MediaID) {
			continue
		}
		var alt *string
		for _, c := range r.d.Chain(locale) {
			if v := it.Alt[c]; strings.TrimSpace(v) != "" {
				vv := v
				alt = &vv
				break
			}
		}
		out = append(out, entities.PublicAsset{RequestedLocale: locale, AssetID: it.MediaID, PublicID: pid(fmt.Sprintf("asset:%d", it.MediaID)),
			Kind: it.Kind, StorageKey: it.StorageKey, MimeType: it.MimeType, Width: it.Width, Height: it.Height, Alt: alt, Variants: entities.JSONB("[]")})
	}
	return out, nil
}
