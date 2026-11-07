package previewapi

import (
	"fmt"
	"strings"
	"testing"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/entities"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/apitest"
)

// Tests of doc/05 section 18.18 (portal-backend list, items 1-7; item 8 is in db_dbtest_test.go): the preview
// snapshot member `shared_sections` (the working copy of the shared sections, frozen by the admin) replaces the
// read of the published view.

func sharedEntry(id int, typ string, version int, variant any, state string, props map[string]any) map[string]any {
	return map[string]any{"id": id, "name": "n", "section_type": typ, "section_version": version, "variant": variant,
		"props": props, "state": state, "draft_revision_id": nil}
}

func sharedSnapshot(t *testing.T, shared any, sections ...any) []byte {
	t.Helper()
	m := map[string]any{"page": map[string]any{"path": "/x", "name": "X"}, "sections": sections}
	if shared != nil {
		m["shared_sections"] = shared
	}
	return snapshotJSON(t, m)
}

func refSection(sortOrder, sharedID int, typ string, extra map[string]any) map[string]any {
	s := map[string]any{"id": 100 + sortOrder, "section_type": typ, "section_version": 1, "sort_order": sortOrder,
		"shared_section_id": sharedID, "props": map[string]any{}}
	for k, v := range extra {
		s[k] = v
	}
	return s
}

func warningList(r *apitest.Response) []string {
	var out []string
	for _, w := range r.Data()["preview"].(map[string]any)["warnings"].([]any) {
		m := w.(map[string]any)
		out = append(out, fmt.Sprintf("%s@%s", m["code"], m["path"]))
	}
	return out
}

func sharedStack(t *testing.T) (*stack, *fakeShared) {
	t.Helper()
	// the published view knows id 7 with OTHER content: it must never be shown, and never even be asked for
	id := int64(7)
	fs := &fakeShared{rows: []entities.PublishedPageSection{
		{SectionKey: "intro_heading", SectionVersion: 1, Props: entities.JSONB(`{"title":{"la":"PUBLISHED VERSION"}}`), SharedSectionID: &id},
	}}
	return newStack(t, func(o *opts) { o.shared = fs }), fs
}

// (1) a shared section that was never published: shown with the frozen props/variant, warning shared_section_not_live,
// and SharedSource is not called at all.
func TestPreviewSharedFromTheSnapshotNeverPublished(t *testing.T) {
	st, fs := sharedStack(t)
	snap := sharedSnapshot(t, []any{sharedEntry(7, "intro_heading", 1, "wide", "draft", map[string]any{"title": map[string]any{"la": "Draft title"}})},
		refSection(1, 7, "intro_heading", nil))
	r := st.resolve(t, st.issue("ss1", 5, "", snap), "")
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	secs := r.Data()["page"].(map[string]any)["sections"].([]any)
	if len(secs) != 1 || secs[0].(map[string]any)["variant"] != "wide" || !strings.Contains(string(r.Body), "Draft title") || strings.Contains(string(r.Body), "PUBLISHED VERSION") {
		t.Errorf("sections: %v", secs)
	}
	if got := warningList(r); fmt.Sprint(got) != "[shared_section_not_live@/sections/0/shared_section_id]" {
		t.Errorf("warnings: %v", got)
	}
	if n := fs.calls.Load(); n != 0 {
		t.Errorf("SharedSource was called %d times, want 0", n)
	}
}

// (2) an entry that was live at issue time: no warning; the page's own variant wins over the entry's (SS-6).
func TestPreviewSharedLiveEntryHasNoWarning(t *testing.T) {
	st, fs := sharedStack(t)
	snap := sharedSnapshot(t, []any{sharedEntry(7, "intro_heading", 1, "wide", "published", map[string]any{"title": map[string]any{"la": "Live"}})},
		refSection(1, 7, "intro_heading", nil), refSection(2, 7, "intro_heading", map[string]any{"variant": "narrow"}))
	r := st.resolve(t, st.issue("ss2", 5, "", snap), "")
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	secs := r.Data()["page"].(map[string]any)["sections"].([]any)
	if len(secs) != 2 || secs[0].(map[string]any)["variant"] != "wide" || secs[1].(map[string]any)["variant"] != "narrow" {
		t.Errorf("variants: %v", secs)
	}
	if got := warningList(r); len(got) != 0 {
		t.Errorf("warnings: %v", got)
	}
	if fs.calls.Load() != 0 {
		t.Error("SharedSource must not be called")
	}
}

// (3) an empty member is authoritative: the section is left out even though the published view has the id.
func TestPreviewSharedEmptyMemberDropsTheSection(t *testing.T) {
	st, fs := sharedStack(t)
	snap := sharedSnapshot(t, []any{}, refSection(1, 7, "intro_heading", nil))
	r := st.resolve(t, st.issue("ss3", 5, "", snap), "")
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if secs := r.Data()["page"].(map[string]any)["sections"].([]any); len(secs) != 0 {
		t.Errorf("sections: %v", secs)
	}
	if got := warningList(r); fmt.Sprint(got) != "[shared_section_unavailable@/sections/0]" {
		t.Errorf("warnings: %v", got)
	}
	if fs.calls.Load() != 0 {
		t.Error("SharedSource must not be called")
	}
}

// (4) type or version mismatch between the section and the entry: left out, shared_section_unavailable.
func TestPreviewSharedTypeOrVersionMismatch(t *testing.T) {
	for name, entry := range map[string]map[string]any{
		"type":    sharedEntry(7, "intro_text", 1, nil, "published", map[string]any{}),
		"version": sharedEntry(7, "intro_heading", 2, nil, "published", map[string]any{"title": map[string]any{"la": "x"}}),
	} {
		t.Run(name, func(t *testing.T) {
			st, _ := sharedStack(t)
			snap := sharedSnapshot(t, []any{entry}, refSection(3, 7, "intro_heading", nil))
			r := st.resolve(t, st.issue("ss4"+name, 5, "", snap), "")
			if r.Status != 200 {
				t.Fatalf("%d %s", r.Status, r.Body)
			}
			if secs := r.Data()["page"].(map[string]any)["sections"].([]any); len(secs) != 0 {
				t.Errorf("sections: %v", secs)
			}
			if got := warningList(r); fmt.Sprint(got) != "[shared_section_unavailable@/sections/0]" {
				t.Errorf("warnings: %v", got)
			}
		})
	}
}

// (5) a snapshot without the key (a link issued before the admin change) takes the old path: the PUBLISHED
// content through SharedSource. Present-but-null counts as absent (json null leaves the pointer nil).
func TestPreviewSharedWithoutTheMemberReadsThePublishedView(t *testing.T) {
	st, fs := sharedStack(t)
	snap := sharedSnapshot(t, nil, refSection(1, 7, "intro_heading", nil))
	r := st.resolve(t, st.issue("ss5", 5, "", snap), "")
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if !strings.Contains(string(r.Body), "PUBLISHED VERSION") {
		t.Errorf("body: %s", r.Body)
	}
	if got := warningList(r); len(got) != 0 {
		t.Errorf("warnings: %v", got)
	}
	if fs.calls.Load() != 1 {
		t.Errorf("SharedSource calls = %d, want 1", fs.calls.Load())
	}
}

// (6) a hidden section that uses a shared section is not shown and gives no warning, member or not.
func TestPreviewSharedHiddenSectionIsSilent(t *testing.T) {
	st, fs := sharedStack(t)
	snap := sharedSnapshot(t, []any{sharedEntry(7, "intro_heading", 1, nil, "draft", map[string]any{"title": map[string]any{"la": "Hidden"}})},
		refSection(1, 7, "intro_heading", map[string]any{"is_visible": false}))
	r := st.resolve(t, st.issue("ss6", 5, "", snap), "")
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if secs := r.Data()["page"].(map[string]any)["sections"].([]any); len(secs) != 0 || strings.Contains(string(r.Body), "Hidden") {
		t.Errorf("sections: %v", secs)
	}
	if got := warningList(r); len(got) != 0 {
		t.Errorf("warnings: %v", got)
	}
	if fs.calls.Load() != 0 {
		t.Error("SharedSource must not be called")
	}
}

// (7) the props of an entry are collapsed with the locale chain of the link, and what they refer to is read
// from the published data: a published page gives a link, an unpublished one the usual warning (its path points
// into the snapshot's section, the shared props being rendered like the page's own).
func TestPreviewSharedPropsAreLocalisedAndReferencesResolved(t *testing.T) {
	st, _ := sharedStack(t)
	props := map[string]any{
		"title": map[string]any{"la": "Title la", "en": "Title en"},
		"cta":   map[string]any{"label": map[string]any{"la": "Go"}, "link": map[string]any{"type": "page", "page_id": 9999}},
	}
	snap := sharedSnapshot(t, []any{sharedEntry(7, "hero_brand", 1, nil, "draft", props)}, refSection(1, 7, "hero_brand", nil))
	tok := st.issue("ss7", 5, "", snap)

	en := st.resolve(t, tok, "?locale=en")
	if en.Status != 200 {
		t.Fatalf("%d %s", en.Status, en.Body)
	}
	if !strings.Contains(string(en.Body), "Title en") || strings.Contains(string(en.Body), "Title la") {
		t.Errorf("en must pick the en leaf: %s", en.Body)
	}
	la := st.resolve(t, tok, "?locale=la")
	if !strings.Contains(string(la.Body), "Title la") || strings.Contains(string(la.Body), "Title en") {
		t.Errorf("la must pick the la leaf: %s", la.Body)
	}
	got := warningList(en)
	if !contains(got, "shared_section_not_live@/sections/0/shared_section_id") || !contains(got, "link_page_not_published@/sections/0") {
		t.Errorf("warnings: %v", got)
	}
}
