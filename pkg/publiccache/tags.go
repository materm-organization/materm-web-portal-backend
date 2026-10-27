package publiccache

import (
	"regexp"
	"strings"
)

// The public tag vocabulary (doc/08 section 2.8). A tag is only ever turned into a Redis key after it passed
// ValidTag, so a hostile or buggy message cannot make the portal touch keys outside tag:<known shape>.
//
// The five INTERNAL tag shapes the outbox trigger writes (shared_section:<id>, menu_id:<id>, menu_item:<id>,
// category:<id>, collection_id:<id>) are not in the vocabulary on purpose: the relay must translate them into
// public tags (doc/08 section 2.7 relay contract, item 6), and an untranslated one is ignored here.

// exactTags are the tags without a parameter. "*" flushes everything.
var exactTags = map[string]struct{}{
	"*": {}, "pages": {}, "posts:list": {}, "albums:list": {}, "settings": {}, "locales": {}, "sitemap": {}, "redirects": {},
}

var (
	idPattern     = regexp.MustCompile(`^[1-9][0-9]{0,17}$`)
	keyPattern    = regexp.MustCompile(`^[a-z0-9]+([_-][a-z0-9]+)*$`)
	localePattern = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)
)

const maxTagLen = 120

// ValidTag reports whether tag belongs to the public vocabulary: the exact tags "*", pages, posts:list,
// albums:list, settings, locales, sitemap, redirects; page:<id>, post:<id>, album:<id>, faq:<id>, form:<id>
// (id = positive integer); menu:<key> and collection:<key> (lower-case words joined by _ or -); ui:<locale>.
func ValidTag(tag string) bool {
	if tag == "" || len(tag) > maxTagLen {
		return false
	}
	if _, ok := exactTags[tag]; ok {
		return true
	}
	kind, arg, found := strings.Cut(tag, ":")
	if !found {
		return false
	}
	switch kind {
	case "page", "post", "album", "faq", "form":
		return idPattern.MatchString(arg)
	case "menu", "collection":
		return len(arg) <= 64 && keyPattern.MatchString(arg)
	case "ui":
		return len(arg) <= 35 && localePattern.MatchString(arg)
	}
	return false
}

// FilterTags keeps the valid tags (deduplicated, in first-seen order) and counts the rest.
func FilterTags(in []string) (valid []string, rejected int) {
	seen := make(map[string]struct{}, len(in))
	for _, t := range in {
		if !ValidTag(t) {
			rejected++
			continue
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		valid = append(valid, t)
	}
	return valid, rejected
}

// flushes reports whether tag invalidates every cache entry (a generation bump).
func flushes(tag string) bool { return tag == "*" || tag == "locales" }
