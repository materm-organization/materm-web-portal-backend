package richtext

import "sort"

// MapResolver is a Resolver over maps that the caller filled with one batched lookup (CollectLinks tells
// which ids to load): page id -> path, post id -> "/blog/<slug>". Missing ids are "not published".
type MapResolver struct {
	Pages map[int64]string
	Posts map[int64]string
}

// PageHref implements Resolver.
func (m MapResolver) PageHref(id int64) (string, bool) { h, ok := m.Pages[id]; return h, ok && h != "" }

// PostHref implements Resolver.
func (m MapResolver) PostHref(id int64) (string, bool) { h, ok := m.Posts[id]; return h, ok && h != "" }

// CollectLinks walks a decoded document (or any decoded JSON containing documents) and returns the page ids and
// post ids that link marks refer to, sorted and without duplicates. Run it before Resolve to load all targets
// with one query each.
func CollectLinks(v any) (pageIDs, postIDs []int64) {
	pages, posts := map[int64]struct{}{}, map[int64]struct{}{}
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case map[string]any:
			if typ, _ := t["type"].(string); typ == "link" {
				if id, ok := intAttr(t, "page_id"); ok && id > 0 {
					pages[int64(id)] = struct{}{}
				}
				if id, ok := intAttr(t, "post_id"); ok && id > 0 {
					posts[int64(id)] = struct{}{}
				}
			}
			for _, c := range t {
				walk(c)
			}
		case []any:
			for _, c := range t {
				walk(c)
			}
		}
	}
	walk(v)
	return sortedKeys(pages), sortedKeys(posts)
}

func sortedKeys(m map[int64]struct{}) []int64 {
	if len(m) == 0 {
		return nil
	}
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
