// Package controller holds the HTTP handlers of the seo module.
package controller

import (
	"github.com/gofiber/fiber/v2"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/readkit"
	"github.com/materm-organization/materm-web-portal-backend/modules/seo/services"
	"github.com/materm-organization/materm-web-portal-backend/pkg/localize"
	"github.com/materm-organization/materm-web-portal-backend/pkg/response"
)

// SEOCtrl is the HTTP surface of the seo module.
type SEOCtrl interface {
	Sitemap(c *fiber.Ctx) error
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
