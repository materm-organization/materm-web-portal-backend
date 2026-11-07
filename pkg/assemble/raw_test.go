package assemble

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/entities"
	"github.com/materm-organization/materm-web-portal-backend/modules/content/repositories"
	"github.com/materm-organization/materm-web-portal-backend/pkg/media"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/seedfake"
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

// ---- shared sections in a preview snapshot (doc/05 section 18.12.3) -----------------------------------------------

type countingShared struct {
	rows  []entities.PublishedPageSection
	calls int
}

func (c *countingShared) SharedSections(_ context.Context, _ []int64) ([]entities.PublishedPageSection, error) {
	c.calls++
	return c.rows, nil
}

func previewShared(t *testing.T, src SharedSource, snapshot string) *PageResult {
	t.Helper()
	db := seedfake.Load(t)
	set := db.Set()
	a := New(Deps{
		Locales: set.Locales, Pages: set.Pages, Posts: set.Posts, Taxonomy: set.Taxonomy, Albums: set.Albums, FAQ: set.FAQ,
		Forms: set.Forms, Settings: set.Settings, Media: media.NewBuilder(media.Config{}), MediaLookup: repositories.NewMediaLookup(set.Assets),
		Shared: src,
	})
	raw, err := ParsePageSnapshot([]byte(snapshot))
	if err != nil {
		t.Fatal(err)
	}
	res, err := a.PreviewPage(context.Background(), raw, PreviewOptions{EntityID: 5, Locale: "la", Chain: db.Chain("la")})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func warnCodes(res *PageResult) []string {
	var out []string
	for _, w := range res.Warnings {
		out = append(out, w.Code+"@"+w.Path)
	}
	return out
}

const sharedSnapHead = `{"page":{"path":"/x"},"sections":[{"id":1,"section_type":"intro_heading","section_version":1,"sort_order":1,"shared_section_id":7,"props":{}}]`

func TestParseSharedSectionsMemberIsAPointer(t *testing.T) {
	for in, wantNil := range map[string]bool{
		`{"page":{"path":"/x"}}`:                        true,
		`{"page":{"path":"/x"},"shared_sections":null}`: true,
		`{"page":{"path":"/x"},"shared_sections":[]}`:   false,
		`{"page":{"path":"/x"},"shared_sections":[{"id":7,"section_type":"faq","section_version":1,"variant":"v","props":{},"state":"draft","name":"n","draft_revision_id":3}]}`: false,
	} {
		r, err := ParsePageSnapshot([]byte(in))
		if err != nil || (r.SharedSections == nil) != wantNil {
			t.Errorf("%s: nil=%v err=%v, want nil=%v", in, r != nil && r.SharedSections == nil, err, wantNil)
		}
	}
}

func TestPreviewSharedMemberReplacesTheViewRead(t *testing.T) {
	id := int64(7)
	src := &countingShared{rows: []entities.PublishedPageSection{{SectionKey: "intro_heading", SectionVersion: 1, SharedSectionID: &id, Props: entities.JSONB(`{"title":{"la":"PUBLISHED"}}`)}}}

	// with the member: the frozen props, never-published => not-live warning, no read of the view
	res := previewShared(t, src, sharedSnapHead+`,"shared_sections":[{"id":7,"section_type":"intro_heading","section_version":1,"variant":"wide","props":{"title":{"la":"FROZEN"}},"state":"draft"}]}`)
	if src.calls != 0 || len(res.Data.Sections) != 1 || !strings.Contains(string(res.Data.Sections[0].Props), "FROZEN") {
		t.Fatalf("calls=%d sections=%+v", src.calls, res.Data.Sections)
	}
	if res.Data.Sections[0].Variant == nil || *res.Data.Sections[0].Variant != "wide" {
		t.Errorf("variant: %v", res.Data.Sections[0].Variant)
	}
	if got := warnCodes(res); len(got) != 1 || got[0] != "shared_section_not_live@/sections/0/shared_section_id" {
		t.Errorf("warnings: %v", got)
	}

	// published entry: no warning; a missing section_version in the section counts as 1
	res = previewShared(t, src, strings.Replace(sharedSnapHead, `"section_version":1,`, ``, 1)+`,"shared_sections":[{"id":7,"section_type":"intro_heading","section_version":1,"props":{"title":{"la":"F"}},"state":"published"}]}`)
	if len(res.Data.Sections) != 1 || len(res.Warnings) != 0 {
		t.Errorf("published entry: %+v %v", res.Data.Sections, warnCodes(res))
	}

	// empty member, other id, wrong type, wrong version: left out with shared_section_unavailable
	for name, member := range map[string]string{
		"empty":      `[]`,
		"other id":   `[{"id":8,"section_type":"intro_heading","section_version":1,"props":{},"state":"published"}]`,
		"wrong type": `[{"id":7,"section_type":"intro_text","section_version":1,"props":{},"state":"published"}]`,
		"version":    `[{"id":7,"section_type":"intro_heading","section_version":2,"props":{},"state":"published"}]`,
	} {
		res = previewShared(t, src, sharedSnapHead+`,"shared_sections":`+member+`}`)
		if got := warnCodes(res); len(res.Data.Sections) != 0 || len(got) != 1 || got[0] != "shared_section_unavailable@/sections/0" {
			t.Errorf("%s: %d sections, warnings %v", name, len(res.Data.Sections), got)
		}
	}
	if src.calls != 0 {
		t.Errorf("SharedSource called %d times with the member present", src.calls)
	}

	// hidden: left out silently
	res = previewShared(t, src, strings.Replace(sharedSnapHead, `"props":{}`, `"is_visible":false,"props":{}`, 1)+`,"shared_sections":[]}`)
	if len(res.Data.Sections) != 0 || len(res.Warnings) != 0 {
		t.Errorf("hidden: %v", warnCodes(res))
	}

	// without the member: the old read of the PUBLISHED content
	res = previewShared(t, src, sharedSnapHead+`}`)
	if src.calls != 1 || len(res.Data.Sections) != 1 || !strings.Contains(string(res.Data.Sections[0].Props), "PUBLISHED") || len(res.Warnings) != 0 {
		t.Errorf("legacy path: calls=%d %+v %v", src.calls, res.Data.Sections, warnCodes(res))
	}
}
