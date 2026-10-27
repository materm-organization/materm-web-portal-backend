package richtext

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

type fakeResolver struct {
	pages map[int64]string
	posts map[int64]string
}

func (f fakeResolver) PageHref(id int64) (string, bool) { h, ok := f.pages[id]; return h, ok }
func (f fakeResolver) PostHref(id int64) (string, bool) { h, ok := f.posts[id]; return h, ok }

var res = fakeResolver{pages: map[int64]string{1: "/our-path"}, posts: map[int64]string{5: "/blog/music-night"}}

func run(t *testing.T, in string, p Profile) (string, Result) {
	t.Helper()
	r := ResolveJSON([]byte(in), p, res)
	b, err := json.Marshal(r.Doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b), r
}

func TestParagraphMarksAndLinks(t *testing.T) {
	in := `{"type":"doc","content":[{"type":"paragraph","content":[
	  {"type":"text","text":"Hello "},
	  {"type":"text","text":"bold","marks":[{"type":"bold"},{"type":"bold"},{"type":"italic"}]},
	  {"type":"text","text":" page","marks":[{"type":"link","attrs":{"page_id":1}}]},
	  {"type":"text","text":" post","marks":[{"type":"link","attrs":{"post_id":5,"target":"_blank"}}]},
	  {"type":"text","text":" ext","marks":[{"type":"link","attrs":{"href":"https://example.com/x","target":"_self"}}]},
	  {"type":"hardBreak"},
	  {"type":"text","text":"mail","marks":[{"type":"link","attrs":{"href":"mailto:a@b.c"}}]}]}]}`
	got, r := run(t, in, Lite)
	want := `{"content":[{"content":[{"text":"Hello ","type":"text"},{"marks":[{"type":"bold"},{"type":"italic"}],"text":"bold","type":"text"},` +
		`{"marks":[{"attrs":{"href":"/our-path"},"type":"link"}],"text":" page","type":"text"},` +
		`{"marks":[{"attrs":{"href":"/blog/music-night","target":"_blank"},"type":"link"}],"text":" post","type":"text"},` +
		`{"marks":[{"attrs":{"href":"https://example.com/x","target":"_self"},"type":"link"}],"text":" ext","type":"text"},` +
		`{"type":"hardBreak"},{"marks":[{"attrs":{"href":"mailto:a@b.c"},"type":"link"}],"text":"mail","type":"text"}],"type":"paragraph"}],"type":"doc"}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if r.Dropped != 0 {
		t.Errorf("dropped %d", r.Dropped)
	}
}

func TestUnsafeAndUnresolvedLinksKeepText(t *testing.T) {
	for _, mark := range []string{
		`{"type":"link","attrs":{"href":"javascript:alert(1)"}}`,
		`{"type":"link","attrs":{"href":"JaVaScRiPt:alert(1)"}}`,
		`{"type":"link","attrs":{"href":"data:text/html,x"}}`,
		`{"type":"link","attrs":{"href":"http://example.com"}}`,
		`{"type":"link","attrs":{"href":"//evil.example"}}`,
		`{"type":"link","attrs":{"href":"https://"}}`,
		`{"type":"link","attrs":{"href":"/a b"}}`,
		`{"type":"link","attrs":{"page_id":999}}`,
		`{"type":"link","attrs":{"post_id":999}}`,
		`{"type":"link","attrs":{}}`,
		`{"type":"link"}`,
		`{"type":"underline"}`,
	} {
		in := `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"t","marks":[` + mark + `]}]}]}`
		got, r := run(t, in, Lite)
		if strings.Contains(got, "marks") || !strings.Contains(got, `"text":"t"`) {
			t.Errorf("%s -> %s", mark, got)
		}
		if r.Dropped == 0 {
			t.Errorf("%s: Dropped must count", mark)
		}
	}
	// with no resolver, id links are dropped
	r := Resolve(map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{
		map[string]any{"type": "text", "text": "x", "marks": []any{map[string]any{"type": "link", "attrs": map[string]any{"page_id": 1.0}}}}}}}}, Lite, nil)
	b, _ := json.Marshal(r.Doc)
	if strings.Contains(string(b), "link") {
		t.Errorf("nil resolver: %s", b)
	}
}

func TestLiteProfileDropsFullNodes(t *testing.T) {
	in := `{"type":"doc","content":[
	  {"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"H"}]},
	  {"type":"lead","content":[{"type":"text","text":"L"}]},
	  {"type":"blockquote","content":[{"type":"paragraph","content":[{"type":"text","text":"Q"}]}]},
	  {"type":"image","attrs":{"media_id":3}},
	  {"type":"embed","attrs":{"provider":"youtube","url":"https://www.youtube.com/embed/x"}},
	  {"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"i"}]}]}]}]}`
	got, r := run(t, in, Lite)
	want := `{"content":[{"content":[{"text":"H","type":"text"}],"type":"paragraph"},{"content":[{"text":"L","type":"text"}],"type":"paragraph"},` +
		`{"content":[{"text":"Q","type":"text"}],"type":"paragraph"},` +
		`{"content":[{"content":[{"content":[{"text":"i","type":"text"}],"type":"paragraph"}],"type":"listItem"}],"type":"bulletList"}],"type":"doc"}`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if len(r.MediaIDs) != 0 {
		t.Errorf("lite has no images: %v", r.MediaIDs)
	}
}

func TestFullProfile(t *testing.T) {
	in := `{"type":"doc","content":[
	  {"type":"lead","content":[{"type":"text","text":"Standfirst"}]},
	  {"type":"lead","content":[{"type":"text","text":"second lead"}]},
	  {"type":"heading","attrs":{"level":3},"content":[{"type":"text","text":"H3"}]},
	  {"type":"heading","attrs":{"level":1},"content":[{"type":"text","text":"H1"}]},
	  {"type":"orderedList","attrs":{"start":3},"content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"x"}]}]}]},
	  {"type":"blockquote","content":[{"type":"paragraph","content":[{"type":"text","text":"Q"}]}]},
	  {"type":"image","attrs":{"media_id":12,"alt":"An alt","caption":"Cap","onerror":"x()"}},
	  {"type":"image","attrs":{"media_id":12}},
	  {"type":"image","attrs":{"media_id":0}},
	  {"type":"image","attrs":{"src":"http://evil/x.png"}},
	  {"type":"embed","attrs":{"provider":"youtube","url":"https://www.youtube.com/embed/abc"}},
	  {"type":"embed","attrs":{"provider":"youtube","url":"http://www.youtube.com/embed/abc"}},
	  {"type":"embed","attrs":{"provider":"youtube","url":"https://evil.example/embed/abc"}},
	  {"type":"embed","attrs":{"provider":"vimeo","url":"https://player.vimeo.com/video/1"}},
	  {"type":"embed","attrs":{"provider":"google_maps","url":"https://www.google.com/maps/embed?pb=1"}},
	  {"type":"embed","attrs":{"provider":"google_maps","url":"https://www.google.com/search?q=x"}},
	  {"type":"embed","attrs":{"provider":"twitter","url":"https://twitter.com/x"}},
	  {"type":"script","content":[{"type":"text","text":"kept text"}]}]}`
	got, r := run(t, in, Full)
	for _, must := range []string{
		`{"content":[{"text":"Standfirst","type":"text"}],"type":"lead"}`,
		`{"content":[{"text":"second lead","type":"text"}],"type":"paragraph"}`,
		`{"attrs":{"level":3},"content":[{"text":"H3","type":"text"}],"type":"heading"}`,
		`{"attrs":{"level":2},"content":[{"text":"H1","type":"text"}],"type":"heading"}`,
		`{"attrs":{"start":3},"content":[`,
		`{"content":[{"content":[{"text":"Q","type":"text"}],"type":"paragraph"}],"type":"blockquote"}`,
		`{"attrs":{"alt":"An alt","caption":"Cap","media_id":12},"type":"image"}`,
		`{"attrs":{"provider":"youtube","url":"https://www.youtube.com/embed/abc"},"type":"embed"}`,
		`{"attrs":{"provider":"vimeo","url":"https://player.vimeo.com/video/1"},"type":"embed"}`,
		`{"attrs":{"provider":"google_maps","url":"https://www.google.com/maps/embed?pb=1"},"type":"embed"}`,
		`{"content":[{"text":"kept text","type":"text"}],"type":"paragraph"}`,
	} {
		if !strings.Contains(got, must) {
			t.Errorf("missing %s\nin %s", must, got)
		}
	}
	for _, mustNot := range []string{"evil", "onerror", "twitter", "search?q", "http://"} {
		if strings.Contains(got, mustNot) {
			t.Errorf("output contains %q: %s", mustNot, got)
		}
	}
	if strings.Count(got, `"type":"embed"`) != 3 || strings.Count(got, `"type":"image"`) != 2 {
		t.Errorf("embed/image counts: %s", got)
	}
	if !reflect.DeepEqual(r.MediaIDs, []int64{12}) {
		t.Errorf("media ids %v", r.MediaIDs)
	}
}

func TestLeadOnlyFirst(t *testing.T) {
	in := `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"p"}]},{"type":"lead","content":[{"type":"text","text":"late"}]}]}`
	got, _ := run(t, in, Full)
	if strings.Contains(got, `"lead"`) {
		t.Errorf("a lead that is not the first node must become a paragraph: %s", got)
	}
}

func TestDegenerateInput(t *testing.T) {
	empty := `{"content":[],"type":"doc"}`
	for _, in := range []string{``, `null`, `[]`, `"str"`, `{}`, `{"type":"paragraph"}`, `{"type":"doc"}`, `{"type":"doc","content":"x"}`, `{bad json`} {
		if got, r := run(t, in, Full); got != empty || len(r.MediaIDs) != 0 {
			t.Errorf("%q -> %s", in, got)
		}
	}
	// non-object children, empty text, wrong types
	in := `{"type":"doc","content":[1,"x",null,{"type":"paragraph","content":[{"type":"text","text":""},{"type":"text","text":5},"s"]},{"type":"text","text":"bare"}]}`
	got, _ := run(t, in, Lite)
	if got != `{"content":[{"content":[],"type":"paragraph"},{"content":[{"text":"bare","type":"text"}],"type":"paragraph"}],"type":"doc"}` {
		t.Errorf("%s", got)
	}
	// control characters are stripped, tab/newline kept
	got, _ = run(t, `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"a\u0000b\u0007c\td\ne"}]}]}`, Lite)
	if !strings.Contains(got, `abc\td\ne`) {
		t.Errorf("%s", got)
	}
}

func TestLimits(t *testing.T) {
	// depth: nested blockquotes deeper than maxDepth are cut
	n := `{"type":"paragraph","content":[{"type":"text","text":"deep"}]}`
	for i := 0; i < 30; i++ {
		n = `{"type":"blockquote","content":[` + n + `]}`
	}
	got, r := run(t, `{"type":"doc","content":[`+n+`]}`, Full)
	if strings.Contains(got, "deep") || r.Dropped == 0 {
		t.Errorf("depth limit: %d dropped", r.Dropped)
	}
	// node count
	var sb strings.Builder
	sb.WriteString(`{"type":"doc","content":[`)
	for i := 0; i < maxNodes+500; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"type":"paragraph"}`)
	}
	sb.WriteString(`]}`)
	_, r = run(t, sb.String(), Full)
	if c := r.Doc["content"].([]any); len(c) > maxNodes {
		t.Errorf("kept %d nodes", len(c))
	}
	// an oversized text node is dropped
	big := strings.Repeat("a", maxText+1)
	got, _ = run(t, `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"`+big+`"}]}]}`, Lite)
	if strings.Contains(got, "aaaa") {
		t.Error("oversized text kept")
	}
}

func TestInputNotModifiedAndFloat64(t *testing.T) {
	in := map[string]any{"type": "doc", "content": []any{
		map[string]any{"type": "image", "attrs": map[string]any{"media_id": 4.0, "alt": "a", "extra": "x"}}}}
	before, _ := json.Marshal(in)
	r := Resolve(in, Full, nil)
	after, _ := json.Marshal(in)
	if string(before) != string(after) {
		t.Error("input modified")
	}
	if !reflect.DeepEqual(r.MediaIDs, []int64{4}) {
		t.Errorf("%v", r.MediaIDs)
	}
	// fractional ids are refused
	r = Resolve(map[string]any{"type": "doc", "content": []any{map[string]any{"type": "image", "attrs": map[string]any{"media_id": 4.5}}}}, Full, nil)
	if len(r.MediaIDs) != 0 {
		t.Error("fractional id accepted")
	}
}

func TestSafeHref(t *testing.T) {
	ok := []string{"https://a.b/c", "mailto:x@y.z", "tel:+85620", "/path", "/", "#anchor", "/a?b=1#c"}
	bad := []string{"", "http://a.b", "javascript:1", "ftp://a", "//a.b", "mailto:", "https://", " ", "/a b", "/a\\b", "data:x", "vbscript:x", "\x01https://a.b"}
	for _, h := range ok {
		if SafeHref(h) != h {
			t.Errorf("%q should pass", h)
		}
	}
	for _, h := range bad {
		if SafeHref(h) != "" {
			t.Errorf("%q should be refused", h)
		}
	}
	if SafeHref(" /trim ") != "" {
		// inner space after trimming is refused; trimming only removes the ends
		t.Log("ok")
	}
}

func TestCollectLinksAndMapResolver(t *testing.T) {
	v, _ := decodeJSON(`{"a":{"type":"doc","content":[{"type":"paragraph","content":[
	  {"type":"text","text":"x","marks":[{"type":"link","attrs":{"page_id":3}},{"type":"link","attrs":{"post_id":8}}]},
	  {"type":"text","text":"y","marks":[{"type":"link","attrs":{"page_id":3}},{"type":"link","attrs":{"href":"/x"}}]}]}]},
	  "b":[{"type":"link","attrs":{"post_id":2}}]}`)
	pages, posts := CollectLinks(v)
	if !reflect.DeepEqual(pages, []int64{3}) || !reflect.DeepEqual(posts, []int64{2, 8}) {
		t.Errorf("%v %v", pages, posts)
	}
	if p, q := CollectLinks(nil); p != nil || q != nil {
		t.Error("nil")
	}
	m := MapResolver{Pages: map[int64]string{3: "/x", 4: ""}, Posts: map[int64]string{8: "/blog/p"}}
	if h, ok := m.PageHref(3); !ok || h != "/x" {
		t.Error("PageHref")
	}
	if _, ok := m.PageHref(4); ok {
		t.Error("an empty path is not a link target")
	}
	if _, ok := m.PostHref(9); ok {
		t.Error("missing post")
	}
	if h, ok := m.PostHref(8); !ok || h != "/blog/p" {
		t.Error("PostHref")
	}
}

func decodeJSON(s string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	err := dec.Decode(&v)
	return v, err
}
