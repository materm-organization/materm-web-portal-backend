// Package assemble builds the page response of the public API (doc/08 section 2.4). Given a published page and
// its sections as the database views hold them (props = raw locale maps), it produces the body of
// GET /public/pages/resolve: the props collapsed to one language, links turned into hrefs, rich text
// sanitised, the `resolved` data each section type needs (blog posts, gallery cards, photo slots, contact
// info and form, FAQ items, categories) and the media map.
//
// The walk over props is a port of the reference implementation in materm-web-portal-frontend
// (scripts/cms/build-seed.mjs), which generated the bundled bodies the frontend was built against:
//
//   - locale maps are collapsed per leaf by pkg/localize (requested -> fallback -> default; "" is missing)
//     driven by hints compiled from the section schemas (`x-localized`);
//   - an image placement is always {media_id, alt, decorative}; a link is {href, open_in_new_tab, rel} and an
//     object that holds a link absorbs it (`cta {label, href, ...}`);
//   - rich text goes through pkg/richtext with server-side link resolution;
//   - one resolver per section type key adds `resolved`; a resolver may answer "skip this section" when the
//     entity it refers to is not published.
//
// A section whose key@version is not registered is skipped and counted (Assembler.Skipped), never an error: a
// newer admin must not break an older portal.
//
// The same assembler serves the public endpoint and preview (T1.4.5): both build PageInput; only the source of
// the rows differs (the views, or a frozen snapshot through raw.go).
package assemble

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/entities"
	"github.com/materm-organization/materm-web-portal-backend/modules/content/readkit"
	"github.com/materm-organization/materm-web-portal-backend/modules/content/repositories"
	"github.com/materm-organization/materm-web-portal-backend/pkg/localize"
	"github.com/materm-organization/materm-web-portal-backend/pkg/logs"
	"github.com/materm-organization/materm-web-portal-backend/pkg/media"
)

// The narrow views of the repositories the assembler reads (modules/content/repositories satisfies them).
type (
	// LocaleSource lists the active locales (date formats).
	LocaleSource interface {
		ListActive(ctx context.Context) ([]entities.PublicLocale, error)
	}
	// SettingSource reads v_public_site_settings.
	SettingSource interface {
		List(ctx context.Context, locale string, groups []string) ([]entities.PublicSiteSetting, error)
	}
	// PostSource reads v_published_posts cards.
	PostSource interface {
		PostLookup
		List(ctx context.Context, f repositories.PostFilter, limit, offset int) ([]entities.PublishedPostCard, int64, error)
	}
	// TaxonomySource reads v_published_categories.
	TaxonomySource interface {
		Categories(ctx context.Context, locale string) ([]entities.PublishedCategory, error)
	}
	// AlbumSource reads v_published_album_items.
	AlbumSource interface {
		Items(ctx context.Context, locale string, albumID int64, limit int) ([]entities.PublishedAlbumItem, error)
	}
	// FAQSource reads v_published_faq_items.
	FAQSource interface {
		Items(ctx context.Context, locale, groupKey string) ([]entities.PublishedFAQItem, error)
	}
	// FormSource reads v_published_forms and v_published_form_fields.
	FormSource interface {
		Find(ctx context.Context, locale, slug string) (*entities.PublishedForm, error)
		Fields(ctx context.Context, locale, slug string) ([]entities.PublishedFormField, error)
	}
)

// SharedSource reads the PUBLISHED content of shared sections (modules/content PageRepository.SharedSections).
type SharedSource interface {
	SharedSections(ctx context.Context, ids []int64) ([]entities.PublishedPageSection, error)
}

// Deps is everything the assembler reads. Every field except Registry is required.
type Deps struct {
	Locales  LocaleSource
	Pages    PageLookup
	Posts    PostSource
	Taxonomy TaxonomySource
	Albums   AlbumSource
	FAQ      FAQSource
	Forms    FormSource
	Settings SettingSource

	// Media builds URLs and MediaLookup loads the assets of data.media.
	Media       *media.Builder
	MediaLookup media.Lookup

	// Shared reads the content of shared sections for preview (a snapshot keeps only the reference). Optional:
	// without it a preview leaves shared sections out with a warning.
	Shared SharedSource

	// DefaultBaseURL is site.defaultBaseUrl: used when the setting site.base_url is blank.
	DefaultBaseURL string
	// Registry defaults to MustRegistry().
	Registry *Registry
}

// Assembler builds page responses. It is safe for concurrent use.
type Assembler struct {
	d       Deps
	reg     *Registry
	skipped atomic.Uint64
}

// New builds an Assembler.
func New(d Deps) *Assembler {
	a := &Assembler{d: d, reg: d.Registry}
	if a.reg == nil {
		a.reg = MustRegistry()
	}
	return a
}

// Registry returns the section registry.
func (a *Assembler) Registry() *Registry { return a.reg }

// Skipped is the number of sections left out so far because their key@version is not registered (or their
// props are not a JSON object): the "warning counter" of doc/08 section 4.5.
func (a *Assembler) Skipped() uint64 { return a.skipped.Load() }

// ---- response types ------------------------------------------------------------------------------------------------

// PageData is `data` of GET /public/pages/resolve.
type PageData struct {
	Locale   string    `json:"locale"`
	Page     PageInfo  `json:"page"`
	Sections []Section `json:"sections"`
	Media    media.Map `json:"media"`
}

// PageInfo is data.page.
type PageInfo struct {
	ID             int64   `json:"id"`
	PublicID       string  `json:"public_id"`
	Key            string  `json:"key"`
	Template       string  `json:"template"`
	Path           string  `json:"path"`
	IsHome         bool    `json:"is_home"`
	ResolvedLocale string  `json:"resolved_locale"`
	IsFallback     bool    `json:"is_fallback"`
	Title          string  `json:"title"`
	UpdatedAt      string  `json:"updated_at"`
	SEO            PageSEO `json:"seo"`
}

// PageSEO is data.page.seo with the fallbacks of doc/04 section 7 already applied.
type PageSEO struct {
	Title         string    `json:"title"`
	TitleAbsolute bool      `json:"title_absolute"`
	Description   *string   `json:"description"`
	OGImage       *MediaRef `json:"og_image"`
	Canonical     string    `json:"canonical"`
	Noindex       bool      `json:"noindex"`
}

// MediaRef points into data.media.
type MediaRef struct {
	MediaID int64 `json:"media_id"`
}

// Section is one entry of data.sections. Props and Resolved are pre-encoded JSON so that a cached body and a
// fresh one are byte-identical (stable ETag).
type Section struct {
	ID         string          `json:"id"`
	Key        string          `json:"key"`
	Version    int             `json:"version"`
	Variant    *string         `json:"variant"`
	AnchorID   *string         `json:"anchor_id"`
	IsFallback bool            `json:"is_fallback"`
	Props      json.RawMessage `json:"props"`
	Resolved   json.RawMessage `json:"resolved"`
}

// PageInput is one page as the views hold it. Locale is the normalised answer locale, Chain its fallback chain
// (localize.Negotiation.Chain).
type PageInput struct {
	Locale   string
	Chain    []string
	Page     *entities.PublishedPage
	Sections []entities.PublishedPageSection
	// Warnings, when set, switches on PREVIEW mode (see Warnings): an unpublished reference keeps its section
	// with an empty `resolved` and every omission is reported. Nil (the public endpoint) leaves the output
	// exactly as P-06 has always produced it.
	Warnings *Warnings
}

// PageResult is the page and the public tags it depends on (doc/08 section 2.8).
type PageResult struct {
	Data *PageData
	Tags []string
	// Warnings is set in preview mode only (PageInput.Warnings != nil).
	Warnings []Warning
}

// ---- assembly ----------------------------------------------------------------------------------------------------

type prepared struct {
	idx       int // position in PageInput.Sections (the /sections/<idx> of a warning)
	row       entities.PublishedPageSection
	spec      *sectionType
	collapsed map[string]any
	fallback  bool
}

// Page assembles a page. Errors are infrastructure errors (a repository failed); content problems skip the
// section instead.
func (a *Assembler) Page(ctx context.Context, in PageInput) (*PageResult, error) {
	if in.Page == nil {
		return nil, fmt.Errorf("assemble: no page")
	}
	e := &env{a: a, in: in, tags: newTagSet(), links: newLinkTargets(a.d.Pages, a.d.Posts, in.Locale)}
	e.tags.add("pages", fmt.Sprintf("page:%d", in.Page.PageID), "settings", "locales")

	srows, err := a.d.Settings.List(ctx, in.Locale, nil)
	if err != nil {
		return nil, err
	}
	e.settings, _ = BuildSettings(srows)
	lrows, err := a.d.Locales.ListActive(ctx)
	if err != nil {
		return nil, err
	}
	e.dates = DateFormatFor(LocaleInfos(lrows), in.Chain)

	// 1. collapse the locale maps of every known section
	var secs []*prepared
	for i, row := range in.Sections {
		spec := a.reg.lookup(row.SectionKey, row.SectionVersion)
		if spec == nil {
			a.skip(in, row, "unknown_type")
			in.Warnings.add(WarnSectionTypeUnknown, sectionPath(i), "section type %s@%d is not known to this portal", row.SectionKey, row.SectionVersion)
			continue
		}
		raw, derr := decodeProps(row.Props)
		if derr != nil {
			a.skip(in, row, "bad_props")
			in.Warnings.add(WarnSectionTypeUnknown, sectionPath(i)+"/props", "the props of %s are not a JSON object", row.SectionKey)
			continue
		}
		collapsed, fb := localize.Collapse(raw, spec.hints, in.Chain)
		secs = append(secs, &prepared{idx: i, row: row, spec: spec, collapsed: collapsed, fallback: fb})
	}

	// 2. one batched lookup for every page / post that a link or a rich text mark refers to
	collect := newTransformer()
	collect.collecting = true
	for _, s := range secs {
		collect.apply(s.spec.plan, s.collapsed)
	}
	if err = e.links.ensure(ctx, sortedIDs(collect.pageIDs), sortedIDs(collect.postIDs)); err != nil {
		return nil, err
	}
	tr := newTransformer()
	tr.pages = e.links.pages
	tr.resolver = e.links.resolver()
	e.t = tr
	if in.Warnings.On() {
		e.warnLinks(secs)
	}

	// 3. structure + resolve
	data := &PageData{Locale: in.Locale, Sections: []Section{}}
	mediaIDs := map[int64]struct{}{}
	var firstHero int64
	for _, s := range secs {
		props, _ := tr.apply(s.spec.plan, s.collapsed).(map[string]any)
		if props == nil {
			props = map[string]any{}
		}
		resolved, rerr := s.spec.resolve(ctx, e, &sectionIn{idx: s.idx, row: s.row, props: props})
		if rerr != nil {
			return nil, fmt.Errorf("assemble: section %s: %w", s.row.SectionKey, rerr)
		}
		if resolved == nil {
			if !in.Warnings.On() {
				continue // the entity it shows is not published: leave the whole section out
			}
			// preview: keep the section (the editor sees where it goes), empty `resolved`, and say why
			in.Warnings.add(WarnReferenceNotPublished, sectionPath(s.idx), "%s shows an entity that is not published (or was not chosen yet)", s.row.SectionKey)
			resolved = map[string]any{}
		}
		pb, perr := json.Marshal(props)
		if perr != nil {
			a.skip(in, s.row, "unencodable_props")
			continue
		}
		rb, rerr2 := json.Marshal(resolved)
		if rerr2 != nil {
			return nil, rerr2
		}
		if firstHero == 0 && strings.HasPrefix(s.row.SectionKey, "hero_") {
			if bg, ok := props["background"].(map[string]any); ok {
				if id, ok := asID(bg["media_id"]); ok {
					firstHero = id
				}
			}
		}
		for _, id := range media.CollectIDs(props) {
			mediaIDs[id] = struct{}{}
		}
		// resolved holds typed structs: walk its JSON form, which is also what the client sees
		if rdec, derr := localize.DecodeObject(rb); derr == nil {
			for _, id := range media.CollectIDs(rdec) {
				mediaIDs[id] = struct{}{}
			}
		}
		data.Sections = append(data.Sections, Section{
			ID: s.row.PublicID, Key: s.row.SectionKey, Version: s.row.SectionVersion, Variant: s.row.Variant,
			AnchorID: s.row.AnchorID, IsFallback: s.fallback, Props: pb, Resolved: rb,
		})
	}
	for id := range tr.media {
		mediaIDs[id] = struct{}{}
	}

	// 4. page head: SEO fallbacks (doc/04 section 7)
	p := in.Page
	base := e.settings.BaseURL(a.d.DefaultBaseURL)
	seo := PageSEO{Title: p.Title, TitleAbsolute: p.SEOTitleAbsolute, Canonical: base + p.Path, Noindex: p.Noindex}
	if p.SEOTitle != nil && strings.TrimSpace(*p.SEOTitle) != "" {
		seo.Title = *p.SEOTitle
	}
	if p.CanonicalPath != nil && *p.CanonicalPath != "" {
		seo.Canonical = base + *p.CanonicalPath
	}
	switch {
	case p.SEODescription != nil && strings.TrimSpace(*p.SEODescription) != "":
		seo.Description = p.SEODescription
	default:
		if d := e.settings.String("seo", "default_description"); d != "" {
			seo.Description = &d
		}
	}
	og := int64(0)
	switch {
	case p.OGImageID != nil && *p.OGImageID > 0:
		og = *p.OGImageID
	case firstHero > 0:
		og = firstHero
	default:
		og = e.settings.MediaID("seo", "default_og_media_id")
	}
	if og > 0 {
		seo.OGImage = &MediaRef{MediaID: og}
		mediaIDs[og] = struct{}{}
	}
	data.Page = PageInfo{
		ID: p.PageID, PublicID: p.PublicID, Key: p.Key, Template: p.Template, Path: p.Path, IsHome: p.IsHome,
		ResolvedLocale: p.ResolvedLocale, IsFallback: p.IsFallback, Title: p.Title, UpdatedAt: isoOrBlank(p.UpdatedAt), SEO: seo,
	}

	// 5. media
	m, err := a.d.Media.Resolve(ctx, a.d.MediaLookup, in.Locale, sortedIDs(mediaIDs))
	if err != nil {
		return nil, err
	}
	data.Media = m
	return &PageResult{Data: data, Tags: e.tags.list()}, nil
}

// warnLinks (preview only) reports, per section, the pages and posts that a link or a rich-text mark refers to
// and that are not published: their href is null / the mark loses its link.
func (e *env) warnLinks(secs []*prepared) {
	for _, s := range secs {
		c := newTransformer()
		c.collecting = true
		c.apply(s.spec.plan, s.collapsed)
		for _, id := range sortedIDs(c.pageIDs) {
			if _, ok := e.links.pages[id]; !ok {
				e.in.Warnings.add(WarnLinkPageNotPublished, sectionPath(s.idx), "a link points at page %d, which is not published", id)
			}
		}
		for _, id := range sortedIDs(c.postIDs) {
			if _, ok := e.links.posts[id]; !ok {
				e.in.Warnings.add(WarnPostNotPublished, sectionPath(s.idx), "a link points at post %d, which is not published", id)
			}
		}
	}
}

// isoOrBlank is readkit.ISO, but "" for the zero time (a preview has no updated_at of the live row).
func isoOrBlank(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return readkit.ISO(t)
}

func decodeProps(b entities.JSONB) (map[string]any, error) {
	if len(b) == 0 {
		return map[string]any{}, nil
	}
	m, err := localize.DecodeObject(b.Bytes())
	if err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

func (a *Assembler) skip(in PageInput, row entities.PublishedPageSection, reason string) {
	a.skipped.Add(1)
	logs.WarnEvery("assemble.skip."+reason+"."+row.SectionKey, time.Minute, "assemble: section skipped",
		logs.String("event", "section_"+reason), logs.String("key", row.SectionKey),
		logs.Int("version", row.SectionVersion), logs.String("page_key", in.Page.Key))
}

// ---- per-page environment ----------------------------------------------------------------------------------------

// env is what a resolver can use while one page is assembled.
type env struct {
	a        *Assembler
	in       PageInput
	settings Settings
	dates    DateFormats
	links    *linkTargets
	t        *transformer
	tags     *tagSet
}

func (e *env) locale() string { return e.in.Locale }

// sectionIn is the input of a resolver: the row (raw ids, config) and its transformed props.
type sectionIn struct {
	idx   int // position in PageInput.Sections (warnings)
	row   entities.PublishedPageSection
	props map[string]any
}

// resolverFunc returns the `resolved` object of a section. (nil, nil) means "skip the section".
type resolverFunc func(ctx context.Context, e *env, s *sectionIn) (any, error)

// tagSet is an ordered set of cache tags.
type tagSet struct {
	seen  map[string]struct{}
	order []string
}

func newTagSet() *tagSet { return &tagSet{seen: map[string]struct{}{}} }

func (t *tagSet) add(tags ...string) {
	for _, tag := range tags {
		if _, dup := t.seen[tag]; dup {
			continue
		}
		t.seen[tag] = struct{}{}
		t.order = append(t.order, tag)
	}
}

func (t *tagSet) list() []string { return append([]string(nil), t.order...) }
