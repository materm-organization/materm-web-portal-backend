// Package controller is the HTTP layer of the faq module.
package controller

import (
	"github.com/gofiber/fiber/v2"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/readkit"
	"github.com/materm-organization/materm-web-portal-backend/modules/faq/services"
	"github.com/materm-organization/materm-web-portal-backend/pkg/localize"
)

// FaqCtrl serves P-13.
type FaqCtrl interface {
	Group(c *fiber.Ctx) error
}

type faqCtrl struct {
	svc     services.FaqService
	locales *localize.Provider
}

// NewFaqCtrl builds the controller.
func NewFaqCtrl(svc services.FaqService, locales *localize.Provider) FaqCtrl {
	return &faqCtrl{svc: svc, locales: locales}
}

// Group is P-13: GET /public/faq/:group_key.
//
// @Summary FAQ group (P-13)
// @Description Questions and answers of one FAQ group.
// @Tags Faq
// @Accept json
// @Produce json
// @Security PortalKey
// @Param group_key path string true "FAQ group key"
// @Param locale query string false "Content locale (^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$, lo = la). Absent: Accept-Language, then the default locale"
// @Param Accept-Language header string false "Used when locale is absent"
// @Param If-None-Match header string false "Weak ETag of an earlier answer; a match gives 304 without a body"
// @Success 200 {object} response.Envelope{data=models.FaqGroup} "OK (ETag and Cache-Control set; Content-Language when the locale was negotiated)"
// @Success 304 "Not modified: If-None-Match matched, no body"
// @Failure 401 {object} apperror.Body "Missing or invalid X-Portal-Key"
// @Failure 422 {object} apperror.Body "Invalid parameter (errors[] names it)"
// @Failure 404 {object} apperror.Body "Not found (cacheable for 30 s)"
// @Failure 503 {object} apperror.Body "Database unavailable"
// @Router /api/v1/public/faq/{group_key} [get]
func (h *faqCtrl) Group(c *fiber.Ctx) error {
	neg, _, err := readkit.Negotiate(c, h.locales)
	if err != nil {
		return err
	}
	g, err := h.svc.Group(c.UserContext(), neg.Code, c.Params("group_key"))
	if err != nil {
		return err
	}
	return readkit.Send(c, neg, readkit.OKBody(g))
}
