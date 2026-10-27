// Package readkit is the plumbing that the read endpoints of the content modules (blog, gallery, faq, forms;
// doc/08 section 2) have in common, so that each module's controller is a few lines: locale negotiation,
// the cache headers with ETag and 304, the list envelope with a top-level media map, the 308 envelope, the
// date formatter, the rich-text link resolver and the cache-control of error answers.
//
// Nothing here knows a module. It lives under modules/content (shared by every content module) because it
// needs the content repositories; pkg/ stays free of module code.
package readkit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/materm-organization/materm-web-portal-backend/pkg/apperror"
	"github.com/materm-organization/materm-web-portal-backend/pkg/localize"
	"github.com/materm-organization/materm-web-portal-backend/pkg/media"
	"github.com/materm-organization/materm-web-portal-backend/pkg/pagination"
)

// Cache-Control values (doc/08 section 2.1).
const (
	// CacheControlOK is sent with every 200 of a GET.
	CacheControlOK = "public, max-age=30, stale-while-revalidate=300, stale-if-error=86400"
	// CacheControlShort is sent with 404 and 308 of a GET.
	CacheControlShort = "public, max-age=30"
	// CacheControlNone is sent with 5xx, 4xx other than 404 and with every POST.
	CacheControlNone = "no-store"
)

// Patterns of the path parameters (CHECK constraints of doc/03).
var (
	// KeyPattern: cms_albums.key, cms_faq_groups.key, forms_forms.slug, cms_categories.key (<= 60 characters).
	KeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,59}$`)
	// SlugPattern: cms_post_translations.slug.
	SlugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	// TagKeyPattern: cms_tags.key.
	TagKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,59}$`)
)

// MaxSlugLen is the longest post slug.
const MaxSlugLen = 100

// Negotiate runs ResolveLocale (doc/08 section 2.1) for a GET: ?locale, then Accept-Language, then the
// default. A bad ?locale is a 422 from localize; a chain that cannot be loaded is a 503.
func Negotiate(c *fiber.Ctx, p *localize.Provider) (localize.Negotiation, *localize.Chain, error) {
	chain, err := p.Chain(c.UserContext())
	if err != nil {
		return localize.Negotiation{}, nil, apperror.ServiceUnavailable(err, 0)
	}
	neg, aerr := chain.Negotiate(c.Query("locale"), c.Get(fiber.HeaderAcceptLanguage))
	if aerr != nil {
		return localize.Negotiation{}, nil, aerr
	}
	return neg, chain, nil
}

// setLanguage writes Content-Language and, when Accept-Language took part, Vary.
func setLanguage(c *fiber.Ctx, neg localize.Negotiation) {
	if neg.ContentLanguage != "" {
		c.Set(fiber.HeaderContentLanguage, neg.ContentLanguage)
	}
	if neg.Vary {
		c.Vary(fiber.HeaderAcceptLanguage)
	}
}

// Send answers 200 with body as JSON, an ETag over the exact bytes sent, the cache headers and the language
// headers; a matching If-None-Match gets 304 with the same headers and no body.
func Send(c *fiber.Ctx, neg localize.Negotiation, body any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return apperror.Internal(err)
	}
	return SendBytes(c, neg, b)
}

// SendBytes is Send over an already encoded body.
func SendBytes(c *fiber.Ctx, neg localize.Negotiation, b []byte) error {
	sum := sha256.Sum256(b)
	etag := `W/"` + hex.EncodeToString(sum[:])[:16] + `"`
	setLanguage(c, neg)
	c.Set(fiber.HeaderETag, etag)
	c.Set(fiber.HeaderCacheControl, CacheControlOK)
	if NoneMatch(c.Get(fiber.HeaderIfNoneMatch), etag) {
		c.Status(fiber.StatusNotModified)
		c.Response().ResetBody()
		return nil
	}
	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
	return c.Status(fiber.StatusOK).Send(b)
}

// NoneMatch reports whether an If-None-Match header matches etag (weak comparison, "*" matches anything).
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

// RedirectBody is the data of a 308 answer (doc/08 section 2.3.3).
type RedirectBody struct {
	Redirect Redirect `json:"redirect"`
}

// Redirect names the target of a 308.
type Redirect struct {
	Location string `json:"location"`
	Status   int    `json:"status"`
}

// SendRedirect answers 308 with the Location header and the envelope {success,data:{redirect}}. It does not
// use c.Redirect, which writes its own body.
func SendRedirect(c *fiber.Ctx, neg localize.Negotiation, location string) error {
	b, err := json.Marshal(envelope{Success: true, Data: RedirectBody{Redirect{Location: location, Status: fiber.StatusPermanentRedirect}}})
	if err != nil {
		return apperror.Internal(err)
	}
	setLanguage(c, neg)
	c.Set(fiber.HeaderLocation, location)
	c.Set(fiber.HeaderCacheControl, CacheControlShort)
	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
	return c.Status(fiber.StatusPermanentRedirect).Send(b)
}

type envelope struct {
	Success bool `json:"success"`
	Data    any  `json:"data"`
}

// OKBody wraps data in the success envelope.
func OKBody(data any) any { return envelope{Success: true, Data: data} }

// MediaList is the list envelope of P-07 and P-11: `data` is a flat array, `meta` and `media` sit beside it
// (doc/08 section 2.3.2/2.3.5: additive to doc/05 section 1.3).
type MediaList[T any] struct {
	Success bool            `json:"success"`
	Data    []T             `json:"data"`
	Meta    pagination.Meta `json:"meta"`
	Media   media.Map       `json:"media"`
}

// NewMediaList builds the envelope (nil items become [], nil media {}).
func NewMediaList[T any](items []T, meta pagination.Meta, m media.Map) MediaList[T] {
	if items == nil {
		items = []T{}
	}
	if m == nil {
		m = media.Map{}
	}
	return MediaList[T]{Success: true, Data: items, Meta: meta, Media: m}
}

// Guard sets the Cache-Control of an error answer before the error handler writes it: 404 is cacheable
// briefly (doc/08 section 2.1), everything else is not. Wrap every GET handler with it.
func Guard(h fiber.Handler) fiber.Handler {
	return func(c *fiber.Ctx) error {
		err := h(c)
		if err == nil {
			return nil
		}
		if apperror.Classify(err).Status == fiber.StatusNotFound {
			c.Set(fiber.HeaderCacheControl, CacheControlShort)
		} else {
			c.Set(fiber.HeaderCacheControl, CacheControlNone)
		}
		return err
	}
}

// NoStore is the Guard of POST handlers: no answer, success or error, may be cached.
func NoStore(h fiber.Handler) fiber.Handler {
	return func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderCacheControl, CacheControlNone)
		return h(c)
	}
}
