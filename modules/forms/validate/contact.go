package validate

// builtinCodes is the error-code table of the contact form (doc/08 section 6.2): the codes that
// validateContactField of the frontend returns, which a generic keyword -> code mapping could not reproduce
// (a name shorter than 2 characters is "required", not "too_short"). Keyed by form slug, then field, then
// keyword. A form's own `x-error` annotations override it; other forms use the generic codes.
var builtinCodes = map[string]map[string]map[string]string{
	"contact": {
		"name":    {kwMinLength: "required"},
		"email":   {kwFormat: "email"},
		"phone":   {kwPattern: "phone"},
		"topic":   {kwEnum: "required"},
		"message": {kwMinLength: "messageShort"},
	},
}

// Frontend codes of the contact form, for tests and callers that name them.
const (
	CodeEmail        = "email"
	CodePhone        = "phone"
	CodeMessageShort = "messageShort"
)
