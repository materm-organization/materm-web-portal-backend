package media

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestURL(t *testing.T) {
	b := NewBuilder(Config{PathIP: "https://files.example.com/base/", StaticPrefix: "static/"})
	cases := []struct {
		key  string
		want string
		ok   bool
	}{
		{"static/images/site/home/hero-bg.png", "/images/site/home/hero-bg.png", true},
		{"static/a b/c#d.png", "", false}, // '#' is refused outright
		{"static/a b/c.png", "/a%20b/c.png", true},
		{"static/", "", false},
		{"buckets/x/uploads/2026/photo.webp", "https://files.example.com/base/buckets/x/uploads/2026/photo.webp", true},
		{"photo ü.jpg", "https://files.example.com/base/photo%20%C3%BC.jpg", true},
		{"", "", false},
		{"/abs/path.png", "", false},
		{"a/../b.png", "", false},
		{"a//b.png", "", false},
		{"./b.png", "", false},
		{"https://evil.example/x.png", "", false},
		{"a\\b.png", "", false},
		{"a?x=1.png", "", false},
		{strings.Repeat("a", 513), "", false},
		{"staticx/photo.png", "https://files.example.com/base/staticx/photo.png", true}, // not the reserved prefix
	}
	for _, tc := range cases {
		got, ok := b.URL(tc.key)
		if ok != tc.ok || got != tc.want {
			t.Errorf("URL(%q) = %q,%v want %q,%v", tc.key, got, ok, tc.want, tc.ok)
		}
	}
}

func TestBuilderConfig(t *testing.T) {
	// blank / invalid PathIP: file-service keys have no URL, static ones still do
	for _, p := range []string{"", "not a url", "ftp://x/y", "https://", "https://u:p@host/", "https://h/?q=1"} {
		b := NewBuilder(Config{PathIP: p})
		if _, ok := b.URL("uploads/a.png"); ok {
			t.Errorf("PathIP %q must not produce URLs", p)
		}
		if u, ok := b.URL("static/a.png"); !ok || u != "/a.png" {
			t.Errorf("static must work with PathIP %q", p)
		}
	}
	// custom prefix without trailing slash, nested
	b := NewBuilder(Config{StaticPrefix: "local/img"})
	if u, ok := b.URL("local/img/x/y.png"); !ok || u != "/x/y.png" {
		t.Errorf("nested prefix: %q %v", u, ok)
	}
	// http base is allowed (the risk is documented, not blocked)
	if u, _ := NewBuilder(Config{PathIP: "http://10.0.0.5:9000"}).URL("a.png"); u != "http://10.0.0.5:9000/a.png" {
		t.Errorf("http base: %q", u)
	}
}

func ptr[T any](v T) *T { return &v }

func TestItem(t *testing.T) {
	b := NewBuilder(Config{PathIP: "https://cdn.example.com"})
	it, ok := b.Item(Source{
		ID: 1, StorageKey: "static/images/a.png", Mime: "image/png", Width: ptr(3456), Height: ptr(2048),
		FocalX: ptr(0.25), FocalY: ptr(0.75), Alt: ptr("alt"), Blurhash: ptr("LEHV6n"), DominantColor: ptr("#112233"),
		Variants: json.RawMessage(`[{"variant":"w640","format":"webp","width":640,"height":379,"storage_key":"v/a-640.webp"},
		                           {"variant":"bad","format":"webp","width":1,"height":1,"storage_key":"/abs"}]`),
	})
	if !ok || it.URL != "/images/a.png" || *it.Mime != "image/png" || it.Focal == nil || it.Focal.X != 0.25 {
		t.Fatalf("%+v", it)
	}
	if !reflect.DeepEqual(it.Variants, []Variant{{Variant: "w640", Format: "webp", Width: 640, Height: 379, URL: "https://cdn.example.com/v/a-640.webp"}}) {
		t.Errorf("variants %+v", it.Variants)
	}
	j, _ := json.Marshal(it)
	for _, k := range []string{`"url"`, `"width"`, `"height"`, `"mime"`, `"alt"`, `"caption"`, `"credit"`, `"blurhash"`, `"dominant_color"`, `"focal"`, `"variants"`} {
		if !strings.Contains(string(j), k) {
			t.Errorf("json lacks %s: %s", k, j)
		}
	}

	// minimal: null fields stay null, variants is [] not null, bad variants JSON is ignored
	it, ok = b.Item(Source{ID: 2, StorageKey: "k.png", Variants: json.RawMessage(`{oops`)})
	if !ok || it.Mime != nil || it.Focal != nil || it.Variants == nil || len(it.Variants) != 0 {
		t.Errorf("%+v", it)
	}
	j, _ = json.Marshal(it)
	if !strings.Contains(string(j), `"variants":[]`) || !strings.Contains(string(j), `"width":null`) {
		t.Errorf("%s", j)
	}
	// a focal point needs both coordinates
	if it, _ = b.Item(Source{StorageKey: "k.png", FocalX: ptr(0.1)}); it.Focal != nil {
		t.Error("half a focal point")
	}
	// no URL -> no item
	if _, ok = NewBuilder(Config{}).Item(Source{StorageKey: "k.png"}); ok {
		t.Error("file-service key without PathIP must be left out")
	}
}

type fakeLookup struct {
	rows []Source
	err  error
	got  []int64
	loc  string
}

func (f *fakeLookup) AssetsByIDs(_ context.Context, locale string, ids []int64) ([]Source, error) {
	f.got, f.loc = ids, locale
	return f.rows, f.err
}

func TestResolve(t *testing.T) {
	b := NewBuilder(Config{})
	l := &fakeLookup{rows: []Source{{ID: 3, StorageKey: "static/x.png"}, {ID: 4, StorageKey: "svc/y.png"}}}
	m, err := b.Resolve(context.Background(), l, "en", []int64{3, 0, -1, 3, 4, 9})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(l.got, []int64{3, 4, 9}) || l.loc != "en" {
		t.Errorf("lookup args %v %q", l.got, l.loc)
	}
	if len(m) != 1 || m["3"].URL != "/x.png" {
		t.Errorf("only the asset with a URL is in the map: %+v", m)
	}
	// nothing to look up: no call, empty (not nil) map
	l2 := &fakeLookup{err: errors.New("must not be called")}
	m, err = b.Resolve(context.Background(), l2, "en", []int64{0})
	if err != nil || m == nil || len(m) != 0 {
		t.Errorf("%v %v", m, err)
	}
	if _, err = b.Resolve(context.Background(), &fakeLookup{err: errors.New("db")}, "en", []int64{1}); err == nil {
		t.Error("lookup error must pass")
	}
	many := make([]int64, maxIDs+1)
	for i := range many {
		many[i] = int64(i + 1)
	}
	if _, err = b.Resolve(context.Background(), l, "en", many); err == nil {
		t.Error("too many ids")
	}
}

func TestCollectIDs(t *testing.T) {
	v, _ := decodeAny(`{"background":{"media_id":5,"alt":""},"items":[{"image":{"media_id":2}},{"image":{"media_id":5}}],
	  "seo":{"og_image":{"media_id":9}},"body":{"type":"doc","content":[{"type":"image","attrs":{"media_id":7}}]},
	  "x":{"media_id":"8"},"y":{"media_id":0},"z":{"media_id":1.5},"cover_media_id":11}`)
	if got := CollectIDs(v); !reflect.DeepEqual(got, []int64{2, 5, 7, 9}) {
		t.Errorf("got %v", got)
	}
	// float64 from a plain json.Unmarshal works too
	var f any
	_ = json.Unmarshal([]byte(`{"a":{"media_id":3}}`), &f)
	if got := CollectIDs(f); !reflect.DeepEqual(got, []int64{3}) {
		t.Errorf("float64: %v", got)
	}
	if CollectIDs(nil) != nil {
		t.Error("nil")
	}
	if got := CollectIDs(map[string]any{"media_id": int64(4), "n": []any{map[string]any{"media_id": 6}}}); !reflect.DeepEqual(got, []int64{4, 6}) {
		t.Errorf("int types: %v", got)
	}
}

func decodeAny(s string) (any, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	err := dec.Decode(&v)
	return v, err
}

func TestItemCarriesKindDurationAndBytes(t *testing.T) {
	b := NewBuilder(Config{PathIP: "https://files.example.com"})
	d, size := 12500, int64(4200000)
	v, ok := b.Item(Source{ID: 7, StorageKey: "uploads/loop.mp4", Kind: "video", Mime: "video/mp4", DurationMS: &d, Bytes: &size})
	if !ok || v.Kind != "video" || v.DurationMS == nil || *v.DurationMS != 12500 || v.Bytes == nil || *v.Bytes != 4200000 {
		t.Fatalf("video item %+v", v)
	}
	raw, _ := json.Marshal(v)
	if !strings.Contains(string(raw), `"kind":"video"`) || !strings.Contains(string(raw), `"duration_ms":12500`) || !strings.Contains(string(raw), `"bytes":4200000`) {
		t.Errorf("json %s", raw)
	}
	zero := int64(0)
	img, _ := b.Item(Source{ID: 8, StorageKey: "uploads/a.webp", Kind: "image", Mime: "image/webp", Bytes: &zero})
	raw, _ = json.Marshal(img)
	if strings.Contains(string(raw), "duration_ms") || strings.Contains(string(raw), `"bytes"`) || !strings.Contains(string(raw), `"kind":"image"`) {
		t.Errorf("an image omits duration_ms and an empty size: %s", raw)
	}
}
