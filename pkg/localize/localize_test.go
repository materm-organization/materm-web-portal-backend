package localize

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/materm-organization/materm-web-portal-backend/pkg/apperror"
)

func testChain(t *testing.T) *Chain {
	t.Helper()
	c, err := NewChain([]Locale{{Code: "la", IsDefault: true}, {Code: "en", FallbackCode: "la"}, {Code: "th", FallbackCode: "en"}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNewChainNeedsOneDefault(t *testing.T) {
	if _, err := NewChain(nil); err == nil {
		t.Error("no locales must fail")
	}
	if _, err := NewChain([]Locale{{Code: "la"}}); err == nil {
		t.Error("no default must fail")
	}
	if _, err := NewChain([]Locale{{Code: "la", IsDefault: true}, {Code: "en", IsDefault: true}}); err == nil {
		t.Error("two defaults must fail")
	}
}

func TestChainFor(t *testing.T) {
	c := testChain(t)
	cases := map[string][]string{
		"la": {"la"}, "en": {"en", "la"}, "th": {"th", "en", "la"}, "xx": {"la"},
	}
	for in, want := range cases {
		if got := c.For(in); !reflect.DeepEqual(got, want) {
			t.Errorf("For(%q) = %v, want %v", in, got, want)
		}
	}
	// a fallback that is not active is skipped
	c2, _ := NewChain([]Locale{{Code: "la", IsDefault: true}, {Code: "en", FallbackCode: "fr"}})
	if got := c2.For("en"); !reflect.DeepEqual(got, []string{"en", "la"}) {
		t.Errorf("inactive fallback: %v", got)
	}
	if c.Default() != "la" || !c.Has("en") || c.Has("fr") {
		t.Error("Default/Has")
	}
	if !reflect.DeepEqual(c.Codes(), []string{"en", "la", "th"}) {
		t.Errorf("Codes %v", c.Codes())
	}
}

func TestNormalizeAndContentLanguage(t *testing.T) {
	for in, want := range map[string]string{"lo": "la", "LO": "la", " lo-LA ": "la-la", "en-US": "en-us", "la": "la"} {
		if got := NormalizeCode(in); got != want {
			t.Errorf("NormalizeCode(%q) = %q, want %q", in, got, want)
		}
	}
	if ContentLanguage("la") != "lo" || ContentLanguage("en") != "en" {
		t.Error("ContentLanguage")
	}
}

func TestNegotiate(t *testing.T) {
	c := testChain(t)
	type want struct {
		code   string
		src    Source
		vary   bool
		cl     string
		failed bool
	}
	cases := []struct {
		name, query, accept string
		want                want
	}{
		{"query en", "en", "", want{code: "en", src: SourceQuery, cl: "en"}},
		{"query lo is la", "lo", "", want{code: "la", src: SourceQuery, cl: "lo"}},
		{"query beats header", "en", "th", want{code: "en", src: SourceQuery, cl: "en"}},
		{"query region falls to base", "en-US", "", want{code: "en", src: SourceQuery, cl: "en"}},
		{"query unknown -> default", "fr", "", want{code: "la", src: SourceDefault, cl: "lo"}},
		{"query bad pattern", "EN", "", want{failed: true}},
		{"query garbage", "en;drop", "", want{failed: true}},
		{"header q order", "", "fr;q=0.9, en;q=0.8, th;q=0.7", want{code: "en", src: SourceHeader, vary: true, cl: "en"}},
		{"header lo", "", "lo-LA,lo;q=0.9,en;q=0.5", want{code: "la", src: SourceHeader, vary: true, cl: "lo"}},
		{"header region", "", "en-GB", want{code: "en", src: SourceHeader, vary: true, cl: "en"}},
		{"header none matches", "", "fr, de;q=0.5", want{code: "la", src: SourceDefault, vary: true, cl: "lo"}},
		{"header q=0 ignored", "", "en;q=0, th", want{code: "th", src: SourceHeader, vary: true, cl: "th"}},
		{"header wildcard", "", "*", want{code: "la", src: SourceDefault, vary: true, cl: "lo"}},
		{"no input", "", "", want{code: "la", src: SourceDefault, vary: true, cl: "lo"}},
		{"bad q value", "", "en;q=abc, th", want{code: "th", src: SourceHeader, vary: true, cl: "th"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n, aerr := c.Negotiate(tc.query, tc.accept)
			if tc.want.failed {
				if aerr == nil || aerr.Code != apperror.CodeValidationFailed || len(aerr.Errors) != 1 ||
					aerr.Errors[0].In != apperror.InQuery || aerr.Errors[0].Path != "/locale" {
					t.Fatalf("want 422 on /locale, got %+v", aerr)
				}
				return
			}
			if aerr != nil {
				t.Fatal(aerr)
			}
			if n.Code != tc.want.code || n.Source != tc.want.src || n.Vary != tc.want.vary || n.ContentLanguage != tc.want.cl {
				t.Errorf("got %+v, want %+v", n, tc.want)
			}
			if !reflect.DeepEqual(n.Chain, c.For(n.Code)) {
				t.Errorf("chain %v", n.Chain)
			}
		})
	}
}

func TestNegotiateBoundsHostileHeader(t *testing.T) {
	c := testChain(t)
	long := ""
	for i := 0; i < 5000; i++ {
		long += "xx,"
	}
	if n, _ := c.Negotiate("", long); n.Code != "la" {
		t.Error("oversized header must fall to default")
	}
	many := ""
	for i := 0; i < 40; i++ {
		many += "zz,"
	}
	many += "en"
	if n, _ := c.Negotiate("", many); n.Code != "la" {
		t.Errorf("entries after the cap are ignored, got %s", n.Code)
	}
}

func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	m, err := DecodeObject([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func toJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestHintsErrors(t *testing.T) {
	for _, bad := range []string{"", "a..b", "[]", "a.[]"} {
		if _, err := NewHints(bad); err == nil {
			t.Errorf("NewHints(%q) must fail", bad)
		}
	}
	if (*Hints)(nil).Len() != 0 {
		t.Error("nil Len")
	}
	defer func() {
		if recover() == nil {
			t.Error("MustHints must panic")
		}
	}()
	MustHints("")
}

func TestCollapse(t *testing.T) {
	props := decode(t, `{
	  "title": {"la": "ສະບາຍດີ", "en": "Hello"},
	  "eyebrow": {"la": "", "en": "Only EN"},
	  "empty": {"la": "", "en": ""},
	  "plain": "already a string",
	  "show": true,
	  "cta": {"label": {"la": "A", "en": "B"}, "link": {"type": "url", "url": "/x"}},
	  "items": [{"title": {"la": "one-la"}, "image": {"media_id": 7, "alt": {"la": "", "en": "alt-en"}}},
	            {"title": {"en": "two-en"}}],
	  "tags": [{"la": "t1"}, {"en": "t2"}],
	  "body": {"la": {"type": "doc", "content": []}, "en": {"type": "doc", "content": [{"type": "paragraph"}]}}
	}`)
	h := MustHints("title", "eyebrow", "empty", "plain", "missing", "cta.label", "items[].title", "items[].image.alt", "tags[]", "body")
	orig := toJSON(t, props)

	out, fb := Collapse(props, h, []string{"en", "la"})
	if !fb {
		t.Error("fallback flag expected (items[0].title falls to la)")
	}
	want := `{"body":{"content":[{"type":"paragraph"}],"type":"doc"},"cta":{"label":"B","link":{"type":"url","url":"/x"}},"empty":null,"eyebrow":"Only EN","items":[{"image":{"alt":"alt-en","media_id":7},"title":"one-la"},{"title":"two-en"}],"plain":"already a string","show":true,"tags":["t1","t2"],"title":"Hello"}`
	if got := toJSON(t, out); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if toJSON(t, props) != orig {
		t.Error("input must not be modified")
	}

	// requested = default: no fallback at all when everything is there
	only := decode(t, `{"title": {"la": "x", "en": "y"}}`)
	out, fb = Collapse(only, MustHints("title"), []string{"la"})
	if fb || out["title"] != "x" {
		t.Errorf("%v %v", out, fb)
	}
	// a leaf missing everywhere is null and is not a fallback
	out, fb = Collapse(decode(t, `{"title": {"fr": "x"}}`), MustHints("title"), []string{"en", "la"})
	if fb || out["title"] != nil {
		t.Errorf("%v %v", out, fb)
	}
	// numbers stay exact
	n := decode(t, `{"a": {"la": 9007199254740993}}`)
	out, _ = Collapse(n, MustHints("a"), []string{"la"})
	if toJSON(t, out) != `{"a":9007199254740993}` {
		t.Errorf("number lost precision: %s", toJSON(t, out))
	}
	// degenerate inputs
	if o, f := Collapse(nil, h, []string{"la"}); o != nil || f {
		t.Error("nil props")
	}
	if o, _ := Collapse(only, nil, []string{"la"}); toJSON(t, o) != toJSON(t, only) {
		t.Error("no hints returns props")
	}
	if o, _ := Collapse(only, MustHints("title"), nil); toJSON(t, o) != toJSON(t, only) {
		t.Error("empty chain returns props")
	}
	// iter on a non-array and path through a scalar are skipped
	odd := decode(t, `{"items": "nope", "cta": 5}`)
	o, _ := Collapse(odd, MustHints("items[].title", "cta.label"), []string{"la"})
	if toJSON(t, o) != toJSON(t, odd) {
		t.Errorf("odd shapes must pass through: %s", toJSON(t, o))
	}
}

func TestDecodeObjectRejectsNonObject(t *testing.T) {
	if _, err := DecodeObject([]byte(`[1]`)); err == nil {
		t.Error("array must fail")
	}
}

func TestProvider(t *testing.T) {
	calls := 0
	var fail error
	now := time.Unix(1000, 0)
	p := NewProvider(func(context.Context) ([]Locale, error) {
		calls++
		if fail != nil {
			return nil, fail
		}
		return []Locale{{Code: "la", IsDefault: true}, {Code: "en", FallbackCode: "la"}}, nil
	}, time.Minute)
	p.now = func() time.Time { return now }

	c1, err := p.Chain(context.Background())
	if err != nil || c1.Default() != "la" || calls != 1 {
		t.Fatalf("first: %v %d", err, calls)
	}
	if _, _ = p.Chain(context.Background()); calls != 1 {
		t.Error("must be served from the process copy")
	}
	now = now.Add(2 * time.Minute)
	fail = errors.New("db down")
	c2, err := p.Chain(context.Background())
	if err != nil || c2 != c1 || calls != 2 {
		t.Errorf("a failed refresh must serve the previous chain: %v %d", err, calls)
	}
	fail = nil
	p.Invalidate()
	if c3, _ := p.Chain(context.Background()); c3 == c1 || calls != 3 {
		t.Errorf("Invalidate must force a reload (%d)", calls)
	}
	// never loaded + failing = error; an invalid locale set (no default) is an error too
	bad := NewProvider(func(context.Context) ([]Locale, error) { return nil, errors.New("x") }, 0)
	if _, err = bad.Chain(context.Background()); err == nil {
		t.Error("error expected")
	}
	nodef := NewProvider(func(context.Context) ([]Locale, error) { return []Locale{{Code: "en"}}, nil }, 0)
	if _, err = nodef.Chain(context.Background()); !errors.Is(err, ErrNoDefault) {
		t.Errorf("%v", err)
	}
	if bad.ttl != DefaultProviderTTL {
		t.Error("default ttl")
	}
}
