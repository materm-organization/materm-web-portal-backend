package assemble

import (
	"errors"
	"strings"
	"testing"
)

func TestParsePageSnapshot(t *testing.T) {
	r, err := ParsePageSnapshot([]byte(`{"page":{"path":"/a","key":"a","unknown":1},"sections":[{"section_type":"faq"}],"extra":true}`))
	if err != nil || r.Page.Path != "/a" || len(r.Sections) != 1 || r.Sections[0].IsVisible != nil {
		t.Fatalf("%+v %v", r, err)
	}
	for name, in := range map[string]string{
		"not json":      `nope`,
		"not an object": `[]`,
		"wrong type":    `{"page":{"path":5}}`,
		"no path":       `{"page":{"key":"k"}}`,
		"blank path":    `{"page":{"path":"  "}}`,
	} {
		if _, perr := ParsePageSnapshot([]byte(in)); !errors.Is(perr, ErrBadSnapshot) {
			t.Errorf("%s: %v, want ErrBadSnapshot", name, perr)
		}
	}
	// the error never quotes the snapshot
	_, err = ParsePageSnapshot([]byte(`{"page":{"path":"TOPSECRET"},"sections":"TOPSECRET"}`))
	if err == nil || contains(err.Error(), "TOPSECRET") {
		t.Errorf("error %v must not carry content", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestPickTranslationFollowsTheChain(t *testing.T) {
	off, on := false, true
	ts := []RawPageTranslation{
		{LocaleCode: "en", Title: "en off", IsEnabled: &off},
		{LocaleCode: "la", Title: "la"},
		{LocaleCode: "fr", Title: "fr", IsEnabled: &on},
	}
	if got := pickTranslation(ts, []string{"en", "la"}); got == nil || got.Title != "la" {
		t.Errorf("a disabled translation must be skipped: %+v", got)
	}
	if got := pickTranslation(ts, []string{"fr", "la"}); got == nil || got.Title != "fr" {
		t.Errorf("chain order wins: %+v", got)
	}
	if pickTranslation(ts, []string{"de"}) != nil || pickTranslation(nil, []string{"la"}) != nil {
		t.Error("no match must be nil")
	}
}

func TestRemapSectionPath(t *testing.T) {
	orig := []int{4, 0, 2}
	for in, want := range map[string]string{
		"/sections/0":               "/sections/4",
		"/sections/2/props/items/1": "/sections/2/props/items/1",
		"/sections/1/props":         "/sections/0/props",
		"/sections/9":               "/sections/9",
		"/other":                    "/other",
		"/sections/x":               "/sections/x",
	} {
		if got := remapSectionPath(in, orig); got != want {
			t.Errorf("%s -> %s, want %s", in, got, want)
		}
	}
}

func TestParsePostSnapshot(t *testing.T) {
	good := `{"post":{"category_id":1,"published_at":"2026-09-05T00:00:00Z"},"translations":[` +
		`{"locale_code":"la","slug":"a-la","title":"A"},{"locale_code":"en","slug":"a-en","title":"A","is_enabled":false}],` +
		`"tag_ids":[3],"media":[{"id":9,"media_id":2,"sort_order":2},{"id":8,"media_id":1,"sort_order":1}]}`
	r, err := ParsePostSnapshot([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if r.Post.CategoryID == nil || *r.Post.CategoryID != 1 || r.Post.PublishedAt == nil || len(r.TagIDs) != 1 {
		t.Errorf("decoded: %+v", r)
	}
	if r.Media[0].ID != 8 {
		t.Errorf("gallery must be ordered by sort_order: %+v", r.Media)
	}
	if !r.HasSlug("a-en") || !r.HasSlug("a-la") || r.HasSlug("") || r.HasSlug("a") {
		t.Error("HasSlug")
	}
	// the first ENABLED translation in chain order; a disabled one is skipped
	if i, tr := r.PickTranslation([]string{"en", "la"}); tr == nil || i != 0 || tr.LocaleCode != "la" {
		t.Errorf("pick: %d %+v", i, tr)
	}
	if _, tr := r.PickTranslation([]string{"en"}); tr != nil {
		t.Error("a disabled translation must not be picked")
	}

	for name, bad := range map[string]string{
		"not json": `SECRET{`, "no slug": `{"translations":[{"locale_code":"en","slug":" ","title":"SECRET"}]}`,
		"wrong type": `{"post":{"category_id":"SECRET"}}`, "empty": `{}`,
	} {
		_, err := ParsePostSnapshot([]byte(bad))
		if !errors.Is(err, ErrBadSnapshot) {
			t.Errorf("%s: %v", name, err)
		} else if strings.Contains(err.Error(), "SECRET") {
			t.Errorf("%s: the error quotes the snapshot: %v", name, err)
		}
	}
}
