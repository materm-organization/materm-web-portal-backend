package validator

import (
	"errors"
	"reflect"
	"strconv"
	"strings"

	"github.com/go-playground/validator/v10"
)

// Issue is one field problem derived from a go-playground validation error, in the vocabulary of the
// API's field errors (doc/05 section 1.5): a JSON Pointer path and a field code.
type Issue struct {
	Path   string
	Code   string
	Params map[string]any
}

// Struct validates v with the shared validator (json tag names, `validate` tags) and returns the issues,
// nil when v is valid. A non-validation failure (v is not a struct, ...) is returned as err.
func Struct(v any) (issues []Issue, err error) {
	e := validate.Struct(v)
	if e == nil {
		return nil, nil
	}
	issues, ok := Issues(e)
	if !ok {
		return nil, e
	}
	return issues, nil
}

// Issues converts an error returned by a validator into Issues. ok is false when err is not a
// validator.ValidationErrors.
func Issues(err error) (issues []Issue, ok bool) {
	var ves validator.ValidationErrors
	if !errors.As(err, &ves) {
		return nil, false
	}
	for _, fe := range ves {
		issues = append(issues, Issue{Path: pointer(fe.Namespace()), Code: code(fe), Params: params(fe)})
	}
	return issues, true
}

// pointer turns "CreateReq.grants[0].actions[1]" into "/grants/0/actions/1" (the root struct name is dropped).
func pointer(ns string) string {
	if i := strings.Index(ns, "."); i >= 0 {
		ns = ns[i+1:]
	} else {
		return ""
	}
	var b strings.Builder
	for _, seg := range strings.Split(ns, ".") {
		name := seg
		var idx []string
		for {
			open := strings.LastIndex(name, "[")
			if open < 0 || !strings.HasSuffix(name, "]") {
				break
			}
			idx = append([]string{name[open+1 : len(name)-1]}, idx...)
			name = name[:open]
		}
		b.WriteString("/" + escape(name))
		for _, n := range idx {
			b.WriteString("/" + escape(n))
		}
	}
	return b.String()
}

// escape applies RFC 6901 escaping to one reference token.
func escape(s string) string {
	return strings.NewReplacer("~", "~0", "/", "~1").Replace(s)
}

func code(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required", "required_if", "required_unless", "required_with", "required_without":
		return "required"
	case "min", "gte", "gt":
		if isCount(fe.Kind()) {
			return "too_short"
		}
		return "out_of_range"
	case "max", "lte", "lt":
		if isCount(fe.Kind()) {
			return "too_long"
		}
		return "out_of_range"
	case "oneof":
		return "not_in_enum"
	default:
		return "invalid_format"
	}
}

// isCount reports kinds whose min/max is a length (string, slice, array, map) rather than a magnitude.
func isCount(k reflect.Kind) bool {
	switch k {
	case reflect.String, reflect.Slice, reflect.Array, reflect.Map:
		return true
	default:
		return false
	}
}

func params(fe validator.FieldError) map[string]any {
	p := fe.Param()
	if p == "" {
		return nil
	}
	switch fe.Tag() {
	case "min", "gte", "gt":
		return map[string]any{"min": number(p)}
	case "max", "lte", "lt":
		return map[string]any{"max": number(p)}
	case "oneof":
		return map[string]any{"expected": strings.Fields(p)}
	default:
		return nil
	}
}

func number(s string) any {
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	return s
}
