// Package richtext is the read-side sanitiser of rich text (doc/08 sections 2.3.1 and 2.4; doc/04 section
// 3.7). The admin backend validates a document when it is saved; the portal checks it again when it is read
// (the second layer of defence) and prepares it for the frontend:
//
//   - the output is a JSON document ({"type":"doc","content":[...]}) built only from an allow-list of nodes,
//     marks and attributes of the profile (lite or full). There is no HTML anywhere: the frontend renders
//     React elements from this tree;
//   - a node or mark outside the allow-list is dropped (the text of a dropped node is kept in place);
//   - link marks are resolved on the server: {page_id} / {post_id} become an href through the injected
//     Resolver; a link that cannot be resolved, or whose href has a scheme other than https, mailto, tel or a
//     site-relative path, loses the mark but keeps its text;
//   - image nodes (full profile) are kept with their media_id, which Result.MediaIDs lists so that the caller
//     can add them to data.media;
//   - embeds are kept only for youtube, vimeo and google_maps hosts, https only.
//
// Input is any decoded JSON (numbers may be json.Number or float64); malformed parts are dropped, never
// panicked on. The input is not modified.
package richtext

import (
	"encoding/json"
	"net/url"
	"sort"
	"strings"
	"unicode"
)

// Profile selects the node allow-list.
type Profile string

const (
	// Lite: paragraph, lists, hardBreak, text; marks bold, italic, link (FAQ answers, intro text, about blocks).
	Lite Profile = "lite"
	// Full: lite plus lead, heading (2-3), blockquote, image, embed (post bodies).
	Full Profile = "full"
)

// Resolver turns internal references of link marks into hrefs.
type Resolver interface {
	// PageHref returns the path of a published page ("/our-path"); ok=false when it is not published.
	PageHref(pageID int64) (string, bool)
	// PostHref returns the site path of a published post ("/blog/<slug>"); ok=false when it is not published.
	PostHref(postID int64) (string, bool)
}

// Result is a sanitised document.
type Result struct {
	// Doc is {"type":"doc","content":[...]}; content is never nil.
	Doc map[string]any
	// MediaIDs lists the media_id of the kept image nodes, sorted, without duplicates.
	MediaIDs []int64
	// Dropped counts the nodes, marks and attributes removed by the allow-list (for logs and tests).
	Dropped int
}

// Limits against hostile or corrupt documents.
const (
	maxDepth = 10
	maxNodes = 10000
	maxText  = 200_000 // bytes of one text node
)

// Resolve sanitises doc for profile. r may be nil (links by page_id / post_id are then dropped). A nil or
// non-object doc yields an empty document.
func Resolve(doc any, profile Profile, r Resolver) Result {
	s := &state{profile: profile, r: r, ids: map[int64]struct{}{}}
	m, _ := doc.(map[string]any)
	var content []any
	if t, _ := m["type"].(string); t == "doc" {
		if arr, ok := m["content"].([]any); ok {
			content = s.blocks(arr, 1, true)
		}
	}
	if content == nil {
		content = []any{}
	}
	res := Result{Doc: map[string]any{"type": "doc", "content": content}, Dropped: s.dropped}
	for id := range s.ids {
		res.MediaIDs = append(res.MediaIDs, id)
	}
	sort.Slice(res.MediaIDs, func(i, j int) bool { return res.MediaIDs[i] < res.MediaIDs[j] })
	return res
}

// ResolveJSON is Resolve over raw JSON (the jsonb column). Invalid JSON gives an empty document.
func ResolveJSON(raw []byte, profile Profile, r Resolver) Result {
	if len(raw) == 0 {
		return Resolve(nil, profile, r)
	}
	var v any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return Resolve(nil, profile, r)
	}
	return Resolve(v, profile, r)
}

type state struct {
	profile  Profile
	r        Resolver
	ids      map[int64]struct{}
	nodes    int
	dropped  int
	leadSeen bool
}

func (s *state) full() bool { return s.profile == Full }

// blocks sanitises the children of a block container (doc, listItem, blockquote). Inline content that is not
// inside a paragraph (a stray text node left by a dropped wrapper) is wrapped into a paragraph so that the
// tree stays valid. top is true for the children of the doc (where `lead` is allowed).
func (s *state) blocks(in []any, depth int, top bool) []any {
	out := []any{}
	var pending []any // inline nodes waiting for a paragraph
	flush := func() {
		if len(pending) > 0 {
			out = append(out, map[string]any{"type": "paragraph", "content": pending})
			pending = nil
		}
	}
	for i, n := range in {
		for _, b := range s.block(n, depth, top && i == 0) {
			if isInline(b) {
				pending = append(pending, b)
				continue
			}
			flush()
			out = append(out, b)
		}
	}
	flush()
	return out
}

func isInline(n any) bool {
	m, _ := n.(map[string]any)
	t, _ := m["type"].(string)
	return t == "text" || t == "hardBreak"
}

// block returns the sanitised form of one block-level node: normally one node, none when dropped, and the
// hoisted children when the node itself is not allowed.
func (s *state) block(n any, depth int, first bool) []any {
	m, ok := n.(map[string]any)
	if !ok || depth > maxDepth || s.nodes >= maxNodes {
		s.dropped++
		return nil
	}
	s.nodes++
	t, _ := m["type"].(string)
	switch t {
	case "paragraph":
		return []any{s.textBlock("paragraph", m, nil, depth)}
	case "lead":
		if !s.full() || !first || s.leadSeen {
			return s.hoist(m, depth)
		}
		s.leadSeen = true
		return []any{s.textBlock("lead", m, nil, depth)}
	case "heading":
		if !s.full() {
			return s.hoist(m, depth)
		}
		lvl, ok := intAttr(m, "level")
		if !ok || (lvl != 2 && lvl != 3) {
			s.dropped++
			lvl = 2
		}
		return []any{s.textBlock("heading", m, map[string]any{"level": lvl}, depth)}
	case "bulletList", "orderedList":
		var attrs map[string]any
		if t == "orderedList" {
			if start, ok := intAttr(m, "start"); ok && start >= 1 && start <= 1_000_000 {
				attrs = map[string]any{"start": start}
			}
		}
		items := []any{}
		for _, c := range children(m) {
			cm, _ := c.(map[string]any)
			if ct, _ := cm["type"].(string); ct != "listItem" {
				s.dropped++
				continue
			}
			s.nodes++
			items = append(items, map[string]any{"type": "listItem", "content": s.blocks(children(cm), depth+2, false)})
		}
		return []any{node(t, attrs, items)}
	case "listItem":
		// a listItem outside a list: keep the content
		return s.hoist(m, depth)
	case "blockquote":
		if !s.full() {
			return s.hoist(m, depth)
		}
		return []any{node("blockquote", nil, s.blocks(children(m), depth+1, false))}
	case "image":
		if !s.full() {
			s.dropped++
			return nil
		}
		return s.image(m)
	case "embed":
		if !s.full() {
			s.dropped++
			return nil
		}
		return s.embed(m)
	case "text", "hardBreak":
		if t == "hardBreak" {
			return []any{map[string]any{"type": "hardBreak"}}
		}
		if tn := s.text(m); tn != nil {
			return []any{tn}
		}
		return nil
	default:
		return s.hoist(m, depth)
	}
}

// hoist drops a node that is not allowed here and keeps its children.
func (s *state) hoist(m map[string]any, depth int) []any {
	s.dropped++
	return s.blocks(children(m), depth+1, false)
}

func node(t string, attrs map[string]any, content []any) map[string]any {
	n := map[string]any{"type": t, "content": content}
	if len(attrs) > 0 {
		n["attrs"] = attrs
	}
	return n
}

// textBlock builds a paragraph / lead / heading from inline children.
func (s *state) textBlock(t string, m map[string]any, attrs map[string]any, depth int) map[string]any {
	return node(t, attrs, s.inlines(children(m), depth+1))
}

func (s *state) inlines(in []any, depth int) []any {
	out := []any{}
	for _, c := range in {
		cm, ok := c.(map[string]any)
		if !ok || depth > maxDepth || s.nodes >= maxNodes {
			s.dropped++
			continue
		}
		s.nodes++
		switch t, _ := cm["type"].(string); t {
		case "text":
			if tn := s.text(cm); tn != nil {
				out = append(out, tn)
			}
		case "hardBreak":
			out = append(out, map[string]any{"type": "hardBreak"})
		default:
			// an unknown inline wrapper: keep what is inside it
			s.dropped++
			out = append(out, s.inlines(children(cm), depth+1)...)
		}
	}
	return out
}

func (s *state) text(m map[string]any) map[string]any {
	txt, ok := m["text"].(string)
	if !ok || txt == "" || len(txt) > maxText {
		s.dropped++
		return nil
	}
	n := map[string]any{"type": "text", "text": cleanText(txt)}
	var marks []any
	seen := map[string]bool{}
	for _, mk := range anySlice(m["marks"]) {
		mm, _ := mk.(map[string]any)
		switch t, _ := mm["type"].(string); t {
		case "bold", "italic":
			if !seen[t] {
				seen[t] = true
				marks = append(marks, map[string]any{"type": t})
			}
		case "link":
			if !seen[t] {
				if lm := s.link(mm); lm != nil {
					seen[t] = true
					marks = append(marks, lm)
				}
			}
		default:
			s.dropped++
		}
	}
	if len(marks) > 0 {
		n["marks"] = marks
	}
	return n
}

// cleanText removes control characters other than tab and newline.
func cleanText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || !unicode.IsControl(r) {
			return r
		}
		return -1
	}, s)
}

// link builds the link mark or returns nil (the text is then kept without the mark).
func (s *state) link(mm map[string]any) map[string]any {
	attrs, _ := mm["attrs"].(map[string]any)
	href := ""
	if h, ok := attrs["href"].(string); ok && h != "" {
		href = SafeHref(h)
	} else if id, ok := intAttr(mm, "page_id"); ok && s.r != nil {
		href, _ = s.r.PageHref(int64(id))
	} else if id, ok := intAttr(mm, "post_id"); ok && s.r != nil {
		href, _ = s.r.PostHref(int64(id))
	}
	if href == "" {
		s.dropped++
		return nil
	}
	out := map[string]any{"href": href}
	if tg, _ := attrs["target"].(string); tg == "_blank" || tg == "_self" {
		out["target"] = tg
	}
	return map[string]any{"type": "link", "attrs": out}
}

// SafeHref returns h when it is an https URL, a mailto:/tel: link or a site-relative path ("/x", "#x"), else "".
// "http:", "javascript:", "data:", protocol-relative "//host" and anything with control characters are refused.
func SafeHref(h string) string {
	h = strings.TrimSpace(h)
	if h == "" || len(h) > 2048 || strings.IndexFunc(h, unicode.IsControl) >= 0 || strings.ContainsAny(h, " \\") {
		return ""
	}
	switch {
	case strings.HasPrefix(h, "//"):
		return ""
	case strings.HasPrefix(h, "/") || strings.HasPrefix(h, "#"):
		return h
	}
	u, err := url.Parse(h)
	if err != nil {
		return ""
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		if u.Host == "" {
			return ""
		}
		return h
	case "mailto", "tel":
		if u.Opaque == "" {
			return ""
		}
		return h
	}
	return ""
}

func (s *state) image(m map[string]any) []any {
	attrs, _ := m["attrs"].(map[string]any)
	id, ok := intAttr(m, "media_id")
	if !ok || id < 1 {
		s.dropped++
		return nil
	}
	out := map[string]any{"media_id": id}
	if alt, ok := attrs["alt"].(string); ok && alt != "" {
		out["alt"] = cleanText(alt)
	}
	if c, ok := attrs["caption"].(string); ok && c != "" {
		out["caption"] = cleanText(c)
	}
	s.ids[int64(id)] = struct{}{}
	return []any{map[string]any{"type": "image", "attrs": out}}
}

var embedHosts = map[string][]string{
	"youtube":     {"www.youtube.com", "youtube.com", "www.youtube-nocookie.com", "youtu.be"},
	"vimeo":       {"vimeo.com", "player.vimeo.com"},
	"google_maps": {"www.google.com", "maps.google.com", "maps.app.goo.gl"},
}

func (s *state) embed(m map[string]any) []any {
	attrs, _ := m["attrs"].(map[string]any)
	provider, _ := attrs["provider"].(string)
	raw, _ := attrs["url"].(string)
	hosts, known := embedHosts[provider]
	u, err := url.Parse(strings.TrimSpace(raw))
	if !known || err != nil || u.Scheme != "https" || u.User != nil {
		s.dropped++
		return nil
	}
	host := strings.ToLower(u.Hostname())
	okHost := false
	for _, h := range hosts {
		if host == h {
			okHost = true
		}
	}
	// www.google.com is shared with search: only its /maps pages are maps
	if provider == "google_maps" && host == "www.google.com" && !strings.HasPrefix(u.Path, "/maps") {
		okHost = false
	}
	if !okHost {
		s.dropped++
		return nil
	}
	return []any{map[string]any{"type": "embed", "attrs": map[string]any{"provider": provider, "url": u.String()}}}
}

func children(m map[string]any) []any { return anySlice(m["content"]) }

func anySlice(v any) []any {
	s, _ := v.([]any)
	return s
}

// intAttr reads an integer attribute of a node (attrs.<key>, or the key on the node itself for the ids a
// link mark carries). Fractions and non-numbers are refused.
func intAttr(n map[string]any, key string) (int, bool) {
	attrs, _ := n["attrs"].(map[string]any)
	v, present := attrs[key]
	if !present {
		v, present = n[key]
	}
	if !present {
		return 0, false
	}
	switch x := v.(type) {
	case json.Number:
		i, err := x.Int64()
		return int(i), err == nil
	case float64:
		i := int(x)
		return i, float64(i) == x
	case int:
		return x, true
	case int64:
		return int(x), true
	}
	return 0, false
}
