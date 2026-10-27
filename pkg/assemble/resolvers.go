package assemble

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	blogservices "github.com/materm-organization/materm-web-portal-backend/modules/blog/services"
	"github.com/materm-organization/materm-web-portal-backend/modules/content/entities"
	"github.com/materm-organization/materm-web-portal-backend/modules/content/repositories"
	"github.com/materm-organization/materm-web-portal-backend/pkg/logs"
)

// resolvers holds one resolver per section type key (doc/08 section 2.3.1 "resolved per type"). A type whose
// data is all in its props answers an empty object. A resolver returns nil to drop the section (the album,
// FAQ group or form it shows is not published).
var resolvers = map[string]resolverFunc{
	"hero_brand":      resolveNone,
	"hero_article":    resolveNone,
	"intro_heading":   resolveNone,
	"intro_text":      resolveNone,
	"teaser_collage":  resolveNone,
	"about_blocks":    resolveNone,
	"blog_strip":      resolveBlogStrip,
	"gallery_cards":   resolveGalleryCards,
	"photo_story":     resolvePhotoStory,
	"contact_section": resolveContactSection,
	"faq":             resolveFAQ,
	"post_list":       resolvePostList,
}

func resolveNone(context.Context, *env, *sectionIn) (any, error) { return map[string]any{}, nil }

// ---- small readers over decoded props --------------------------------------------------------------------------------

func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }

func asSlice(v any) []any { s, _ := v.([]any); return s }

// asString returns v when it is a non-blank string, else "".
func asString(v any) string {
	s, _ := v.(string)
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return s
}

// nilIfEmpty is the JSON-null form of a missing string.
func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func strPtr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

// ---- blog_strip ----------------------------------------------------------------------------------------------------

// BlogStripPost is one slice of resolved.posts.
type BlogStripPost struct {
	PostID   int64          `json:"post_id"`
	Href     string         `json:"href"`
	Label    *string        `json:"label"`
	Title    string         `json:"title"`
	Date     string         `json:"date"`
	DateTime string         `json:"date_time"`
	Excerpt  *string        `json:"excerpt"`
	Image    BlogStripImage `json:"image"`
}

// BlogStripImage is the image of a slice: the override of the section, else the card image of the post.
type BlogStripImage struct {
	MediaID *int64 `json:"media_id"`
	Alt     any    `json:"alt"`
}

const blogStripMax = 4

func resolveBlogStrip(ctx context.Context, e *env, s *sectionIn) (any, error) {
	e.tags.add("posts:list")
	type pick struct {
		card    entities.PublishedPostCard
		image   map[string]any
		excerpt string
	}
	var picks []pick

	if asString(s.props["source"]) == "manual" {
		items := asSlice(s.props["items"])
		var ids []int64
		for _, it := range items {
			if id, ok := asID(asMap(it)["post_id"]); ok {
				ids = append(ids, id)
			}
		}
		byID := map[int64]entities.PublishedPostCard{}
		if len(ids) > 0 {
			rows, err := e.a.d.Posts.ByIDs(ctx, e.locale(), uniqueIDs(ids))
			if err != nil {
				return nil, err
			}
			for _, r := range rows {
				byID[r.PostID] = r
			}
		}
		// walk the items in the editor's order; the same post may appear twice (the seed shows post 1 four times)
		for _, it := range items {
			m := asMap(it)
			id, ok := asID(m["post_id"])
			if !ok {
				continue
			}
			card, found := byID[id]
			if !found {
				continue // not published (any more): skipped
			}
			picks = append(picks, pick{card: card, image: asMap(m["image"]), excerpt: asString(m["excerpt"])})
		}
	} else {
		limit := blogStripMax
		if n, ok := asID(s.props["limit"]); ok && n < blogStripMax {
			limit = int(n)
		}
		f := repositories.PostFilter{Locale: e.locale()}
		if c, ok := asID(s.props["category_id"]); ok {
			f.CategoryID = &c
		}
		rows, _, err := e.a.d.Posts.List(ctx, f, limit, 0)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			picks = append(picks, pick{card: r})
		}
	}

	posts := make([]BlogStripPost, 0, len(picks))
	for _, p := range picks {
		// the card shape (href, date, date_time, image) is the blog module's: identical to P-07
		item := blogservices.ItemFromCard(p.card, e.dates)
		img := BlogStripImage{}
		if item.Image != nil {
			id := item.Image.MediaID
			img.MediaID = &id
			if item.Image.Alt != nil {
				img.Alt = *item.Image.Alt
			}
		}
		if p.image != nil {
			if id, ok := asID(p.image["media_id"]); ok {
				img.MediaID = &id
				img.Alt = p.image["alt"]
			}
		}
		excerpt := item.Excerpt
		if p.excerpt != "" {
			x := p.excerpt
			excerpt = &x
		}
		posts = append(posts, BlogStripPost{
			PostID: item.ID, Href: item.Href, Label: item.Label, Title: item.Title,
			Date: item.Date, DateTime: item.DateTime, Excerpt: excerpt, Image: img,
		})
		if len(posts) == blogStripMax {
			break
		}
	}
	return map[string]any{"posts": posts}, nil
}

func uniqueIDs(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; !dup {
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out
}

// ---- gallery_cards -------------------------------------------------------------------------------------------------

const galleryCardsDefault = 3

func resolveGalleryCards(ctx context.Context, e *env, s *sectionIn) (any, error) {
	cardsHref := asMap(s.props["cards_link"])["href"]
	if asString(s.props["source"]) == "album" {
		albumID, ok := asID(s.props["album_id"])
		if !ok {
			return nil, nil
		}
		limit := galleryCardsDefault
		if n, ok := asID(s.props["limit"]); ok {
			limit = int(n)
		}
		items, err := e.a.d.Albums.Items(ctx, e.locale(), albumID, limit)
		if err != nil {
			return nil, err
		}
		if len(items) == 0 {
			return nil, nil // album not published (or empty)
		}
		e.tags.add(fmt.Sprintf("album:%d", albumID), "albums:list")
		cards := make([]any, 0, len(items))
		for _, it := range items {
			var caption any
			if it.CaptionTitle != nil && strings.TrimSpace(*it.CaptionTitle) != "" {
				caption = map[string]any{"date": nil, "title": *it.CaptionTitle}
			}
			cards = append(cards, map[string]any{
				"image":   map[string]any{"media_id": it.MediaID, "alt": strPtr(it.Alt), "decorative": false},
				"caption": caption,
				"href":    cardsHref,
			})
		}
		return map[string]any{"cards": cards}, nil
	}
	cards := make([]any, 0)
	for _, c := range asSlice(s.props["cards"]) {
		m := asMap(c)
		href := m["href"]
		if href == nil {
			href = cardsHref
		}
		cards = append(cards, map[string]any{"image": m["image"], "caption": m["caption"], "href": href})
	}
	return map[string]any{"cards": cards}, nil
}

// ---- photo_story ---------------------------------------------------------------------------------------------------

// PhotoSlot is one entry of resolved.slots (keyed by the album item id).
type PhotoSlot struct {
	MediaID            int64   `json:"media_id"`
	Alt                *string `json:"alt"`
	CaptionTitle       *string `json:"caption_title"`
	CaptionDescription *string `json:"caption_description"`
}

func resolvePhotoStory(ctx context.Context, e *env, s *sectionIn) (any, error) {
	albumID, ok := asID(s.props["album_id"])
	if !ok {
		return nil, nil
	}
	items, err := e.a.d.Albums.Items(ctx, e.locale(), albumID, 0)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	e.tags.add(fmt.Sprintf("album:%d", albumID), "albums:list")
	byID := make(map[int64]entities.PublishedAlbumItem, len(items))
	for _, it := range items {
		byID[it.AlbumItemID] = it
	}
	slots := map[string]PhotoSlot{}
	for _, row := range asSlice(s.props["rows"]) {
		for _, slot := range asSlice(asMap(row)["slots"]) {
			id, ok := asID(asMap(slot)["album_item_id"])
			if !ok {
				continue
			}
			it, found := byID[id]
			if !found {
				continue
			}
			slots[strconv.FormatInt(id, 10)] = PhotoSlot{MediaID: it.MediaID, Alt: it.Alt, CaptionTitle: it.CaptionTitle, CaptionDescription: it.CaptionDescription}
		}
	}
	return map[string]any{"slots": slots}, nil
}

// ---- contact_section -----------------------------------------------------------------------------------------------

// ContactInfo is one line of resolved.info.
type ContactInfo struct {
	Icon  any    `json:"icon"`
	Label any    `json:"label"`
	Value string `json:"value"`
	Href  any    `json:"href"`
}

func resolveContactSection(ctx context.Context, e *env, s *sectionIn) (any, error) {
	e.tags.add("settings")
	info := make([]ContactInfo, 0)
	for _, it := range asSlice(s.props["info_items"]) {
		m := asMap(it)
		var value string
		var href any
		switch src := asString(m["source"]); src {
		case "contact.address":
			value = e.settings.String("contact", "address")
			href = nilIfEmpty(e.settings.String("contact", "map_url"))
		case "contact.phone":
			raw := e.settings.String("contact", "phone")
			value = raw
			if d := e.settings.String("contact", "phone_display"); d != "" {
				value = d
			}
			if raw != "" {
				href = "tel:" + raw
			}
		case "contact.email":
			value = e.settings.String("contact", "email")
			if value != "" {
				href = "mailto:" + value
			}
		case "contact.hours":
			value = e.settings.String("contact", "hours")
		case "custom":
			value = asString(m["value"])
			href = m["href"]
		default:
			continue
		}
		if strings.TrimSpace(value) == "" {
			continue
		}
		info = append(info, ContactInfo{Icon: m["icon"], Label: m["label"], Value: value, Href: href})
	}

	var form *FormDefinition
	if slug := asString(s.props["form_slug"]); slug != "" {
		f, err := e.a.d.Forms.Find(ctx, e.locale(), slug)
		switch {
		case errors.Is(err, repositories.ErrNotFound):
		case err != nil:
			return nil, err
		default:
			fields, ferr := e.a.d.Forms.Fields(ctx, e.locale(), slug)
			if ferr != nil {
				return nil, ferr
			}
			def, berr := BuildForm(*f, fields)
			if berr != nil {
				// a form whose json_schema is broken is left out of the section (P-14 answers 500 for it)
				logs.Error("contact_section: form definition cannot be built", logs.Err(berr))
			} else {
				form = def
			}
			e.tags.add(fmt.Sprintf("form:%d", f.FormID))
		}
	}
	return map[string]any{"info": info, "form": form}, nil
}

// ---- faq -----------------------------------------------------------------------------------------------------------

func resolveFAQ(ctx context.Context, e *env, s *sectionIn) (any, error) {
	group := asString(s.props["group_key"])
	if group == "" {
		return nil, nil
	}
	rows, err := e.a.d.FAQ.Items(ctx, e.locale(), group)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil // group not published (or without visible questions)
	}
	e.tags.add(fmt.Sprintf("faq:%d", rows[0].GroupID))
	p, q := collectDocLinks(FAQAnswerDocs(rows))
	if err := e.links.ensure(ctx, p, q); err != nil {
		return nil, err
	}
	return map[string]any{"items": BuildFAQItems(rows, e.links.resolver())}, nil
}

// ---- post_list -----------------------------------------------------------------------------------------------------

// Category is one entry of resolved.categories.
type Category struct {
	Key       string `json:"key"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	SortOrder int    `json:"sort_order"`
}

func resolvePostList(ctx context.Context, e *env, _ *sectionIn) (any, error) {
	e.tags.add("posts:list")
	rows, err := e.a.d.Taxonomy.Categories(ctx, e.locale())
	if err != nil {
		return nil, err
	}
	cats := make([]Category, 0, len(rows))
	for _, r := range rows {
		cats = append(cats, Category{Key: r.Key, Name: r.Name, Slug: r.Slug, SortOrder: r.SortOrder})
	}
	return map[string]any{"categories": cats}, nil
}
