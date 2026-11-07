package assemble

import "fmt"

// Codes of Warning.Code (the same vocabulary as the publish warnings of doc/05 section 12.7.2).
const (
	// WarnReferenceNotPublished: the album, FAQ group or form a section shows is not published, so the section
	// is kept with an empty `resolved` (the public page leaves the whole section out).
	WarnReferenceNotPublished = "reference_not_published"
	// WarnPostNotPublished: a post a section or a rich-text link refers to is not published.
	WarnPostNotPublished = "post_not_published"
	// WarnLinkPageNotPublished: a link refers to a page that is not published (its href is null).
	WarnLinkPageNotPublished = "link_page_not_published"
	// WarnSectionTypeUnknown: the section type@version is not in the registry (or its props are unusable): the
	// section is left out, as on the public page.
	WarnSectionTypeUnknown = "section_type_unknown"
	// WarnSharedSectionUnavailable: the section uses a shared section that is not published, so its content
	// cannot be shown: the section is left out.
	WarnSharedSectionUnavailable = "shared_section_unavailable"
	// WarnSharedSectionNotLive: PREVIEW only. The page snapshot froze a working copy of the shared section whose
	// state is not "published" (draft, in_review, scheduled, archived, expired): the section is shown, the public
	// page would not show it (doc/05 section 18.12).
	WarnSharedSectionNotLive = "shared_section_not_live"
)

// Warning is one entry of data.preview.warnings: something in the previewed content that the public page would
// hide or leave out. Path is a JSON Pointer into the SNAPSHOT (/sections/2/props/...), never a value.
type Warning struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

// Warnings collects the warnings of one assembly. A nil *Warnings is "public mode": PageInput.Warnings set means
// PREVIEW mode, in which an unpublished reference keeps the section (resolved = {}) instead of dropping it and
// every dropped or degraded thing is reported. The zero value is ready to use.
type Warnings struct{ list []Warning }

// add records a warning (no-op on nil).
func (w *Warnings) add(code, path, format string, args ...any) {
	if w == nil {
		return
	}
	w.list = append(w.list, Warning{Code: code, Path: path, Message: fmt.Sprintf(format, args...)})
}

// On reports whether warnings are being collected (preview mode).
func (w *Warnings) On() bool { return w != nil }

// List returns the warnings collected so far (never nil).
func (w *Warnings) List() []Warning {
	if w == nil {
		return []Warning{}
	}
	return append([]Warning{}, w.list...)
}

func sectionPath(idx int) string { return fmt.Sprintf("/sections/%d", idx) }
