package assemble

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/entities"
)

// The Raw adapter: a frozen page SNAPSHOT (cms_content_revisions.snapshot, schema_version 1, doc/04 section 5.2,
// the image preview links are bound to) turned into the PageInput that the assembler builds P-06 from, so a
// preview is rendered by exactly the same code as the public page (doc/08 section 2.4). What differs from the
// public path:
//
//   - the page head, its translations and its sections come from the snapshot, not from the views; the page
//     is not required to be published;
//   - everything the snapshot REFERS to (media, linked pages and posts, albums, FAQ groups, forms, settings,
//     menus) is read from the CURRENT published data, as on the public page; what is not published yields an
//     empty `resolved` and a warning instead of a dropped section;
//   - sections with is_visible=false are skipped (as v_published_page_sections does);
//   - draft data is tolerated: a missing optional member never fails the preview;
//   - the page is marked noindex, whatever the snapshot says.

// ErrBadSnapshot: the snapshot is not the page image of doc/04 section 5.2 (not an object, a member of the wrong
// type, no path). The text never carries snapshot content.
var ErrBadSnapshot = errors.New("assemble: unusable page snapshot")

// RawPage is a decoded page snapshot. Members the preview does not use (parent_id, sort_order, ...) are ignored.
type RawPage struct {
	Page struct {
		Key       string `json:"key"`
		Name      string `json:"name"`
		Template  string `json:"template"`
		Path      string `json:"path"`
		IsHome    bool   `json:"is_home"`
		Noindex   bool   `json:"noindex"`
		InSitemap bool   `json:"in_sitemap"`
	} `json:"page"`
	Translations []RawPageTranslation `json:"translations"`
	Sections     []RawSection         `json:"sections"`
	// SharedSections is the working copy of the shared sections the visible sections use, frozen by the admin
	// into the preview snapshot (doc/05 section 18.12.2). A POINTER: nil = the key is absent (a link issued before
	// the admin wrote the member: the PUBLISHED content is read as before), non-nil (even empty) = the member is
	// authoritative and SharedSource is not consulted.
	SharedSections *[]RawSharedSection `json:"shared_sections"`
}

// RawSharedSection is one entry of shared_sections[]. The admin also writes name and draft_revision_id; the
// preview does not use them. State is "published" when the shared section was live at issue time.
type RawSharedSection struct {
	ID             int64           `json:"id"`
	SectionType    string          `json:"section_type"`
	SectionVersion int             `json:"section_version"`
	Variant        *string         `json:"variant"`
	Props          json.RawMessage `json:"props"`
	State          string          `json:"state"`
}

// RawPageTranslation is one entry of translations[]. A missing is_enabled counts as enabled.
type RawPageTranslation struct {
	LocaleCode       string  `json:"locale_code"`
	Title            string  `json:"title"`
	SEOTitle         *string `json:"seo_title"`
	SEOTitleAbsolute bool    `json:"seo_title_absolute"`
	SEODescription   *string `json:"seo_description"`
	OGImageID        *int64  `json:"og_image_id"`
	CanonicalPath    *string `json:"canonical_path"`
	IsEnabled        *bool   `json:"is_enabled"`
}

// RawSection is one entry of sections[]. A missing is_visible counts as visible, a missing section_version as 1.
type RawSection struct {
	ID              int64           `json:"id"`
	PublicID        string          `json:"public_id"`
	SectionType     string          `json:"section_type"`
	SectionVersion  int             `json:"section_version"`
	Variant         *string         `json:"variant"`
	SortOrder       int             `json:"sort_order"`
	IsVisible       *bool           `json:"is_visible"`
	AnchorID        *string         `json:"anchor_id"`
	Props           json.RawMessage `json:"props"`
	SharedSectionID *int64          `json:"shared_section_id"`
}

// ParsePageSnapshot decodes the snapshot of a `cms.pages` revision.
func ParsePageSnapshot(b []byte) (*RawPage, error) {
	var r RawPage
	if err := json.Unmarshal(b, &r); err != nil {
		// json errors may quote a value: report the kind only
		return nil, fmt.Errorf("%w: not decodable", ErrBadSnapshot)
	}
	if strings.TrimSpace(r.Page.Path) == "" {
		return nil, fmt.Errorf("%w: no page.path", ErrBadSnapshot)
	}
	return &r, nil
}

// PreviewOptions are the facts about the request that the snapshot does not hold.
type PreviewOptions struct {
	// EntityID is cms_pages.id of the previewed page (the snapshot has no id of its own).
	EntityID int64
	// Locale is the negotiated answer locale and Chain its fallback chain (localize.Negotiation).
	Locale string
	Chain  []string
	// Now is stamped into page.updated_at: a frozen snapshot has no updated_at, and the live row's is not the
	// snapshot's. Zero leaves it blank.
	Now time.Time
}

// PreviewPage assembles a page from its snapshot. The result carries the warnings (never nil) in
// PageResult.Warnings; their paths point into the snapshot (/sections/<index in sections[]>).
func (a *Assembler) PreviewPage(ctx context.Context, raw *RawPage, o PreviewOptions) (*PageResult, error) {
	if raw == nil {
		return nil, ErrBadSnapshot
	}
	warn := &Warnings{}  // warnings of Page: numbered by position in rows, remapped below
	early := &Warnings{} // warnings made here: already numbered by position in the snapshot

	// order and visibility (the view: is_visible, ORDER BY sort_order, id)
	type indexed struct {
		i int
		s RawSection
	}
	var keep []indexed
	for i, s := range raw.Sections {
		if s.IsVisible != nil && !*s.IsVisible {
			continue
		}
		keep = append(keep, indexed{i, s})
	}
	sort.SliceStable(keep, func(x, y int) bool { return keep[x].s.SortOrder < keep[y].s.SortOrder })

	// shared sections: with the snapshot member `shared_sections` the content is the working copy frozen in it
	// (SharedSource is not consulted); without it (a link issued before the member existed) the content is the
	// PUBLISHED shared section
	var frozen map[int64]RawSharedSection
	var shared map[int64]entities.PublishedPageSection
	if raw.SharedSections != nil {
		frozen = make(map[int64]RawSharedSection, len(*raw.SharedSections))
		for _, e := range *raw.SharedSections {
			if _, dup := frozen[e.ID]; !dup {
				frozen[e.ID] = e
			}
		}
	} else {
		kept := make([]RawSection, len(keep))
		for n, k := range keep {
			kept[n] = k.s
		}
		var err error
		if shared, err = a.sharedFor(ctx, kept); err != nil {
			return nil, err
		}
	}

	var rows []entities.PublishedPageSection
	var orig []int // rows[n] came from raw.Sections[orig[n]]
	for _, k := range keep {
		s := k.s
		version := s.SectionVersion
		if version < 1 {
			version = 1
		}
		row := entities.PublishedPageSection{
			PageID: o.EntityID, SectionID: s.ID, PublicID: s.PublicID, SectionKey: s.SectionType, SectionVersion: version,
			Variant: s.Variant, SortOrder: s.SortOrder, AnchorID: s.AnchorID, Props: entities.JSONB(s.Props),
			SharedSectionID: s.SharedSectionID,
		}
		if row.PublicID == "" {
			row.PublicID = "preview-" + strconv.Itoa(k.i)
		}
		if s.SharedSectionID != nil && raw.SharedSections != nil {
			e, ok := frozen[*s.SharedSectionID]
			if !ok || e.SectionType != s.SectionType || e.SectionVersion != version {
				early.add(WarnSharedSectionUnavailable, sectionPath(k.i), "shared section %d is not available for this preview, the section is left out", *s.SharedSectionID)
				continue
			}
			row.Props = entities.JSONB(e.Props)
			if row.Variant == nil {
				row.Variant = e.Variant
			}
			if e.State != "published" {
				early.add(WarnSharedSectionNotLive, sectionPath(k.i)+"/shared_section_id", "shared section %d is not live, the public page would not show it", *s.SharedSectionID)
			}
		} else if s.SharedSectionID != nil {
			sh, ok := shared[*s.SharedSectionID]
			if !ok || sh.SectionKey != s.SectionType {
				early.add(WarnSharedSectionUnavailable, sectionPath(k.i), "shared section %d is not published, the section is left out", *s.SharedSectionID)
				continue
			}
			row.Props = sh.Props
			if row.Variant == nil {
				row.Variant = sh.Variant
			}
		}
		rows = append(rows, row)
		orig = append(orig, k.i)
	}

	res, err := a.Page(ctx, PageInput{
		Locale: o.Locale, Chain: o.Chain, Page: a.rawPageRow(ctx, raw, o), Sections: rows, Warnings: warn,
	})
	if err != nil {
		return nil, err
	}
	// Page numbered the sections by position in rows: point the paths back into the snapshot
	list := warn.List()
	for n := range list {
		list[n].Path = remapSectionPath(list[n].Path, orig)
	}
	res.Warnings = append(early.List(), list...)
	return res, nil
}

// sharedFor loads the published content of the shared sections the visible sections use (one query).
func (a *Assembler) sharedFor(ctx context.Context, secs []RawSection) (map[int64]entities.PublishedPageSection, error) {
	var ids []int64
	seen := map[int64]struct{}{}
	for _, s := range secs {
		if s.SharedSectionID == nil {
			continue
		}
		if _, dup := seen[*s.SharedSectionID]; !dup {
			seen[*s.SharedSectionID] = struct{}{}
			ids = append(ids, *s.SharedSectionID)
		}
	}
	out := map[int64]entities.PublishedPageSection{}
	if len(ids) == 0 || a.d.Shared == nil {
		return out, nil
	}
	rows, err := a.d.Shared.SharedSections(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if r.SharedSectionID == nil {
			continue
		}
		if _, have := out[*r.SharedSectionID]; !have {
			out[*r.SharedSectionID] = r
		}
	}
	return out, nil
}

// rawPageRow builds the page row from the snapshot, choosing the translation like v_published_pages does: the
// first ENABLED translation in chain order; none -> the page's internal name as the title.
func (a *Assembler) rawPageRow(ctx context.Context, raw *RawPage, o PreviewOptions) *entities.PublishedPage {
	p := &entities.PublishedPage{
		RequestedLocale: o.Locale, PageID: o.EntityID, Key: raw.Page.Key, Template: raw.Page.Template,
		IsHome: raw.Page.IsHome, InSitemap: raw.Page.InSitemap,
		// a preview must never be indexed, whatever the snapshot says
		Noindex:   true,
		UpdatedAt: o.Now, Path: raw.Page.Path, ResolvedLocale: o.Locale, Title: raw.Page.Name,
	}
	if p.Template == "" {
		p.Template = "standard"
	}
	if t := pickTranslation(raw.Translations, o.Chain); t != nil {
		p.ResolvedLocale, p.IsFallback = t.LocaleCode, t.LocaleCode != o.Locale
		p.Title, p.SEOTitle, p.SEOTitleAbsolute = t.Title, t.SEOTitle, t.SEOTitleAbsolute
		p.SEODescription, p.OGImageID, p.CanonicalPath = t.SEODescription, t.OGImageID, t.CanonicalPath
	}
	// the public id lives on the live row only (a page that was never published has none: "")
	if a.d.Pages != nil && o.EntityID > 0 {
		if live, err := a.d.Pages.ByIDs(ctx, o.Locale, []int64{o.EntityID}); err == nil && len(live) > 0 {
			p.PublicID = live[0].PublicID
		}
	}
	return p
}

func pickTranslation(ts []RawPageTranslation, chain []string) *RawPageTranslation {
	for _, code := range chain {
		for i := range ts {
			t := &ts[i]
			if t.LocaleCode == code && (t.IsEnabled == nil || *t.IsEnabled) {
				return t
			}
		}
	}
	return nil
}

// remapSectionPath turns "/sections/<n>..." (n = position among the rows given to Page) into
// "/sections/<orig[n]>..." (position in the snapshot).
func remapSectionPath(path string, orig []int) string {
	rest, ok := strings.CutPrefix(path, "/sections/")
	if !ok {
		return path
	}
	num, tail, _ := strings.Cut(rest, "/")
	n, err := strconv.Atoi(num)
	if err != nil || n < 0 || n >= len(orig) {
		return path
	}
	if tail != "" {
		tail = "/" + tail
	}
	return "/sections/" + strconv.Itoa(orig[n]) + tail
}
