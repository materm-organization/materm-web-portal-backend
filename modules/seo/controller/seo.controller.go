// Package controller holds the HTTP handlers of the seo module.
package controller

import (
	"errors"

	"github.com/gofiber/fiber/v2"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/readkit"
	"github.com/materm-organization/materm-web-portal-backend/modules/seo/services"
	"github.com/materm-organization/materm-web-portal-backend/pkg/apperror"
	"github.com/materm-organization/materm-web-portal-backend/pkg/localize"
	"github.com/materm-organization/materm-web-portal-backend/pkg/response"
)

// SEOCtrl is the HTTP surface of the seo module.
type SEOCtrl interface {
	Sitemap(c *fiber.Ctx) error
	Redirect(c *fiber.Ctx) error
}

type seoCtrl struct {
	svc    services.SEOService
	chains readkit.ChainSource
}

// NewSEOCtrl builds the controller. chains is the locale provider (localize.Provider), used to find the
// default locale.
func NewSEOCtrl(svc services.SEOService, chains readkit.ChainSource) SEOCtrl {
	return &seoCtrl{svc: svc, chains: chains}
}

// Sitemap is P-16: GET /public/sitemap. It takes no locale: the sitemap is always in the default locale.
//
// @Summary Sitemap entries (P-16)
// @Description Indexable pages and posts with their alternates. Always in the default locale; takes no locale.
// @Tags Seo
// @Accept json
// @Produce json
// @Security PortalKey
// @Param If-None-Match header string false "Weak ETag of an earlier answer; a match gives 304 without a body"
// @Success 200 {object} response.Envelope{data=models.SitemapData} "OK (ETag and Cache-Control set; Content-Language when the locale was negotiated)"
// @Success 304 "Not modified: If-None-Match matched, no body"
// @Failure 401 {object} apperror.Body "Missing or invalid X-Portal-Key"
// @Failure 422 {object} apperror.Body "Invalid parameter (errors[] names it)"
// @Failure 503 {object} apperror.Body "Database unavailable"
// @Router /api/v1/public/sitemap [get]
func (h *seoCtrl) Sitemap(c *fiber.Ctx) error {
	chain, err := readkit.LoadChain(c, h.chains)
	if err != nil {
		return err
	}
	d, err := h.svc.Sitemap(c.UserContext(), chain.Default())
	if err != nil {
		return err
	}
	return readkit.Send(c, localize.Negotiation{}, response.Envelope{Success: true, Data: d})
}

// maxRawPath bounds the raw query value before it is normalised (a long query string after the path is cut off).
const maxRawPath = 2048

// Redirect is P-17: GET /public/redirects/resolve?path=&locale=
//
// @Summary Resolve a redirect (P-17)
// @Description Looks up the active redirect rules for path (lower-cased, query and fragment dropped, trailing slash removed; the rule of the locale wins over a rule for all locales), follows internal targets up to 3 hops on the server and answers the final target and the status_code of the LAST rule. A loop is a 404. Found and not-found answers are cached (not found for 30 s).
// @Tags Seo
// @Accept json
// @Produce json
// @Security PortalKey
// @Param path query string true "Request path starting with / (max 512 bytes after normalising)"
// @Param locale query string false "Locale (^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$, lo = la). Absent: Accept-Language, then the default locale"
// @Param Accept-Language header string false "Used when locale is absent"
// @Param If-None-Match header string false "Weak ETag of an earlier answer; a match gives 304 without a body"
// @Success 200 {object} response.Envelope{data=models.RedirectData} "OK: target, status_code (301/302/307/308), hops (1-3)"
// @Success 304 "Not modified: If-None-Match matched, no body"
// @Failure 401 {object} apperror.Body "Missing or invalid X-Portal-Key"
// @Failure 404 {object} apperror.Body "NOT_FOUND: no active redirect (cacheable for 30 s)"
// @Failure 422 {object} apperror.Body "Invalid parameter (errors[] names it)"
// @Failure 503 {object} apperror.Body "Database unavailable"
// @Router /api/v1/public/redirects/resolve [get]
func (h *seoCtrl) Redirect(c *fiber.Ctx) error {
	raw := c.Query("path")
	if raw == "" {
		return apperror.Validation(apperror.FieldError{In: apperror.InQuery, Path: "/path", Code: "required"})
	}
	path := services.NormalizePath(raw)
	if len(raw) > maxRawPath || len(path) > services.MaxPathLen || path == "" || path[0] != '/' || hasControl(path) {
		return apperror.Validation(apperror.FieldError{In: apperror.InQuery, Path: "/path", Code: "invalid_format",
			Params: map[string]any{"max": services.MaxPathLen}})
	}
	neg, _, err := readkit.Negotiate(c, h.chains)
	if err != nil {
		return err
	}
	d, err := h.svc.Redirect(c.UserContext(), neg.Code, path)
	if errors.Is(err, services.ErrRedirectNotFound) {
		return readkit.NotFound(c)
	}
	if err != nil {
		return err
	}
	return readkit.Send(c, neg, response.Envelope{Success: true, Data: d})
}

// hasControl reports a space or control character (or DEL): never part of a path that was percent-encoded.
func hasControl(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] <= ' ' || s[i] == 0x7f {
			return true
		}
	}
	return false
}
