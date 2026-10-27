// Package httpx is the HTTP side of the public read endpoints that every content module shares (doc/08 section
// 2.1): locale negotiation, the success headers (Cache-Control, a weak ETag, Vary, Content-Language) and the
// conditional GET (304 without a body). Modules call it from their controllers so that site, pages, seo and the
// modules of T1.2.8 (blog, gallery, faq, forms) answer the same way.
//
// Response bodies are built by the caller as the envelope value (pkg/response.Envelope for `{success,data}`, or
// a struct that adds meta/media beside data). Send marshals it once, derives the ETag from the exact bytes, and
// writes either the body or a 304.
package httpx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/materm-organization/materm-web-portal-backend/pkg/apperror"
	"github.com/materm-organization/materm-web-portal-backend/pkg/localize"
)

// CacheControlOK is the Cache-Control of a successful GET (doc/08 section 2.1): a short shared lifetime with
// stale-while-revalidate / stale-if-error for a CDN or proxy in front of the portal.
const CacheControlOK = "public, max-age=30, stale-while-revalidate=300, stale-if-error=86400"

// CacheControlNotFound is the Cache-Control of a 404 (doc/08 section 2.1).
const CacheControlNotFound = "public, max-age=30"

// CacheControlNone is the Cache-Control of 5xx and of every other error but 404 (doc/08 section 2.1).
const CacheControlNone = "no-store"

// ChainSource hands out the locale Chain (localize.Provider implements it).
type ChainSource interface {
	Chain(ctx context.Context) (*localize.Chain, error)
}

// Negotiate resolves the locale of a request (?locale, then Accept-Language, then the default) against the
// active locales. An invalid ?locale is 422 VALIDATION_FAILED. The returned error is a plain error interface
// holding a *apperror.Error, never a typed nil.
func Negotiate(c *fiber.Ctx, p ChainSource) (localize.Negotiation, error) {
	chain, err := LoadChain(c, p)
	if err != nil {
		return localize.Negotiation{}, err
	}
	return NegotiateWith(c, chain)
}

// LoadChain returns the active locales' Chain; when they cannot be loaded nothing can be answered: 503.
func LoadChain(c *fiber.Ctx, p ChainSource) (*localize.Chain, error) {
	chain, err := p.Chain(c.UserContext())
	if err != nil {
		return nil, apperror.ServiceUnavailable(err, 0)
	}
	return chain, nil
}

// NegotiateWith is Negotiate for a Chain that is already loaded.
func NegotiateWith(c *fiber.Ctx, chain *localize.Chain) (localize.Negotiation, error) {
	neg, aerr := chain.Negotiate(c.Query("locale"), c.Get(fiber.HeaderAcceptLanguage))
	if aerr != nil {
		return localize.Negotiation{}, aerr
	}
	return neg, nil
}

// ETag is the weak validator of a body: W/"<first 16 hex of sha256(body)>".
func ETag(body []byte) string {
	sum := sha256.Sum256(body)
	return `W/"` + hex.EncodeToString(sum[:8]) + `"`
}

// Send writes a 200 JSON response for the envelope value env: Cache-Control, ETag, Vary: Accept-Language (only
// when neg says the header took part in the decision), Content-Language (when neg is not nil) and, if the
// request's If-None-Match matches, a 304 with the same headers and no body. neg may be nil for language
// independent resources (P-01).
func Send(c *fiber.Ctx, neg *localize.Negotiation, env any) error {
	body, err := json.Marshal(env)
	if err != nil {
		return err
	}
	etag := ETag(body)
	c.Set(fiber.HeaderCacheControl, CacheControlOK)
	c.Set(fiber.HeaderETag, etag)
	if neg != nil {
		if neg.Vary {
			c.Vary(fiber.HeaderAcceptLanguage)
		}
		c.Set(fiber.HeaderContentLanguage, neg.ContentLanguage)
	}
	if NoneMatch(c.Get(fiber.HeaderIfNoneMatch), etag) {
		return c.SendStatus(fiber.StatusNotModified)
	}
	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
	return c.Status(fiber.StatusOK).Send(body)
}

// Guard wraps a GET handler so that an error answer carries the right Cache-Control, set before the error
// handler writes it: 404 is cacheable for a short time, every other error (422, 5xx) is not. A success sets
// its own headers in Send. Register every public read route through it.
func Guard(h fiber.Handler) fiber.Handler {
	return func(c *fiber.Ctx) error {
		err := h(c)
		if err == nil {
			return nil
		}
		if apperror.Classify(err).Status == fiber.StatusNotFound {
			c.Set(fiber.HeaderCacheControl, CacheControlNotFound)
		} else {
			c.Set(fiber.HeaderCacheControl, CacheControlNone)
		}
		return err
	}
}

// NotFound marks the coming 404 as cacheable for a short time (doc/08 section 2.1) and returns the error to
// hand back from the handler. The error handler (pkg/apperror) keeps the header.
func NotFound(c *fiber.Ctx) error {
	c.Set(fiber.HeaderCacheControl, CacheControlNotFound)
	return apperror.NotFound()
}

// NoneMatch reports whether an If-None-Match header value matches etag (weak comparison, "*" and lists
// included).
func NoneMatch(header, etag string) bool {
	header = strings.TrimSpace(header)
	if header == "" {
		return false
	}
	if header == "*" {
		return true
	}
	want := strings.TrimPrefix(etag, "W/")
	for _, part := range strings.Split(header, ",") {
		if strings.TrimPrefix(strings.TrimSpace(part), "W/") == want {
			return true
		}
	}
	return false
}
