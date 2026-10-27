// Package models holds the DTOs of the forms module (doc/08 section 2.3.4 and 6; the shapes are those of
// the frontend loader, src/lib/cms/types.ts: FormDefinition).
package models

import "github.com/materm-organization/materm-web-portal-backend/pkg/assemble"

// FormDefinition is the data of P-14: what the frontend needs to render a form. The shape is assemble.FormDefinition
// (the one builder, shared with `resolved.form` of a contact_section) plus MinFillSeconds, which belongs to P-14
// only (the minimum time before a submission is accepted as human; the server action may use it to explain a
// refusal). It never carries recipients, retention settings, rate-limit settings or payload limits, and
// `x-pii` and `x-error` are removed from the field schemas.
type FormDefinition struct {
	assemble.FormDefinition
	MinFillSeconds int `json:"min_fill_seconds"`
}

// FormSuccess, FormField and FormOption are the shared shapes.
type (
	FormSuccess = assemble.FormSuccess
	FormField   = assemble.FormField
	FormOption  = assemble.FormOption
)

// SubmissionRequest is the body of P-15 (doc/08 section 6.2). `rendered_at` is additive (the instant the
// server action rendered the form, RFC 3339): it lets the server apply forms.maxClockSkew; it may be absent.
type SubmissionRequest struct {
	Locale     string         `json:"locale"`
	SourcePath string         `json:"source_path"`
	Data       map[string]any `json:"data"`
	Honeypot   string         `json:"honeypot"`
	ElapsedMS  *int64         `json:"elapsed_ms"`
	RenderedAt string         `json:"rendered_at"`
	Consent    *Consent       `json:"consent"`
}

// Consent is the privacy-notice consent of the visitor ({version, at}); P1 sends null.
type Consent struct {
	Version string `json:"version"`
	At      string `json:"at"`
}

// Received is the data of the 200 answer of P-15, for a normal submission and for a spam one alike.
type Received struct {
	Status string `json:"status"`
}

// StatusReceived is the only status.
const StatusReceived = "received"
