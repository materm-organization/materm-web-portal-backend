package assemble

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/entities"
	"github.com/materm-organization/materm-web-portal-backend/pkg/richtext"
)

// FormDefinition is the public definition of a form (doc/08 section 2.3.4): what `resolved.form` of a
// contact_section carries and the base of what P-14 answers (modules/forms adds min_fill_seconds). No
// recipients, no retention, no rate limits; "x-pii" and "x-error" are removed from the field schemas. This is
// the ONE builder of the shape (modules/forms and modules/pages both call BuildForm), so the two cannot drift.
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
	Key         string         `json:"key"`
	Required    bool           `json:"required"`
	Schema      map[string]any `json:"schema"`
	Label       string         `json:"label"`
	Placeholder *string        `json:"placeholder"`
	Help        *string        `json:"help"`
	Options     []FormOption   `json:"options,omitempty"`
}

// FormOption is one value of an enum field with its translated label.
type FormOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// internalSchemaKeys are removed from the field schemas that leave the server: x-pii marks personal fields for
// the retention job, x-error holds the error-code annotations of the validator.
var internalSchemaKeys = []string{"x-pii", "x-error"}

type schemaDoc struct {
	Required   []string                  `json:"required"`
	Properties map[string]map[string]any `json:"properties"`
}

// BuildForm turns the rows of v_published_forms and v_published_form_fields into the public definition.
// Fields follow field_order; properties that field_order forgets follow alphabetically; a field without a
// translation is labelled with its key (the form still renders). A json_schema that is not an object is an
// error: the caller decides (P-14 answers 500, a contact_section drops the form).
func BuildForm(f entities.PublishedForm, fields []entities.PublishedFormField) (*FormDefinition, error) {
	var sd schemaDoc
	if err := json.Unmarshal(f.JSONSchema.Bytes(), &sd); err != nil {
		return nil, fmt.Errorf("form %s: json_schema is not an object", f.Slug)
	}
	required := make(map[string]bool, len(sd.Required))
	for _, k := range sd.Required {
		required[k] = true
	}
	tr := make(map[string]entities.PublishedFormField, len(fields))
	for _, t := range fields {
		tr[t.FieldKey] = t
	}

	order := make([]string, 0, len(sd.Properties))
	seen := map[string]bool{}
	for _, k := range f.FieldOrder {
		if _, ok := sd.Properties[k]; ok && !seen[k] {
			order = append(order, k)
			seen[k] = true
		}
	}
	var rest []string
	for k := range sd.Properties {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	order = append(order, rest...)

	def := &FormDefinition{
		Slug: f.Slug, ResolvedLocale: f.ResolvedLocale, Title: f.Title, SubmitLabel: f.SubmitLabel,
		Description: strOr(f.Description), SendingLabel: strOr(f.SendingLabel), HoneypotField: strOr(f.HoneypotField),
		Success:  FormSuccess{Title: strOr(f.SuccessTitle), Description: strOr(f.SuccessDescription), AgainLabel: strOr(f.SuccessAgainLabel)},
		Messages: stringMap(f.Messages.Bytes()), SuccessRedirectURL: f.SuccessRedirectURL,
		Fields: make([]FormField, 0, len(order)),
	}
	for _, k := range order {
		schema := cloneWithout(sd.Properties[k], internalSchemaKeys)
		t, translated := tr[k]
		ff := FormField{Key: k, Required: required[k], Schema: schema, Label: k}
		if translated {
			if t.Label != "" {
				ff.Label = t.Label
			}
			ff.Placeholder = t.Placeholder
			ff.Help = t.Help
		}
		if enum, ok := schema["enum"].([]any); ok {
			labels := stringMap(t.OptionLabels.Bytes())
			for _, e := range enum {
				v, isString := e.(string)
				if !isString {
					continue
				}
				label := v
				if l := labels[v]; l != "" {
					label = l
				}
				ff.Options = append(ff.Options, FormOption{Value: v, Label: label})
			}
		}
		def.Fields = append(def.Fields, ff)
	}
	return def, nil
}

func strOr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// stringMap reads a JSON object and keeps its string values (anything else is dropped); never nil.
func stringMap(raw []byte) map[string]string {
	out := map[string]string{}
	if len(raw) == 0 {
		return out
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return out
	}
	for k, v := range m {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

func cloneWithout(m map[string]any, drop []string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	for _, k := range drop {
		delete(out, k)
	}
	return out
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
