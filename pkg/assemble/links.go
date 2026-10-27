package assemble

import (
	"context"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/entities"
	"github.com/materm-organization/materm-web-portal-backend/pkg/richtext"
)

// BlogBasePath is the site path under which posts live ("/blog/<slug>"): the path of the blog page is fixed
// by the reserved-path rule of cms_pages (`path !~ '^/blog/'`) and by the frontend route. Lists, the blog
// strip and rich text links all build post hrefs from it.
const BlogBasePath = "/blog"

// PostHref is the site path of a post.
func PostHref(slug string) string { return BlogBasePath + "/" + slug }

// PageLookup resolves page ids to published pages (modules/content PageRepository.ByIDs).
type PageLookup interface {
	ByIDs(ctx context.Context, locale string, ids []int64) ([]entities.PublishedPage, error)
}

// PostLookup resolves post ids to published posts (modules/content PostRepository.ByIDs).
type PostLookup interface {
	ByIDs(ctx context.Context, locale string, ids []int64) ([]entities.PublishedPostCard, error)
}

// linkTargets loads, once and in batches, the targets that links and rich text marks point to. Ids that are not
// published are simply absent (the link then loses its href).
type linkTargets struct {
	pagesSrc PageLookup
	postsSrc PostLookup
	locale   string

	pages map[int64]string
	posts map[int64]string
	askP  map[int64]struct{}
	askQ  map[int64]struct{}
}

func newLinkTargets(pages PageLookup, posts PostLookup, locale string) *linkTargets {
	return &linkTargets{pagesSrc: pages, postsSrc: posts, locale: locale,
		pages: map[int64]string{}, posts: map[int64]string{}, askP: map[int64]struct{}{}, askQ: map[int64]struct{}{}}
}

// ensure loads the ids that were not asked for before.
func (l *linkTargets) ensure(ctx context.Context, pageIDs, postIDs []int64) error {
	var needP, needQ []int64
	for _, id := range pageIDs {
		if _, seen := l.askP[id]; !seen {
			l.askP[id] = struct{}{}
			needP = append(needP, id)
		}
	}
	for _, id := range postIDs {
		if _, seen := l.askQ[id]; !seen {
			l.askQ[id] = struct{}{}
			needQ = append(needQ, id)
		}
	}
	if len(needP) > 0 && l.pagesSrc != nil {
		rows, err := l.pagesSrc.ByIDs(ctx, l.locale, needP)
		if err != nil {
			return err
		}
		for _, r := range rows {
			l.pages[r.PageID] = r.Path
		}
	}
	if len(needQ) > 0 && l.postsSrc != nil {
		rows, err := l.postsSrc.ByIDs(ctx, l.locale, needQ)
		if err != nil {
			return err
		}
		for _, r := range rows {
			l.posts[r.PostID] = PostHref(r.Slug)
		}
	}
	return nil
}

func (l *linkTargets) resolver() richtext.MapResolver {
	return richtext.MapResolver{Pages: l.pages, Posts: l.posts}
}

// NewLinkResolver loads the pages and posts that the link marks of docs (decoded rich text documents, or any
// decoded JSON containing them) refer to, with one query each, and returns the resolver for richtext.Resolve.
// FAQ answers and post bodies use it.
func NewLinkResolver(ctx context.Context, pages PageLookup, posts PostLookup, locale string, docs ...any) (richtext.Resolver, error) {
	t := newLinkTargets(pages, posts, locale)
	pageIDs, postIDs := collectDocLinks(docs)
	if err := t.ensure(ctx, pageIDs, postIDs); err != nil {
		return nil, err
	}
	return t.resolver(), nil
}

// collectDocLinks gathers the page and post ids that the link marks of docs refer to.
func collectDocLinks(docs []any) (pageIDs, postIDs []int64) {
	for _, d := range docs {
		p, q := richtext.CollectLinks(d)
		pageIDs = append(pageIDs, p...)
		postIDs = append(postIDs, q...)
	}
	return pageIDs, postIDs
}
