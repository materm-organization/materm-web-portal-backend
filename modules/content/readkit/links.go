package readkit

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/repositories"
	"github.com/materm-organization/materm-web-portal-backend/pkg/richtext"
)

// BlogPath is the site path under which posts live: the frontend route /blog/[slug] is in the code (doc/08
// section 0), and the 308 of P-08 names it explicitly.
const BlogPath = "/blog"

// PostHref is the site path of a post.
func PostHref(slug string) string { return BlogPath + "/" + slug }

// DecodeJSON decodes a jsonb column for CollectLinks (numbers stay json.Number, as in richtext.ResolveJSON).
// nil on empty or invalid input.
func DecodeJSON(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil
	}
	return v
}

// Links is the richtext.Resolver of one response plus what it had to look at: UsedPages / UsedPosts say which
// cache tags the response depends on (a link target that changes its path must invalidate it).
type Links struct {
	richtext.MapResolver
	UsedPages bool
	UsedPosts bool
}

// ResolveLinks loads, with one query each, the published pages and posts that the link marks of docs refer to
// and returns the resolver for richtext.Resolve. Targets that are not published are absent (the link is then
// dropped by the sanitiser, doc/04 section 6.3).
func ResolveLinks(ctx context.Context, set *repositories.Set, locale string, docs ...any) (Links, error) {
	l := Links{MapResolver: richtext.MapResolver{Pages: map[int64]string{}, Posts: map[int64]string{}}}
	var pageIDs, postIDs []int64
	for _, d := range docs {
		p, q := richtext.CollectLinks(d)
		pageIDs = append(pageIDs, p...)
		postIDs = append(postIDs, q...)
	}
	if len(pageIDs) > 0 {
		l.UsedPages = true
		pages, err := set.Pages.ByIDs(ctx, locale, pageIDs)
		if err != nil {
			return l, err
		}
		for _, p := range pages {
			l.Pages[p.PageID] = p.Path
		}
	}
	if len(postIDs) > 0 {
		l.UsedPosts = true
		posts, err := set.Posts.ByIDs(ctx, locale, postIDs)
		if err != nil {
			return l, err
		}
		for _, p := range posts {
			l.Posts[p.PostID] = PostHref(p.Slug)
		}
	}
	return l, nil
}
