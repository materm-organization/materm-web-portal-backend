// Package services holds the logic of the blog module (P-07 ... P-10): the post list, the post page with its
// two-step slug resolution, categories and tags. Every answer is a finished DTO that goes through the response
// cache (pkg/publiccache) with the tags of doc/08 section 2.8.
package services

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/materm-organization/materm-web-portal-backend/modules/blog/models"
	"github.com/materm-organization/materm-web-portal-backend/modules/content/entities"
	"github.com/materm-organization/materm-web-portal-backend/modules/content/readkit"
	"github.com/materm-organization/materm-web-portal-backend/modules/content/repositories"
	"github.com/materm-organization/materm-web-portal-backend/pkg/apperror"
	"github.com/materm-organization/materm-web-portal-backend/pkg/localize"
	"github.com/materm-organization/materm-web-portal-backend/pkg/media"
	"github.com/materm-organization/materm-web-portal-backend/pkg/pagination"
	"github.com/materm-organization/materm-web-portal-backend/pkg/publiccache"
	"github.com/materm-organization/materm-web-portal-backend/pkg/richtext"
)

// Post list bounds (doc/08 section 2.1; OpenAPI P-07): limit 3-48, default 12.
const (
	DefaultLimit = 12
	MinLimit     = 3
	MaxLimit     = 48
	// MaxPage keeps (page-1)*limit far from overflow; no blog has this many pages (NFR-27: <= 5,000 posts).
	MaxPage = 100000
)

// PostTTL is the lifetime of a cached P-08 answer, for a post, a 404 and a 308 alike (doc/08 section 2.3.3:
// the slug result is cached for a short time so that a hostile scan of slugs cannot fill Redis for a day and
// a post that becomes visible shows up within a minute even if no event arrives).
const PostTTL = 60 * time.Second

// PostsQuery is a validated P-07 request.
type PostsQuery struct {
	Category string // category key; "" = all
	Tag      string // tag key; "" = all
	Page     int
	Limit    int
}

// BlogService serves the blog endpoints. locale is always a code already negotiated by the controller.
type BlogService interface {
	Posts(ctx context.Context, locale string, q PostsQuery) (*models.PostsPage, error)
	Post(ctx context.Context, locale, slug string) (*models.PostOutcome, error)
	Categories(ctx context.Context, locale string) ([]models.CategoryInfo, error)
	Tags(ctx context.Context, locale string) ([]models.TagInfo, error)
}

// Deps are the collaborators of the service.
type Deps struct {
	Content *repositories.Set
	Media   *media.Builder
	Lookup  media.Lookup
	Cache   *publiccache.Cache
	Locales *localize.Provider
	// DefaultBaseURL is site.defaultBaseUrl: the canonical host when the setting site.base_url is blank.
	DefaultBaseURL string
}

type blogService struct{ d Deps }

// NewBlogService builds the service.
func NewBlogService(d Deps) BlogService { return &blogService{d: d} }

// ---- P-07 ---------------------------------------------------------------------------------------------------

func (s *blogService) Posts(ctx context.Context, locale string, q PostsQuery) (*models.PostsPage, error) {
	spec := publiccache.Spec{Resource: "posts", Locale: locale, Params: map[string]string{
		"category": q.Category, "tag": q.Tag, "page": strconv.Itoa(q.Page), "limit": strconv.Itoa(q.Limit),
	}}
	page, _, err := publiccache.Get(ctx, s.d.Cache, spec, func(ctx context.Context) (*models.PostsPage, []string, error) {
		tags := []string{"posts:list", "locales"}
		meta := func(total int64) pagination.Meta {
			return pagination.NewMeta(total, pagination.ListQuery{Page: q.Page, Limit: q.Limit})
		}
		empty := &models.PostsPage{Items: []models.PostItem{}, Meta: meta(0), Media: media.Map{}}

		filter := repositories.PostFilter{Locale: locale, TagKey: q.Tag}
		if q.Category != "" {
			cat, err := s.d.Content.Taxonomy.CategoryByKey(ctx, locale, q.Category)
			if errors.Is(err, repositories.ErrNotFound) {
				return empty, tags, nil // an unknown category is an empty list, not a 404 (doc/08 section 2.3.2)
			}
			if err != nil {
				return nil, nil, err
			}
			filter.CategoryID = &cat.CategoryID
		}
		rows, total, err := s.d.Content.Posts.List(ctx, filter, q.Limit, (q.Page-1)*q.Limit)
		if err != nil {
			return nil, nil, err
		}
		formats, err := s.dateFormats(ctx, locale)
		if err != nil {
			return nil, nil, err
		}
		items := make([]models.PostItem, 0, len(rows))
		var mediaIDs []int64
		for _, r := range rows {
			items = append(items, ItemFromCard(r, formats))
			if r.CardMediaID != nil {
				mediaIDs = append(mediaIDs, *r.CardMediaID)
			}
		}
		m, err := s.d.Media.Resolve(ctx, s.d.Lookup, locale, mediaIDs)
		if err != nil {
			return nil, nil, err
		}
		return &models.PostsPage{Items: items, Meta: meta(total), Media: m}, tags, nil
	})
	return page, err
}

// ItemFromCard maps a view row to the card shape of P-07 (also the head of P-08). Exported so that the page
// assembler's blog_strip can build the same cards with the same date format.
func ItemFromCard(c entities.PublishedPostCard, f readkit.DateFormats) models.PostItem {
	it := models.PostItem{
		ID: c.PostID, PublicID: c.PublicID, Slug: c.Slug, Href: readkit.PostHref(c.Slug),
		ResolvedLocale: c.ResolvedLocale, IsFallback: c.IsFallback, Title: c.Title,
		Excerpt: c.Excerpt, Label: c.Label, LocationLabel: c.LocationLabel, IsFeatured: c.IsFeatured,
	}
	if c.PublishedAt != nil {
		it.Date, it.DateTime = f.Format(*c.PublishedAt), readkit.ISO(*c.PublishedAt)
	}
	if c.CategoryKey != nil && c.CategoryName != nil {
		it.Category = &models.CategoryRef{Key: *c.CategoryKey, Name: *c.CategoryName}
	}
	if c.AuthorID != nil && c.AuthorName != nil {
		it.Author = &models.AuthorRef{ID: *c.AuthorID, Name: *c.AuthorName}
	}
	if c.CardMediaID != nil {
		it.Image = &models.ImageRef{MediaID: *c.CardMediaID}
		if c.TeaserMediaID == nil { // the card shows the hero, so the hero's alt is this placement's alt
			it.Image.Alt = c.HeroAlt
		}
	}
	return it
}

// dateFormats reads date_formats of the locale (the Chain does not carry them).
func (s *blogService) dateFormats(ctx context.Context, locale string) (readkit.DateFormats, error) {
	ls, err := s.d.Content.Locales.ListActive(ctx)
	if err != nil {
		return readkit.DateFormats{}, err
	}
	for _, l := range ls {
		if l.Code == locale {
			return readkit.ParseDateFormats(l.DateFormats.Bytes()), nil
		}
	}
	return readkit.DateFormats{}, nil
}

// ---- P-08 ---------------------------------------------------------------------------------------------------

// Post resolves a slug in three steps (doc/08 section 2.3.3):
//
//  1. the slug in the visitor's locale - a real translation wins over a fallback row, and a fallback row is
//     shown at its own URL (FR-BLOG-05);
//  2. otherwise the same slug owned by another locale (default locale first): the post in the visitor's locale
//     has a different slug, so the answer is a 308 to it;
//  3. otherwise 404.
//
// The outcome (also the 308 and the 404) is cached for PostTTL.
func (s *blogService) Post(ctx context.Context, locale, slug string) (*models.PostOutcome, error) {
	if len(slug) > readkit.MaxSlugLen || !readkit.SlugPattern.MatchString(slug) {
		return &models.PostOutcome{Kind: models.KindNotFound}, nil
	}
	out, _, err := publiccache.Get(ctx, s.d.Cache,
		publiccache.Spec{Resource: "post", Locale: locale, Params: map[string]string{"slug": slug}, TTL: PostTTL},
		func(ctx context.Context) (*models.PostOutcome, []string, error) {
			return s.resolvePost(ctx, locale, slug)
		})
	return out, err
}

func (s *blogService) resolvePost(ctx context.Context, locale, slug string) (*models.PostOutcome, []string, error) {
	base := []string{"posts:list", "locales"}
	p, err := s.d.Content.Posts.FindBySlug(ctx, locale, slug)
	switch {
	case err == nil:
		detail, tags, derr := s.detail(ctx, locale, p)
		if derr != nil {
			return nil, nil, derr
		}
		return &models.PostOutcome{Kind: models.KindPost, Post: detail}, tags, nil
	case !errors.Is(err, repositories.ErrNotFound):
		return nil, nil, err
	}

	chain, err := s.d.Locales.Chain(ctx)
	if err != nil {
		return nil, nil, apperror.ServiceUnavailable(err, 0)
	}
	owner, err := s.d.Content.Posts.FindSlugOwner(ctx, slug, chain.Default())
	if errors.Is(err, repositories.ErrNotFound) {
		return &models.PostOutcome{Kind: models.KindNotFound}, base, nil
	}
	if err != nil {
		return nil, nil, err
	}
	target, err := s.d.Content.Posts.FindByID(ctx, locale, owner.PostID)
	if errors.Is(err, repositories.ErrNotFound) {
		return &models.PostOutcome{Kind: models.KindNotFound}, base, nil
	}
	if err != nil {
		return nil, nil, err
	}
	tags := append([]string{"post:" + strconv.FormatInt(owner.PostID, 10)}, base...)
	if target.Slug == slug { // cannot happen (step 1 would have found it); never redirect to itself
		detail, dtags, derr := s.detail(ctx, locale, target)
		if derr != nil {
			return nil, nil, derr
		}
		return &models.PostOutcome{Kind: models.KindPost, Post: detail}, dtags, nil
	}
	return &models.PostOutcome{Kind: models.KindRedirect, Location: readkit.PostHref(target.Slug)}, tags, nil
}

// detail builds the flat post page and the cache tags it depends on.
func (s *blogService) detail(ctx context.Context, locale string, p *entities.PublishedPost) (*models.PostDetail, []string, error) {
	formats, err := s.dateFormats(ctx, locale)
	if err != nil {
		return nil, nil, err
	}
	tags := []string{"post:" + strconv.FormatInt(p.PostID, 10), "posts:list", "locales", "settings"}

	// body: links to pages and posts are resolved on the server; images join the media map
	var body map[string]any
	var bodyMedia []int64
	doc := readkit.DecodeJSON(p.Body.Bytes())
	links, err := readkit.ResolveLinks(ctx, s.d.Content, locale, doc)
	if err != nil {
		return nil, nil, err
	}
	if links.UsedPages {
		tags = append(tags, "pages")
	}
	if len(p.Body) > 0 {
		res := richtext.ResolveJSON(p.Body.Bytes(), richtext.Full, links.MapResolver)
		body, bodyMedia = res.Doc, res.MediaIDs
	}

	tagRows, err := s.d.Content.Posts.Tags(ctx, locale, []int64{p.PostID})
	if err != nil {
		return nil, nil, err
	}
	postTags := make([]models.TagInfo, 0, len(tagRows))
	for _, t := range tagRows {
		postTags = append(postTags, models.TagInfo{Key: t.Key, Name: t.Name, Slug: t.Slug})
	}
	mediaRows, err := s.d.Content.Posts.Media(ctx, locale, p.PostID)
	if err != nil {
		return nil, nil, err
	}
	gallery := models.Gallery{Heading: p.GalleryHeading, Description: p.GalleryDescription, Items: make([]models.GalleryItem, 0, len(mediaRows))}
	ids := append([]int64{}, bodyMedia...)
	for _, m := range mediaRows {
		gallery.Items = append(gallery.Items, models.GalleryItem{MediaID: m.MediaID, Alt: m.Alt, Caption: m.Caption})
		ids = append(ids, m.MediaID)
	}

	site, err := s.siteSettings(ctx, locale)
	if err != nil {
		return nil, nil, err
	}
	canonicalSlug := p.Slug
	if chain, cerr := s.d.Locales.Chain(ctx); cerr == nil && chain.Default() != locale {
		if def, derr := s.d.Content.Posts.FindByID(ctx, chain.Default(), p.PostID); derr == nil {
			canonicalSlug = def.Slug
		}
	}

	d := &models.PostDetail{
		PostItem:  ItemFromCard(p.PublishedPostCard, formats),
		Locale:    locale,
		Body:      body,
		Gallery:   gallery,
		Tags:      postTags,
		SEO:       s.seo(p, site, canonicalSlug),
		UpdatedAt: readkit.ISO(p.UpdatedAt),
	}
	if p.HeroMediaID != nil {
		d.Hero = &models.ImageRef{MediaID: *p.HeroMediaID, Alt: p.HeroAlt}
		ids = append(ids, *p.HeroMediaID)
	}
	if p.CardMediaID != nil {
		ids = append(ids, *p.CardMediaID)
	}
	if d.SEO.OGImage != nil {
		ids = append(ids, d.SEO.OGImage.MediaID)
	}
	if d.Media, err = s.d.Media.Resolve(ctx, s.d.Lookup, locale, ids); err != nil {
		return nil, nil, err
	}
	return d, tags, nil
}

// siteSettings holds the settings a post page needs.
type siteSettings struct {
	baseURL            string
	defaultDescription string
	defaultOGMediaID   int64
}

func (s *blogService) siteSettings(ctx context.Context, locale string) (siteSettings, error) {
	rows, err := s.d.Content.Settings.List(ctx, locale, []string{"site", "seo"})
	if err != nil {
		return siteSettings{}, err
	}
	out := siteSettings{baseURL: strings.TrimRight(s.d.DefaultBaseURL, "/")}
	for _, r := range rows {
		switch r.GroupKey + "." + r.Key {
		case "site.base_url":
			var v string
			if json.Unmarshal(r.Value.Bytes(), &v) == nil && strings.TrimSpace(v) != "" {
				out.baseURL = strings.TrimRight(strings.TrimSpace(v), "/")
			}
		case "seo.default_description":
			var v string
			if json.Unmarshal(r.Value.Bytes(), &v) == nil {
				out.defaultDescription = v
			}
		case "seo.default_og_media_id":
			if r.MediaAssetID != nil {
				out.defaultOGMediaID = *r.MediaAssetID
			}
		}
	}
	return out, nil
}

// seo builds the metadata: title, description (seo_description, then the excerpt, then the site default), the
// Open Graph image (og_image_id, the hero, the card image, the site default) and the canonical URL on the
// default locale's slug (doc/08 section 2.3.3).
func (s *blogService) seo(p *entities.PublishedPost, site siteSettings, canonicalSlug string) models.SEO {
	title := p.Title
	if p.SEOTitle != nil && strings.TrimSpace(*p.SEOTitle) != "" {
		title = *p.SEOTitle
	}
	var desc *string
	for _, c := range []*string{p.SEODescription, p.Excerpt} {
		if c != nil && strings.TrimSpace(*c) != "" {
			desc = c
			break
		}
	}
	if desc == nil && site.defaultDescription != "" {
		d := site.defaultDescription
		desc = &d
	}
	var og *models.MediaRef
	for _, id := range []*int64{p.OGImageID, p.HeroMediaID, p.CardMediaID} {
		if id != nil {
			og = &models.MediaRef{MediaID: *id}
			break
		}
	}
	if og == nil && site.defaultOGMediaID > 0 {
		og = &models.MediaRef{MediaID: site.defaultOGMediaID}
	}
	return models.SEO{
		Title: title, Description: desc, OGImage: og, Noindex: false,
		Canonical: site.baseURL + readkit.PostHref(canonicalSlug),
	}
}

// ---- P-09 / P-10 --------------------------------------------------------------------------------------------

func (s *blogService) Categories(ctx context.Context, locale string) ([]models.CategoryInfo, error) {
	out, _, err := publiccache.Get(ctx, s.d.Cache, publiccache.Spec{Resource: "categories", Locale: locale},
		func(ctx context.Context) ([]models.CategoryInfo, []string, error) {
			rows, err := s.d.Content.Taxonomy.Categories(ctx, locale)
			if err != nil {
				return nil, nil, err
			}
			items := make([]models.CategoryInfo, 0, len(rows))
			for _, r := range rows {
				items = append(items, models.CategoryInfo{Key: r.Key, Name: r.Name, Slug: r.Slug, SortOrder: r.SortOrder})
			}
			return items, []string{"posts:list", "locales"}, nil
		})
	return out, err
}

func (s *blogService) Tags(ctx context.Context, locale string) ([]models.TagInfo, error) {
	out, _, err := publiccache.Get(ctx, s.d.Cache, publiccache.Spec{Resource: "tags", Locale: locale},
		func(ctx context.Context) ([]models.TagInfo, []string, error) {
			rows, err := s.d.Content.Taxonomy.Tags(ctx, locale)
			if err != nil {
				return nil, nil, err
			}
			items := make([]models.TagInfo, 0, len(rows))
			for _, r := range rows {
				items = append(items, models.TagInfo{Key: r.Key, Name: r.Name, Slug: r.Slug})
			}
			return items, []string{"posts:list", "locales"}, nil
		})
	return out, err
}
