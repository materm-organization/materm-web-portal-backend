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
