package assemble

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/materm-organization/materm-web-portal-backend/pkg/richtext"
)

// transformer is the structural half of the props walk (the locale maps were already collapsed to one value
// by localize.Collapse): image placements get their final shape, links become hrefs, an object that holds a
// link absorbs it (`cta {label, link}` -> `cta {label, href, open_in_new_tab, rel}`), rich text documents go
// through the allow-list sanitiser and media ids are collected.
//
// It runs twice per page: once with collecting=true, which only gathers the page and post ids that links
// refer to (so one batched lookup can serve the whole page), and once for real.
type transformer struct {
	collecting bool
	pageIDs    map[int64]struct{}
	postIDs    map[int64]struct{}
	media      map[int64]struct{}
	pages      map[int64]string // page id -> path of a published page
	resolver   richtext.Resolver
}

func newTransformer() *transformer {
	return &transformer{pageIDs: map[int64]struct{}{}, postIDs: map[int64]struct{}{}, media: map[int64]struct{}{}}
}

// apply returns the transformed copy of v for node n. It never modifies v.
func (t *transformer) apply(n *node, v any) any {
	if v == nil || n == nil {
		return v
	}
	switch n.kind {
	case kImage:
		return t.image(v)
	case kLink:
		return t.link(v)
	case kRich:
		return t.rich(n, v)
	case kMedia:
		if id, ok := asID(v); ok {
			t.media[id] = struct{}{}
		}
		return v
	case kArray:
		arr, ok := v.([]any)
		if !ok {
			return v
		}
		out := make([]any, len(arr))
		for i, el := range arr {
			out[i] = t.apply(n.items, el)
		}
		return out
	case kObject:
		m, ok := v.(map[string]any)
		if !ok {
			return v
		}
		out := make(map[string]any, len(m))
		for k, val := range m {
			if child, known := n.props[k]; known {
				out[k] = t.apply(child, val)
			} else {
				out[k] = val // unknown keys are kept as they are
			}
		}
		if l, ok := out["link"].(map[string]any); ok {
			if _, has := l["href"]; has {
				delete(out, "link")
				for k, val := range l {
					out[k] = val
				}
			}
		}
		return out
	}
	return v
}

// image: {media_id, alt, decorative}. The alt was collapsed already; an empty alt of a decorative image is "",
// of a normal image null (the component then uses the alt of the media asset).
func (t *transformer) image(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	decorative, _ := m["decorative"].(bool)
	var alt any
	if s, isStr := m["alt"].(string); isStr && strings.TrimSpace(s) != "" {
		alt = s
	} else if decorative {
		alt = ""
	}
	if id, ok := asID(m["media_id"]); ok {
		t.media[id] = struct{}{}
	}
	return map[string]any{"media_id": m["media_id"], "alt": alt, "decorative": decorative}
}

// link: {href, open_in_new_tab, rel}. A page link to a page that is not published has href null.
func (t *transformer) link(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	typ, _ := m["type"].(string)
	if typ == "page" {
		var href any
		if id, ok := asID(m["page_id"]); ok {
			if t.collecting {
				t.pageIDs[id] = struct{}{}
			} else if p, found := t.pages[id]; found {
				h := p
				if a, _ := m["anchor"].(string); a != "" {
					h += "#" + a
				}
				href = h
			}
		}
		return map[string]any{"href": href, "open_in_new_tab": false, "rel": []any{}}
	}
	var href any
	if u, _ := m["url"].(string); u != "" {
		href = u
	}
	rel := []any{}
	if r, ok := m["rel"].([]any); ok {
		rel = append(rel, r...)
	}
	nt, _ := m["open_in_new_tab"].(bool)
	return map[string]any{"href": href, "open_in_new_tab": nt, "rel": rel}
}

func (t *transformer) rich(n *node, v any) any {
	if t.collecting {
		pages, posts := richtext.CollectLinks(v)
		for _, id := range pages {
			t.pageIDs[id] = struct{}{}
		}
		for _, id := range posts {
			t.postIDs[id] = struct{}{}
		}
		return v
	}
	if _, isDoc := v.(map[string]any); !isDoc {
		return nil
	}
	res := richtext.Resolve(v, n.profile, t.resolver)
	for _, id := range res.MediaIDs {
		t.media[id] = struct{}{}
	}
	return res.Doc
}

// asID reads a positive integer id from a decoded JSON value.
func asID(v any) (int64, bool) {
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

func sortedIDs(m map[int64]struct{}) []int64 {
	out := make([]int64, 0, len(m))
	for id := range m {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
