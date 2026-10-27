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
