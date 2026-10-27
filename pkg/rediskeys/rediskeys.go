// Package rediskeys is the single place that names every Redis key of the portal backend (doc/08 section 2.7).
//
// Every key starts with the service prefix (constants.AppKeyPrefix, "materm-portal:"): the Redis is shared
// with other projects (and with the admin backend, which owns "materm-admin:"), so a key without it must
// never be written, and nothing flushes the database. Tests build Keys with their own random prefix
// (redistest) so that parallel test packages on the one scratch Redis cannot see each other's keys. Raw IP
// addresses never appear in a key: they are hashed (ratelimit.Hash: the first 32 hex characters of sha256),
// and no key carries personal data.
//
// The cache key families (Cache, Tag, Locales, Gen) are used by pkg/publiccache (PB-4); the rate-limit and
// salt families by the read limiter and the forms module (PB-9). They are named here first so that the
// prefix rule and its test cover them from the start.
package rediskeys

import (
	"strconv"
	"time"

	constants "github.com/materm-organization/materm-web-portal-backend/pkg/constants/services"
	"github.com/materm-organization/materm-web-portal-backend/pkg/ratelimit"
)

// Keys builds the key names under one prefix.
type Keys struct{ prefix string }

// New returns Keys under prefix; an empty prefix means constants.AppKeyPrefix.
func New(prefix string) Keys {
	if prefix == "" {
		prefix = constants.AppKeyPrefix
	}
	return Keys{prefix: prefix}
}

// Prefix returns the prefix.
func (k Keys) Prefix() string { return k.prefix }

// Gen is the cache generation number (INCR = logical flush of every cache entry; no TTL).
func (k Keys) Gen() string { return k.prefix + "gen" }

// Inv is the invalidation counter: INCR-ed by every applied invalidation (before the keys are deleted). A cache
// write is refused when it changed since the load started (doc/10 R-024: a slow request that read the
// database before a commit must not write its stale answer after the second delete). It never expires.
func (k Keys) Inv() string { return k.prefix + "inv" }

// Cache is one cached response DTO: materm-portal:c:<gen>:<resource>:<locale>:<paramsHash>. paramsHash is
// the sha1 of the normalised parameters (computed by the caller); resource and locale are fixed vocabularies.
func (k Keys) Cache(gen int64, resource, locale, paramsHash string) string {
	return k.prefix + "c:" + strconv.FormatInt(gen, 10) + ":" + resource + ":" + locale + ":" + paramsHash
}

// Tag is the SET of cache keys that depend on a public tag (doc/08 section 2.8).
func (k Keys) Tag(tag string) string { return k.prefix + "tag:" + tag }

// Locales holds the LocaleChain used by every request (TTL 10 minutes).
func (k Keys) Locales() string { return k.prefix + "locales" }

// Preview holds the snapshot answered for a preview token, addressed by the sha256 of the token (hex), never
// by the token itself (TTL 10 s).
func (k Keys) Preview(tokenSHA256Hex string) string { return k.prefix + "preview:" + tokenSHA256Hex }

// ReadRL counts reads of one client address in one minute (only for callers without a portal key).
func (k Keys) ReadRL(ip string, at time.Time) string {
	return k.prefix + "rl:read:" + ratelimit.Hash(ip) + ":" + at.UTC().Format("200601021504")
}

// FormWindowRL counts submissions of one form by one hashed client in a rate-limit window that started at
// windowStart (TTL = the window).
func (k Keys) FormWindowRL(slug, ipHash string, windowStart time.Time) string {
	return k.prefix + "rl:form:" + slug + ":" + ipHash + ":w:" + strconv.FormatInt(windowStart.UTC().Unix(), 10)
}

// FormDailyRL counts submissions of one form by one hashed client in a UTC day (TTL 25 h).
func (k Keys) FormDailyRL(slug, ipHash string, day time.Time) string {
	return k.prefix + "rl:form:" + slug + ":" + ipHash + ":d:" + day.UTC().Format("20060102")
}

// FormKeyMinuteRL counts every submission made with one portal key (all visitors, all forms) in one UTC
// minute: materm-portal:rl:form-key:<keyid16>:m:<yyyymmddhhmm> (doc/10 R-020). keyID is the truncated hash that
// names the key, never the key.
func (k Keys) FormKeyMinuteRL(keyID string, at time.Time) string {
	return k.prefix + "rl:form-key:" + keyID + ":m:" + at.UTC().Format("200601021504")
}

// FormKeyDailyRL is the same counter for a UTC day: materm-portal:rl:form-key:<keyid16>:d:<yyyymmdd>.
func (k Keys) FormKeyDailyRL(keyID string, at time.Time) string {
	return k.prefix + "rl:form-key:" + keyID + ":d:" + at.UTC().Format("20060102")
}

// FormDup is the marker of one submission (visitor, form, content) that makes an identical retry harmless:
// materm-portal:rl:form:<slug>:<iphash32>:dup:<sha256_32>. contentHash is 32 hex characters of a sha256.
func (k Keys) FormDup(slug, ipHash, contentHash string) string {
	return k.prefix + "rl:form:" + slug + ":" + ipHash + ":dup:" + contentHash
}

// IPSalt is the random daily salt of the IP hash (SETNX, TTL 48 h).
func (k Keys) IPSalt(day time.Time) string {
	return k.prefix + "ipsalt:" + day.UTC().Format("20060102")
}
