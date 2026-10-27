package services

import (
	"github.com/materm-organization/materm-web-portal-backend/modules/content/entities"
	"github.com/materm-organization/materm-web-portal-backend/modules/forms/models"
	"github.com/materm-organization/materm-web-portal-backend/pkg/assemble"
)

// BuildDefinition turns the rows of v_published_forms and v_published_form_fields into the P-14 data: the
// shared definition (assemble.BuildForm, the same builder as `resolved.form` of a contact_section) plus
// min_fill_seconds, which only P-14 carries.
func BuildDefinition(f entities.PublishedForm, fields []entities.PublishedFormField) (*models.FormDefinition, error) {
	base, err := assemble.BuildForm(f, fields)
	if err != nil {
		return nil, err
	}
	def := &models.FormDefinition{FormDefinition: *base}
	if f.MinFillSeconds != nil {
		def.MinFillSeconds = *f.MinFillSeconds
	}
	return def, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
