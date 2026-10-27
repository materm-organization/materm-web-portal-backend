// Package localize is the locale logic of the public API (doc/08 sections 2.1, 2.3.1 and 2.4; doc/04
// section 4.2): which language a request is answered in, the fallback chain of that language, and the collapse
// of the "x-localized" locale maps of section props into a single value.
//
// The chain is built from v_public_locales (code, fallback_code, is_default). The portal role has no grant on
// v_locale_resolution, which the views use internally, so nothing here reads it. Everything in this package
// is pure (no database, no Redis); the caller loads the locales and may cache the Chain.
package localize

import (
	"errors"
	"sort"
	"strings"
)

// Locale is one active language (a row of v_public_locales).
type Locale struct {
	Code         string
	FallbackCode string // "" = none
	IsDefault    bool
}

// Chain is the set of active locales. It is immutable after NewChain and safe for concurrent use.
type Chain struct {
	byCode map[string]Locale
	codes  []string // sorted, for stable output
	def    string
}

// ErrNoDefault is returned by NewChain when the locales do not contain exactly one default.
var ErrNoDefault = errors.New("localize: the active locales must contain exactly one default")

// NewChain validates and indexes the active locales. A fallback that is not active, or that points at the
// locale itself, is ignored (the chain then goes straight to the default), because the database view does the
// same (row-level fallback uses fallback_code only through cms_locales, which has no cycles: BR-05).
func NewChain(locales []Locale) (*Chain, error) {
	c := &Chain{byCode: make(map[string]Locale, len(locales))}
	for _, l := range locales {
		l.Code = strings.ToLower(strings.TrimSpace(l.Code))
		l.FallbackCode = strings.ToLower(strings.TrimSpace(l.FallbackCode))
		if l.Code == "" {
			continue
		}
		c.byCode[l.Code] = l
		c.codes = append(c.codes, l.Code)
		if l.IsDefault {
			if c.def != "" {
				return nil, ErrNoDefault
			}
			c.def = l.Code
		}
	}
	if c.def == "" {
		return nil, ErrNoDefault
	}
	sort.Strings(c.codes)
	return c, nil
}

// Default returns the code of the default locale.
func (c *Chain) Default() string { return c.def }

// Codes returns the active locale codes, sorted. The slice is a copy.
func (c *Chain) Codes() []string { return append([]string(nil), c.codes...) }

// Has reports whether code (already normalised) is an active locale.
func (c *Chain) Has(code string) bool { _, ok := c.byCode[code]; return ok }

// For returns the fallback chain of an active locale: requested, then its fallback, then the default, without
// duplicates ("en" -> [en la]; the default -> [la]). An inactive or unknown code is answered with the
// default's chain (FR-LOC-03).
func (c *Chain) For(code string) []string {
	l, ok := c.byCode[code]
	if !ok {
		return []string{c.def}
	}
	out := []string{l.Code}
	if fb := l.FallbackCode; fb != "" && fb != l.Code && c.Has(fb) {
		out = append(out, fb)
	}
	if out[len(out)-1] != c.def && !contains(out, c.def) {
		out = append(out, c.def)
	}
	return out
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// Codes of the Lao locale: the database uses "la" (the seed and the code), the ISO 639-1 code is "lo"
// (doc/08 Q-08-1). Callers may send either; responses name "lo" in Content-Language.
const (
	codeDB  = "la"
	codeISO = "lo"
)

// NormalizeCode lower-cases a language tag and maps the ISO code of Lao to the database code ("lo" -> "la",
// "lo-LA" -> "la-la"). Region subtags are kept; Negotiate falls back to the base tag when the full tag is not
// active.
func NormalizeCode(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if tag == codeISO {
		return codeDB
	}
	if strings.HasPrefix(tag, codeISO+"-") {
		return codeDB + tag[len(codeISO):]
	}
	return tag
}

// ContentLanguage is the value of the Content-Language header / <html lang> for a database locale code:
// "la" is announced as "lo" (Q-08-1); every other code is unchanged.
func ContentLanguage(code string) string {
	if code == codeDB {
		return codeISO
	}
	return code
}

// match returns the active locale that best matches a normalised tag: the tag itself, otherwise its base tag
// ("en-us" -> "en"). ok=false when none is active.
func (c *Chain) match(tag string) (string, bool) {
	if c.Has(tag) {
		return tag, true
	}
	if i := strings.IndexByte(tag, '-'); i > 0 && c.Has(tag[:i]) {
		return tag[:i], true
	}
	return "", false
}
