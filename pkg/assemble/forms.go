package assemble

import (
	"encoding/json"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/entities"
	"github.com/materm-organization/materm-web-portal-backend/pkg/richtext"
)

// FormDefinition is the public definition of a form (doc/08 section 2.3.4): what P-14 answers and what
// `resolved.form` of a contact_section carries. No recipients, no retention, no rate limits; "x-pii" is
// removed from the field schemas. Shared by modules/pages and modules/forms so that the shape exists once.
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
	Fields             []FormField       `json:"fields"`
	SuccessRedirectURL *string           `json:"success_redirect_url"`
}

// FormSuccess is the thank-you panel.
type FormSuccess struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	AgainLabel  string `json:"again_label"`
}

// FormField is one field: its JSON Schema, translated label/placeholder/help, and the options of an enum.
type FormField struct {
	Key         string          `json:"key"`
	Required    bool            `json:"required"`
	Schema      json.RawMessage `json:"schema"`
	Label       string          `json:"label"`
	Placeholder *string         `json:"placeholder"`
	Help        *string         `json:"help"`
	Options     []FormOption    `json:"options,omitempty"`
}

// FormOption is one value of an enum field with its translated label.
type FormOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// BuildForm assembles the definition from v_published_forms and v_published_form_fields. A field of
// field_order without a translation row is still listed (its key is its label).
func BuildForm(f *entities.PublishedForm, fields []entities.PublishedFormField) *FormDefinition {
	var schema struct {
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	_ = json.Unmarshal(f.JSONSchema.Bytes(), &schema) // a malformed schema yields a form without fields
	required := make(map[string]bool, len(schema.Required))
	for _, k := range schema.Required {
		required[k] = true
	}
	byKey := make(map[string]entities.PublishedFormField, len(fields))
	for _, fl := range fields {
		byKey[fl.FieldKey] = fl
	}

	out := &FormDefinition{
		Slug: f.Slug, ResolvedLocale: f.ResolvedLocale, Title: f.Title, Description: strOr(f.Description),
		SubmitLabel: f.SubmitLabel, SendingLabel: strOr(f.SendingLabel),
		Success:            FormSuccess{Title: strOr(f.SuccessTitle), Description: strOr(f.SuccessDescription), AgainLabel: strOr(f.SuccessAgainLabel)},
		Messages:           map[string]string{},
		HoneypotField:      strOr(f.HoneypotField),
		Fields:             []FormField{},
		SuccessRedirectURL: f.SuccessRedirectURL,
	}
	if b := f.Messages.Bytes(); len(b) > 0 {
		_ = json.Unmarshal(b, &out.Messages)
		if out.Messages == nil {
			out.Messages = map[string]string{}
		}
	}
	for _, key := range f.FieldOrder {
		propRaw, ok := schema.Properties[key]
		if !ok {
			continue
		}
		var prop map[string]any
		if json.Unmarshal(propRaw, &prop) != nil {
			continue
		}
		delete(prop, "x-pii")
		sb, _ := json.Marshal(prop)
		tr := byKey[key]
		field := FormField{Key: key, Required: required[key], Schema: sb, Label: tr.Label, Placeholder: tr.Placeholder, Help: tr.Help}
		if field.Label == "" {
			field.Label = key
		}
		if enum, ok := prop["enum"].([]any); ok {
			var labels map[string]string
			if b := tr.OptionLabels.Bytes(); len(b) > 0 {
				_ = json.Unmarshal(b, &labels)
			}
			field.Options = make([]FormOption, 0, len(enum))
			for _, e := range enum {
				v, _ := e.(string)
				label := labels[v]
				if label == "" {
					label = v
				}
				field.Options = append(field.Options, FormOption{Value: v, Label: label})
			}
		}
		out.Fields = append(out.Fields, field)
	}
	return out
}

func strOr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// FAQItem is one question of a FAQ group; Answer is a sanitised rich text (lite) document.
type FAQItem struct {
	ID       int64          `json:"id"`
	Question string         `json:"question"`
	Answer   map[string]any `json:"answer"`
}

// BuildFAQItems sanitises the answers (links resolved through r, which may be nil) and keeps the order of rows.
func BuildFAQItems(rows []entities.PublishedFAQItem, r richtext.Resolver) []FAQItem {
	out := make([]FAQItem, 0, len(rows))
	for _, row := range rows {
		res := richtext.ResolveJSON(row.Answer.Bytes(), richtext.Lite, r)
		out = append(out, FAQItem{ID: row.FAQItemID, Question: row.Question, Answer: res.Doc})
	}
	return out
}

// FAQAnswerDocs decodes the answers of rows, to pass to NewLinkResolver.
func FAQAnswerDocs(rows []entities.PublishedFAQItem) []any {
	docs := make([]any, 0, len(rows))
	for _, row := range rows {
		var d any
		if b := row.Answer.Bytes(); len(b) > 0 && json.Unmarshal(b, &d) == nil {
			docs = append(docs, d)
		}
	}
	return docs
}
