package localize

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/materm-organization/materm-web-portal-backend/pkg/apperror"
)

// Source says where the answered locale came from.
type Source string

const (
	// SourceQuery: a valid ?locale that is active.
	SourceQuery Source = "query"
	// SourceHeader: the best active match of Accept-Language.
	SourceHeader Source = "header"
	// SourceDefault: nothing requested matched (or nothing was requested): the default locale.
	SourceDefault Source = "default"
)

// Negotiation is the result of Negotiate.
type Negotiation struct {
	// Code is the locale the response is built in (always active). Cache keys and views use it.
	Code string
	// Chain is the fallback chain of Code (Chain.For).
	Chain  []string
	Source Source
	// Vary is true when Accept-Language took part in the decision (no ?locale): the response must carry
	// `Vary: Accept-Language`.
	Vary bool
	// ContentLanguage is the value for the Content-Language header ("la" is announced as "lo").
	ContentLanguage string
}

// queryLocalePattern is the shape check of ?locale (doc/08 section 2.1): the pattern of
// cms_locales.code. Lower-case primary subtag only, as in the database.
var queryLocalePattern = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)

// ValidQueryLocale reports whether raw has the shape of a locale code.
func ValidQueryLocale(raw string) bool { return len(raw) <= 35 && queryLocalePattern.MatchString(raw) }

// Negotiate implements ResolveLocale (doc/08 section 2.1):
//
//  1. a non-empty ?locale must match the code pattern, otherwise 422 VALIDATION_FAILED (in:"query",
//     path:"/locale"); "lo" is read as "la". An active locale (or the base tag of a region tag) is used.
//  2. without ?locale the best Accept-Language entry that is active wins (q-values, "lo" read as "la",
//     region tags fall back to their base tag); Vary: Accept-Language applies.
//  3. an inactive or unknown locale, or no match, answers the default locale with 200 (FR-LOC-03).
func (c *Chain) Negotiate(queryLocale, acceptLanguage string) (Negotiation, *apperror.Error) {
	queryLocale = strings.TrimSpace(queryLocale)
	if queryLocale != "" {
		if !ValidQueryLocale(queryLocale) {
			return Negotiation{}, apperror.Validation(apperror.FieldError{
				In: apperror.InQuery, Path: "/locale", Code: "invalid_format",
				Params: map[string]any{"pattern": queryLocalePattern.String()},
			})
		}
		if code, ok := c.match(NormalizeCode(queryLocale)); ok {
			return c.result(code, SourceQuery, false), nil
		}
		return c.result(c.def, SourceDefault, false), nil
	}
	if code, ok := c.fromAcceptLanguage(acceptLanguage); ok {
		return c.result(code, SourceHeader, true), nil
	}
	return c.result(c.def, SourceDefault, true), nil
}

func (c *Chain) result(code string, src Source, vary bool) Negotiation {
	return Negotiation{Code: code, Chain: c.For(code), Source: src, Vary: vary, ContentLanguage: ContentLanguage(code)}
}

// maxAcceptEntries bounds the work for a hostile header.
const maxAcceptEntries = 32

type langQ struct {
	tag string
	q   float64
	pos int
}

func (c *Chain) fromAcceptLanguage(header string) (string, bool) {
	header = strings.TrimSpace(header)
	if header == "" || len(header) > 1024 {
		return "", false
	}
	var entries []langQ
	for i, part := range strings.Split(header, ",") {
		if i >= maxAcceptEntries {
			break
		}
		tag, q := parseAcceptEntry(part)
		if tag == "" || tag == "*" || q <= 0 {
			continue
		}
		entries = append(entries, langQ{tag: NormalizeCode(tag), q: q, pos: i})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].q != entries[j].q {
			return entries[i].q > entries[j].q
		}
		return entries[i].pos < entries[j].pos
	})
	for _, e := range entries {
		if code, ok := c.match(e.tag); ok {
			return code, true
		}
	}
	return "", false
}

// parseAcceptEntry splits "en-US;q=0.8" into ("en-US", 0.8). A missing or malformed q is 1 / skipped.
func parseAcceptEntry(part string) (tag string, q float64) {
	q = 1
	fields := strings.Split(part, ";")
	tag = strings.TrimSpace(fields[0])
	for _, p := range fields[1:] {
		p = strings.TrimSpace(p)
		if strings.HasPrefix(p, "q=") || strings.HasPrefix(p, "Q=") {
			v, err := strconv.ParseFloat(strings.TrimSpace(p[2:]), 64)
			if err != nil || v < 0 || v > 1 {
				return "", 0
			}
			q = v
		}
	}
	return tag, q
}
