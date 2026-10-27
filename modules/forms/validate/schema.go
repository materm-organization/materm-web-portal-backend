package validate

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"

	"github.com/materm-organization/materm-web-portal-backend/pkg/apperror"
)

// Error codes. The contact form uses the codes of the frontend (CodeRequired, CodeEmail, CodeMessageShort,
// CodePhone, see contact.go); any other form gets the generic ones below unless its schema names its own in
// an `x-error` annotation ({"minLength": "required"}, doc/08 proposal P08-6).
const (
	CodeRequired     = "required"
	CodeUnknownField = "unknown_field"
	CodeTooShort     = "too_short"
	CodeInvalid      = "invalid_format"
	CodeNotInEnum    = "not_in_enum"
	CodeOutOfRange   = "out_of_range"
	CodeInvalidType  = "invalid_type"
	CodeTooMany      = "too_many_errors"
)

// Keywords an `x-error` annotation and the built-in tables can name.
const (
	kwRequired  = "required"
	kwMinLength = "minLength"
	kwFormat    = "format"
	kwPattern   = "pattern"
	kwEnum      = "enum"
	kwRange     = "range"
	kwType      = "type"
)

var genericCodes = map[string]string{
	kwRequired: CodeRequired, kwMinLength: CodeTooShort, kwFormat: CodeInvalid, kwPattern: CodeInvalid,
	kwEnum: CodeNotInEnum, kwRange: CodeOutOfRange, kwType: CodeInvalidType,
}

const (
	// defaultMaxLength caps a string field whose schema names no maxLength.
	defaultMaxLength = 2000
	// multilineAbove: a string field longer than this is a text area (the same rule as the frontend widget
	// table, doc/08 section 4.1) and keeps its line breaks.
	multilineAbove = 300
	// maxErrors bounds the answer for a hostile payload.
	maxErrors = 20
	// maxKeyEcho bounds how much of a client-chosen field name is echoed in an error path.
	maxKeyEcho = 60
)

// Field is one compiled property of the schema.
type Field struct {
	Key       string
	Type      string // string | boolean | integer | number
	Required  bool
	MinLength *int
	MaxLength *int
	Pattern   *regexp.Regexp
	Format    string
	Enum      []string
	Multiline bool
	Min, Max  *float64
	codes     map[string]string
}

// Schema is a compiled json_schema of a form.
type Schema struct {
	Slug   string
	Fields []Field
	byKey  map[string]*Field
}

// ErrSchema marks a form definition that cannot be compiled (an operator mistake, not a client error).
var ErrSchema = errors.New("validate: unusable form schema")

type rawProp struct {
	Type      string         `json:"type"`
	MinLength *int           `json:"minLength"`
	MaxLength *int           `json:"maxLength"`
	Pattern   string         `json:"pattern"`
	Format    string         `json:"format"`
	Enum      []any          `json:"enum"`
	Minimum   *float64       `json:"minimum"`
	Maximum   *float64       `json:"maximum"`
	XMultiln  *bool          `json:"x-multiline"`
	XError    map[string]any `json:"x-error"`
}

type rawSchema struct {
	Type       string             `json:"type"`
	Required   []string           `json:"required"`
	Properties map[string]rawProp `json:"properties"`
}

// Compile builds the Schema of a form from its json_schema, the explicit field order and its slug. Fields are
// ordered by fieldOrder; properties missing from it follow in alphabetical order.
func Compile(jsonSchema []byte, fieldOrder []string, slug string) (*Schema, error) {
	var rs rawSchema
	if err := json.Unmarshal(jsonSchema, &rs); err != nil {
		return nil, fmt.Errorf("%w: not JSON", ErrSchema)
	}
	if rs.Type != "object" {
		return nil, fmt.Errorf("%w: type is not object", ErrSchema)
	}
	required := map[string]bool{}
	for _, k := range rs.Required {
		required[k] = true
	}
	order := make([]string, 0, len(rs.Properties))
	seen := map[string]bool{}
	for _, k := range fieldOrder {
		if _, ok := rs.Properties[k]; ok && !seen[k] {
			order = append(order, k)
			seen[k] = true
		}
	}
	var rest []string
	for k := range rs.Properties {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	order = append(order, rest...)

	s := &Schema{Slug: slug, byKey: map[string]*Field{}}
	for _, k := range order {
		p := rs.Properties[k]
		f := Field{Key: k, Type: p.Type, Required: required[k], MinLength: p.MinLength, MaxLength: p.MaxLength, Format: p.Format, Min: p.Minimum, Max: p.Maximum}
		if f.Type == "" {
			f.Type = "string"
		}
		switch f.Type {
		case "string", "boolean", "integer", "number":
		default:
			return nil, fmt.Errorf("%w: field type %q is not supported", ErrSchema, f.Type)
		}
		if p.Pattern != "" {
			re, err := regexp.Compile(p.Pattern)
			if err != nil {
				return nil, fmt.Errorf("%w: a pattern does not compile", ErrSchema)
			}
			f.Pattern = re
		}
		for _, e := range p.Enum {
			es, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("%w: only string enums are supported", ErrSchema)
			}
			f.Enum = append(f.Enum, es)
		}
		f.Multiline = f.Type == "string" && ((p.XMultiln != nil && *p.XMultiln) || (p.XMultiln == nil && f.MaxLength != nil && *f.MaxLength > multilineAbove))
		f.codes = mergeCodes(slug, k, p.XError)
		s.Fields = append(s.Fields, f)
	}
	for i := range s.Fields {
		s.byKey[s.Fields[i].Key] = &s.Fields[i]
	}
	return s, nil
}

// Field returns the compiled property named key.
func (s *Schema) Field(key string) (*Field, bool) { f, ok := s.byKey[key]; return f, ok }

// mergeCodes resolves keyword -> code: generic < built-in table of the form < `x-error` annotation.
func mergeCodes(slug, key string, annotation map[string]any) map[string]string {
	out := make(map[string]string, len(genericCodes))
	for k, v := range genericCodes {
		out[k] = v
	}
	for k, v := range builtinCodes[slug][key] {
		out[k] = v
	}
	for k, v := range annotation {
		if code, ok := v.(string); ok && code != "" && len(code) <= 40 {
			out[k] = code
		}
	}
	return out
}

// Result is a checked submission.
type Result struct {
	// Data holds the sanitised value of every schema field that the client sent (plus every required one that
	// was valid): strings, booleans and numbers. It is what goes to forms_submit_form.
	Data map[string]any
	// Errors are 422 field errors (in:"body", path:"/data/<key>"), in field order, unknown fields last.
	Errors []apperror.FieldError
}

// Check sanitises and validates data. It never reflects a submitted value in an error.
func (s *Schema) Check(data map[string]any) Result {
	res := Result{Data: map[string]any{}}
	add := func(key, code string) {
		if len(res.Errors) < maxErrors {
			res.Errors = append(res.Errors, apperror.Field("/data/"+pointer(key), code))
		}
	}
	for i := range s.Fields {
		f := &s.Fields[i]
		raw, present := data[f.Key]
		switch f.Type {
		case "string":
			s.checkString(f, raw, present, res.Data, add)
		case "boolean":
			b, ok := raw.(bool)
			switch {
			case ok:
				res.Data[f.Key] = b
			case f.Required:
				add(f.Key, f.codes[kwRequired])
			}
		default: // integer, number
			n, ok := toFloat(raw)
			switch {
			case !ok && f.Required:
				add(f.Key, f.codes[kwRequired])
			case !ok:
			case f.Type == "integer" && n != math.Trunc(n),
				f.Min != nil && n < *f.Min, f.Max != nil && n > *f.Max:
				add(f.Key, f.codes[kwRange])
			default:
				res.Data[f.Key] = raw
			}
		}
	}
	var unknown []string
	for k := range data {
		if _, ok := s.byKey[k]; !ok {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	for _, k := range unknown {
		add(k, CodeUnknownField)
	}
	if len(res.Errors) >= maxErrors {
		res.Errors = append(res.Errors[:maxErrors-1], apperror.Field("", CodeTooMany))
	}
	return res
}

func (s *Schema) checkString(f *Field, raw any, present bool, out map[string]any, add func(key, code string)) {
	max := defaultMaxLength
	switch {
	case f.MaxLength != nil:
		max = *f.MaxLength
	case len(f.Enum) > 0:
		max = math.MaxInt32 // a membership test needs no cut
	}
	v := SanitizeText(raw, max, f.Multiline)
	if v == "" {
		if f.Required {
			add(f.Key, f.codes[kwRequired])
			return
		}
		if present {
			out[f.Key] = ""
		}
		return
	}
	switch {
	case f.MinLength != nil && UTF16Len(v) < *f.MinLength:
		add(f.Key, f.codes[kwMinLength])
	case f.Format == "email" && !ValidEmail(v):
		add(f.Key, f.codes[kwFormat])
	case f.Pattern != nil && !f.Pattern.MatchString(v):
		add(f.Key, f.codes[kwPattern])
	case len(f.Enum) > 0 && !contains(f.Enum, v):
		add(f.Key, f.codes[kwEnum])
	default:
		out[f.Key] = v
	}
}

func contains(list []string, v string) bool {
	for _, e := range list {
		if e == v {
			return true
		}
	}
	return false
}

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case json.Number:
		f, err := strconv.ParseFloat(t.String(), 64)
		return f, err == nil
	}
	return 0, false
}

// pointer escapes a client-chosen key for use in a JSON Pointer (RFC 6901) and bounds its length.
func pointer(key string) string {
	if len(key) > maxKeyEcho {
		key = sliceUnits(key, maxKeyEcho)
	}
	var b bytes.Buffer
	for _, r := range key {
		switch {
		case r == '~':
			b.WriteString("~0")
		case r == '/':
			b.WriteString("~1")
		case r < 0x20 || r == 0x7f:
			b.WriteByte('?')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
