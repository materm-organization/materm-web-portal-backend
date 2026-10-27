package services

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/entities"
	"github.com/materm-organization/materm-web-portal-backend/modules/forms/models"
)

// internalSchemaKeys are removed from the field schemas that leave the server (doc/08 section 2.3.4): x-pii
// marks personal fields for the retention job, x-error holds the error-code annotations of the validator.
var internalSchemaKeys = []string{"x-pii", "x-error"}

type schemaDoc struct {
	Required   []string                  `json:"required"`
	Properties map[string]map[string]any `json:"properties"`
}

// BuildDefinition turns the rows of v_published_forms and v_published_form_fields into the P-14 data. Fields
// follow field_order; properties that field_order forgets follow alphabetically. A field without a
// translation is labelled with its key (the form still renders). Nothing that belongs to the server leaves:
// no recipients or retention (the view has none), no rate limits, no payload limit, no x-pii.
func BuildDefinition(f entities.PublishedForm, fields []entities.PublishedFormField) (*models.FormDefinition, error) {
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

	def := &models.FormDefinition{
		Slug: f.Slug, ResolvedLocale: f.ResolvedLocale, Title: f.Title, SubmitLabel: f.SubmitLabel,
		Description: deref(f.Description), SendingLabel: deref(f.SendingLabel), HoneypotField: deref(f.HoneypotField),
		Success:  models.FormSuccess{Title: deref(f.SuccessTitle), Description: deref(f.SuccessDescription), AgainLabel: deref(f.SuccessAgainLabel)},
		Messages: stringMap(f.Messages.Bytes()), SuccessRedirectURL: f.SuccessRedirectURL,
		Fields: make([]models.FormField, 0, len(order)),
	}
	if f.MinFillSeconds != nil {
		def.MinFillSeconds = *f.MinFillSeconds
	}
	for _, k := range order {
		schema := cloneWithout(sd.Properties[k], internalSchemaKeys)
		t, translated := tr[k]
		ff := models.FormField{Key: k, Required: required[k], Schema: schema, Label: k}
		if translated {
			ff.Label = t.Label
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
				if l, has := labels[v]; has && l != "" {
					label = l
				}
				ff.Options = append(ff.Options, models.FormOption{Value: v, Label: label})
			}
		}
		def.Fields = append(def.Fields, ff)
	}
	return def, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// stringMap reads a JSON object and keeps its string values (anything else is dropped).
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
