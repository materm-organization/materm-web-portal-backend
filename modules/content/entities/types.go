// Package entities holds the read-structs of the 21 public views of the CMS database (doc/03 section 5,
// doc/08 section 2.5). Every struct maps ONE view (TableName returns the view name), has an explicit
// `gorm:"column:..."` on every field and no json tags: services turn entities into DTOs. Nothing here is a
// table and nothing is ever migrated or written: the portal role (cms_portal_ro) can only SELECT the views.
//
// Nullable columns are pointers (database/sql cannot scan NULL into a string). The scanners JSONB and
// StringArray exist because pgx's database/sql adapter hands jsonb and text[] over as strings, which
// json.RawMessage and []string cannot scan.
package entities

import (
	"database/sql/driver"
	"fmt"
)

// JSONB is a jsonb column read as raw JSON bytes (nil for SQL NULL).
type JSONB []byte

// Scan implements sql.Scanner.
func (j *JSONB) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*j = nil
	case []byte:
		*j = append((*j)[:0], v...)
	case string:
		*j = append((*j)[:0], v...)
	default:
		return fmt.Errorf("entities: cannot scan %T into JSONB", src)
	}
	return nil
}

// Value implements driver.Valuer (the read-structs are never written, but GORM may ask).
func (j JSONB) Value() (driver.Value, error) {
	if j == nil {
		return nil, nil
	}
	return string(j), nil
}

// GormDataType tells GORM's schema parser what the column is.
func (JSONB) GormDataType() string { return "jsonb" }

// Bytes returns the raw JSON (nil when the column was NULL).
func (j JSONB) Bytes() []byte { return []byte(j) }

// StringArray is a text[] column.
type StringArray []string

// GormDataType tells GORM's schema parser what the column is.
func (StringArray) GormDataType() string { return "text[]" }

// Scan implements sql.Scanner for the text form of a one-dimensional array: {a,b} with optional "quoted"
// elements and backslash escapes. NULL elements are read as "".
func (a *StringArray) Scan(src any) error {
	var s string
	switch v := src.(type) {
	case nil:
		*a = nil
		return nil
	case []byte:
		s = string(v)
	case string:
		s = v
	case []string:
		*a = append((*a)[:0], v...)
		return nil
	default:
		return fmt.Errorf("entities: cannot scan %T into StringArray", src)
	}
	out, err := parseTextArray(s)
	if err != nil {
		return err
	}
	*a = out
	return nil
}

// Value implements driver.Valuer.
func (a StringArray) Value() (driver.Value, error) {
	if a == nil {
		return nil, nil
	}
	b := []byte{'{'}
	for i, e := range a {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, '"')
		for _, r := range []byte(e) {
			if r == '"' || r == '\\' {
				b = append(b, '\\')
			}
			b = append(b, r)
		}
		b = append(b, '"')
	}
	return string(append(b, '}')), nil
}

func parseTextArray(s string) ([]string, error) {
	if len(s) < 2 || s[0] != '{' || s[len(s)-1] != '}' {
		return nil, fmt.Errorf("entities: %q is not a text array", s)
	}
	body := s[1 : len(s)-1]
	out := []string{}
	for i := 0; i < len(body); {
		var el []byte
		if body[i] == '"' {
			i++
			for ; ; i++ {
				if i >= len(body) {
					return nil, fmt.Errorf("entities: unterminated quote in array")
				}
				if body[i] == '\\' && i+1 < len(body) {
					i++
					el = append(el, body[i])
					continue
				}
				if body[i] == '"' {
					i++
					break
				}
				el = append(el, body[i])
			}
		} else {
			start := i
			for i < len(body) && body[i] != ',' {
				i++
			}
			el = []byte(body[start:i])
			if string(el) == "NULL" {
				el = nil
			}
		}
		out = append(out, string(el))
		if i < len(body) {
			if body[i] != ',' {
				return nil, fmt.Errorf("entities: malformed array")
			}
			i++
		}
	}
	return out, nil
}
