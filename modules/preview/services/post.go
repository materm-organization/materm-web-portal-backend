package services

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	blogmodels "github.com/materm-organization/materm-web-portal-backend/modules/blog/models"
	blogservices "github.com/materm-organization/materm-web-portal-backend/modules/blog/services"
	"github.com/materm-organization/materm-web-portal-backend/modules/content/entities"
	"github.com/materm-organization/materm-web-portal-backend/modules/content/readkit"
	"github.com/materm-organization/materm-web-portal-backend/modules/content/repositories"
	"github.com/materm-organization/materm-web-portal-backend/pkg/assemble"
	"github.com/materm-organization/materm-web-portal-backend/pkg/richtext"
)

// EntityPosts is the entity_type of a post revision (doc/05 PT-18, M5).
const EntityPosts = "cms.posts"

// PostOptions are the facts about the request that the snapshot does not hold.
type PostOptions struct {
	// EntityID is cms_posts.id of the previewed post (the snapshot has no id of its own).
	EntityID int64
	// Locale is the negotiated answer locale, Chain its fallback chain, DefaultLocale the default of the site
	// (the canonical URL is on its slug).
	Locale        string
	Chain         []string
	DefaultLocale string
	// Now is stamped into updated_at (a frozen snapshot has none) and used as the date of a post that was never
	// published and has no editor-set date.
	Now time.Time
}

// PostResult is the P-08 body built from the snapshot and the warnings (never nil).
type PostResult struct {
	Detail   *blogmodels.PostDetail
	Warnings []assemble.Warning
}

// PostRenderer turns a post snapshot into the body of P-08.
type PostRenderer interface {
	Render(ctx context.Context, raw *assemble.RawPost, o PostOptions) (*PostResult, error)
}

// PostSources is everything the post renderer reads: the views (current PUBLISHED data of everything the
// snapshot refers to) and the blog's own page builder, so that a preview is rendered by the same code as
// GET /public/posts/:slug (doc/08 section 2.4).
type PostSources struct {
	Set      *repositories.Set
	Composer *blogservices.Composer
}

// NewPostRenderer builds the renderer.
func NewPostRenderer(s PostSources) PostRenderer { return &postRenderer{s} }

type postRenderer struct{ PostSources }

func (r *postRenderer) Render(ctx context.Context, raw *assemble.RawPost, o PostOptions) (*PostResult, error) {
	warns := []assemble.Warning{}
	warn := func(code, path, format string, args ...any) {
		warns = append(warns, assemble.Warning{Code: code, Path: path, Message: fmt.Sprintf(format, args...)})
	}

	idx, tr := raw.PickTranslation(o.Chain)
	if tr == nil {
		idx, tr = anyTranslation(raw)
	}
	if tr == nil { // ParsePostSnapshot guarantees a slug, so this cannot happen
		return nil, assemble.ErrBadSnapshot
	}

	// the live row (only if the post is published now): public id, the date it went live and its author. The
	// author of an unpublished post is not readable by the portal role (no granted view), so it stays empty.
	var live *entities.PublishedPostCard
	if o.EntityID > 0 {
		rows, err := r.Set.Posts.ByIDs(ctx, o.Locale, []int64{o.EntityID})
		if err != nil {
			return nil, err
		}
		if len(rows) > 0 {
			live = &rows[0]
		}
	}

	card := entities.PublishedPostCard{
		RequestedLocale: o.Locale, PostID: o.EntityID,
		HeroMediaID: raw.Post.HeroMediaID, TeaserMediaID: raw.Post.TeaserMediaID, IsFeatured: raw.Post.IsFeatured,
		UpdatedAt: o.Now, ResolvedLocale: tr.LocaleCode, IsFallback: tr.LocaleCode != o.Locale,
		Slug: tr.Slug, Title: tr.Title, Excerpt: tr.Excerpt, Label: tr.Label, LocationLabel: tr.LocationLabel, HeroAlt: tr.HeroAlt,
	}
	card.CardMediaID = card.TeaserMediaID
	if card.CardMediaID == nil {
		card.CardMediaID = card.HeroMediaID
	}
	publishedAt := o.Now
	if raw.Post.PublishedAt != nil {
		publishedAt = *raw.Post.PublishedAt
	} else if live != nil && live.PublishedAt != nil {
		publishedAt = *live.PublishedAt
	}
	card.PublishedAt = &publishedAt
	if live != nil {
		card.PublicID, card.AuthorID, card.AuthorName = live.PublicID, live.AuthorID, live.AuthorName
	}

	// category and tags: the published ones; what is not published yields a warning, not a failure
	if raw.Post.CategoryID != nil {
		cats, err := r.Set.Taxonomy.Categories(ctx, o.Locale)
		if err != nil {
			return nil, err
		}
		found := false
		for _, c := range cats {
			if c.CategoryID == *raw.Post.CategoryID {
				id, key, name, slug := c.CategoryID, c.Key, c.Name, c.Slug
				card.CategoryID, card.CategoryKey, card.CategoryName, card.CategorySlug = &id, &key, &name, &slug
				found = true
				break
			}
		}
		if !found {
			warn(assemble.WarnReferenceNotPublished, "/post/category_id", "the category is not published, the post is shown without it")
		}
	}
	var tags []entities.PublishedPostTag
	if len(raw.TagIDs) > 0 {
		all, err := r.Set.Taxonomy.Tags(ctx, o.Locale)
		if err != nil {
			return nil, err
		}
		byID := make(map[int64]entities.PublishedTag, len(all))
		for _, t := range all {
			byID[t.TagID] = t
		}
		for i, id := range raw.TagIDs {
			t, ok := byID[id]
			if !ok {
				warn(assemble.WarnReferenceNotPublished, fmt.Sprintf("/tag_ids/%d", i), "a tag is not published, the post is shown without it")
				continue
			}
			tags = append(tags, entities.PublishedPostTag{PostID: o.EntityID, RequestedLocale: o.Locale, TagID: t.TagID, Key: t.Key, Name: t.Name, Slug: t.Slug})
		}
		sort.SliceStable(tags, func(i, j int) bool { // the order of v_published_post_tags: name, tag_id
			if tags[i].Name != tags[j].Name {
				return tags[i].Name < tags[j].Name
			}
			return tags[i].TagID < tags[j].TagID
		})
	}

	// gallery: alt / caption from the first language of the chain that has a row (as v_published_post_media);
	// nil falls back to the asset's own text in the media map
	media := make([]entities.PublishedPostMedia, 0, len(raw.Media))
	for _, m := range raw.Media {
		row := entities.PublishedPostMedia{RequestedLocale: o.Locale, PostID: o.EntityID, PostMediaID: m.ID, MediaID: m.MediaID, SortOrder: m.SortOrder}
	pick:
		for _, code := range o.Chain {
			for _, mt := range m.Translations {
				if mt.LocaleCode == code {
					row.Alt, row.Caption = mt.Alt, mt.Caption
					break pick
				}
			}
		}
		media = append(media, row)
	}

	// links in the body: published pages and posts only; the rest loses its href, with a warning
	body := []byte(tr.Body)
	doc := readkit.DecodeJSON(body)
	links, err := readkit.ResolveLinks(ctx, r.Set, o.Locale, doc)
	if err != nil {
		return nil, err
	}
	pageIDs, postIDs := richtext.CollectLinks(doc)
	bodyPath := fmt.Sprintf("/translations/%d/body", idx)
	for _, id := range pageIDs {
		if _, ok := links.Pages[id]; !ok {
			warn(assemble.WarnLinkPageNotPublished, bodyPath, "a link in the body points to a page that is not published")
		}
	}
	for _, id := range postIDs {
		if _, ok := links.Posts[id]; !ok {
			warn(assemble.WarnPostNotPublished, bodyPath, "a link in the body points to a post that is not published")
		}
	}

	canonical := tr.Slug
	for _, t := range raw.Translations {
		if t.LocaleCode == o.DefaultLocale && strings.TrimSpace(t.Slug) != "" {
			canonical = t.Slug
			break
		}
	}

	post := &entities.PublishedPost{
		PublishedPostCard: card, Body: entities.JSONB(body),
		GalleryHeading: tr.GalleryHeading, GalleryDescription: tr.GalleryDescription,
		SEOTitle: tr.SEOTitle, SEODescription: tr.SEODescription, OGImageID: tr.OGImageID,
	}
	detail, err := r.Composer.Compose(ctx, o.Locale, blogservices.DetailParts{
		Post: post, Links: links.MapResolver, Tags: tags, Media: media, CanonicalSlug: canonical,
		Noindex: true, // a preview must never be indexed
	})
	if err != nil {
		return nil, err
	}
	return &PostResult{Detail: detail, Warnings: warns}, nil
}

// anyTranslation is the last resort when no enabled translation is in the chain: the first enabled one by
// language code, then the first one with a slug at all (it is the editor's own draft).
func anyTranslation(raw *assemble.RawPost) (int, *assemble.RawPostTranslation) {
	best := -1
	for i, t := range raw.Translations {
		if t.Slug == "" {
			continue
		}
		switch {
		case best < 0:
			best = i
		case t.Enabled() && !raw.Translations[best].Enabled():
			best = i
		case t.Enabled() == raw.Translations[best].Enabled() && t.LocaleCode < raw.Translations[best].LocaleCode:
			best = i
		}
	}
	if best < 0 {
		return -1, nil
	}
	return best, &raw.Translations[best]
}
