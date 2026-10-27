// Package pagination parses the list query of list endpoints (copied from the admin backend) and builds the `meta` block (doc/05
// section 1.6, doc/00 section 3): page, limit, keyword, sort.
//
//	GET /iam/users?page=2&limit=20&keyword=ann&sort=-last_login_at,name
//
// Out-of-range values are rejected with 422 (never clamped silently); a page past the end is a normal
// answer ([] with the real meta). `sort` and the SQL ORDER BY are built from a per-endpoint allow-list, so
// client text never reaches SQL. Keyset/cursor pagination (LoadMoreQuery) is reserved for public feeds and
// infinite scroll and is not part of the offset list contract.
package pagination

import (
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"

	"github.com/materm-organization/materm-web-portal-backend/pkg/apperror"
)

// Defaults and bounds.
const (
	DefaultPage  = 1
	DefaultLimit = 20
	MaxLimit     = 100
	MaxKeyword   = 100
)

// Meta is the `meta` block that sits next to `data` in a list response.
type Meta struct {
	Total     int64 `json:"total"`
	Page      int   `json:"page"`
	Limit     int   `json:"limit"`
	TotalPage int   `json:"total_page"`
}

// NewMeta builds the meta of a page: total = rows matching the filter (all pages),
// total_page = ceil(total / limit).
func NewMeta(total int64, q ListQuery) Meta {
	m := Meta{Total: total, Page: q.Page, Limit: q.Limit}
	if q.Limit > 0 && total > 0 {
		m.TotalPage = int((total + int64(q.Limit) - 1) / int64(q.Limit))
	}
	return m
}

// Spec is what an endpoint allows.
type Spec struct {
	// Sorts maps the public sort name to the SQL column ("last_login_at" -> "u.last_login_at").
	Sorts map[string]string
	// DefaultSort is applied when `sort` is absent, in the same syntax ("-id", "name").
	DefaultSort string
	// IDColumn is the tie-breaker column (default "id").
	IDColumn string
	// Keyword reports whether the endpoint supports `keyword`; otherwise sending it is a 422 unknown_field.
	Keyword bool
}

// SortKey is one parsed sort term.
type SortKey struct {
	Field  string // public name
	Column string // allow-listed SQL column
	Desc   bool
}

// ListQuery is a validated list request.
type ListQuery struct {
	Page    int
	Limit   int
	Keyword string
	Sort    []SortKey
	idCol   string
}

// Offset is the number of rows to skip.
func (q ListQuery) Offset() int { return (q.Page - 1) * q.Limit }

// OrderBy returns the ORDER BY expression for GORM's .Order(...): the requested keys followed by the id
// tie-breaker, e.g. "u.last_login_at DESC, u.name ASC, id DESC". Only allow-listed column names and
// ASC/DESC appear in it.
func (q ListQuery) OrderBy() string {
	var parts []string
	hasID := false
	for _, s := range q.Sort {
		parts = append(parts, s.Column+dir(s.Desc))
		if s.Column == q.idColumn() {
			hasID = true
		}
	}
	if !hasID {
		desc := true
		if len(q.Sort) > 0 {
			desc = q.Sort[0].Desc
		}
		parts = append(parts, q.idColumn()+dir(desc))
	}
	return strings.Join(parts, ", ")
}

func (q ListQuery) idColumn() string {
	if q.idCol == "" {
		return "id"
	}
	return q.idCol
}

func dir(desc bool) string {
	if desc {
		return " DESC"
	}
	return " ASC"
}

// Like returns the keyword as an ILIKE pattern "%keyword%" with LIKE wildcards escaped; use it with
// `ILIKE ? ESCAPE '\'`. It returns "" when there is no keyword.
func (q ListQuery) Like() string {
	if q.Keyword == "" {
		return ""
	}
	return "%" + EscapeLike(q.Keyword) + "%"
}

// EscapeLike escapes the LIKE wildcards % and _ and the escape character \ in s.
func EscapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// Parse reads page, limit, keyword and sort from the query string of c.
func Parse(c *fiber.Ctx, spec Spec) (ListQuery, error) {
	q, err := ParseValues(c.Query, spec)
	if err != nil {
		return ListQuery{}, err
	}
	return q, nil
}

// ParseValues is Parse over any accessor of query parameters (it returns "" for an absent one). A failure
// is an *apperror.Error (422 VALIDATION_FAILED with `in: "query"` field errors).
func ParseValues(get func(key string, defaultValue ...string) string, spec Spec) (ListQuery, *apperror.Error) {
	var errs []apperror.FieldError
	bad := func(path, code string, params map[string]any) {
		errs = append(errs, apperror.FieldError{In: apperror.InQuery, Path: path, Code: code, Params: params})
	}
	q := ListQuery{Page: DefaultPage, Limit: DefaultLimit, idCol: spec.IDColumn}

	if raw := get("page"); raw != "" {
		n, err := strconv.Atoi(raw)
		switch {
		case err != nil:
			bad("/page", "invalid_type", map[string]any{"expected": "integer"})
		case n < 1:
			bad("/page", "out_of_range", map[string]any{"min": 1})
		default:
			q.Page = n
		}
	}
	if raw := get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		switch {
		case err != nil:
			bad("/limit", "invalid_type", map[string]any{"expected": "integer"})
		case n < 1 || n > MaxLimit:
			bad("/limit", "out_of_range", map[string]any{"min": 1, "max": MaxLimit})
		default:
			q.Limit = n
		}
	}
	if raw := strings.TrimSpace(get("keyword")); raw != "" {
		switch {
		case !spec.Keyword:
			bad("/keyword", "unknown_field", nil)
		case utf8.RuneCountInString(raw) > MaxKeyword:
			bad("/keyword", "too_long", map[string]any{"max": MaxKeyword})
		default:
			q.Keyword = raw
		}
	}

	sortExpr := get("sort")
	if sortExpr == "" {
		sortExpr = spec.DefaultSort
	}
	if sortExpr != "" {
		keys, ok := parseSort(sortExpr, spec.Sorts)
		if !ok {
			allowed := make([]string, 0, len(spec.Sorts))
			for k := range spec.Sorts {
				allowed = append(allowed, k)
			}
			bad("/sort", "not_in_enum", map[string]any{"expected": sortedStrings(allowed)})
		}
		q.Sort = keys
	}

	if len(errs) > 0 {
		return ListQuery{}, apperror.Validation(errs...)
	}
	return q, nil
}

func parseSort(expr string, allowed map[string]string) ([]SortKey, bool) {
	var out []SortKey
	seen := map[string]bool{}
	for _, term := range strings.Split(expr, ",") {
		term = strings.TrimSpace(term)
		if term == "" {
			return nil, false
		}
		desc := false
		if strings.HasPrefix(term, "-") {
			desc, term = true, term[1:]
		}
		col, ok := allowed[term]
		if !ok || seen[term] {
			return nil, false
		}
		seen[term] = true
		out = append(out, SortKey{Field: term, Column: col, Desc: desc})
	}
	return out, len(out) > 0
}

func sortedStrings(in []string) []string {
	out := slices.Clone(in)
	slices.Sort(out)
	return out
}
