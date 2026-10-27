// Package models holds the DTOs of the forms module (doc/08 section 2.3.4 and 6; the shapes are those of
// the frontend loader, src/lib/cms/types.ts: FormDefinition).
package models

// FormDefinition is the data of P-14: what the frontend needs to render a form. It never carries recipients,
// retention settings, rate-limit settings or payload limits (those stay on the server), and `x-pii` and
// `x-error` are removed from the field schemas. MinFillSeconds is additive to the frontend type (the minimum
// time before a submission is accepted as human; the server action may use it to explain a refusal).
type FormDefinition struct {
	Slug               string            `json:"slug"`
	ResolvedLocale     string            `json:"resolved_locale"`
	Title              string            `json:"title"`
	Description        string            `json:"description"`
	SubmitLabel        string            `json:"submit_label"`
	SendingLabel       string            `json:"sending_label"`
	Success            FormSuccess       `json:"success"`
	Messages           map[string]string `json:"messages"`
	HoneypotField      string            `json:"honeypot_field"`
	MinFillSeconds     int               `json:"min_fill_seconds"`
	Fields             []FormField       `json:"fields"`
	SuccessRedirectURL *string           `json:"success_redirect_url"`
}

// FormSuccess is the thank-you text.
type FormSuccess struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	AgainLabel  string `json:"again_label"`
}

// FormField is one field of the definition, in display order.
type FormField struct {
	Key         string         `json:"key"`
	Required    bool           `json:"required"`
	Schema      map[string]any `json:"schema"`
	Label       string         `json:"label"`
	Placeholder *string        `json:"placeholder"`
	Help        *string        `json:"help"`
	Options     []FormOption   `json:"options,omitempty"`
}

// FormOption is a choice of an enum field.
type FormOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

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
