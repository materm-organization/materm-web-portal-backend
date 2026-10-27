// Package controller holds the HTTP handlers of the pages module.
package controller

import (
	"errors"
	"regexp"

	"github.com/gofiber/fiber/v2"

	"github.com/materm-organization/materm-web-portal-backend/modules/pages/services"
	"github.com/materm-organization/materm-web-portal-backend/pkg/apperror"
	"github.com/materm-organization/materm-web-portal-backend/pkg/assemble/httpx"
	"github.com/materm-organization/materm-web-portal-backend/pkg/response"
)

// PagesCtrl is the HTTP surface of the pages module.
type PagesCtrl interface {
	Resolve(c *fiber.Ctx) error
}

type pagesCtrl struct {
	svc    services.PageService
	chains httpx.ChainSource
}

// NewPagesCtrl builds the controller. chains is the locale provider (localize.Provider).
func NewPagesCtrl(svc services.PageService, chains httpx.ChainSource) PagesCtrl {
	return &pagesCtrl{svc: svc, chains: chains}
}

// PathPattern is the CHECK of cms_pages.path (doc/03): "/" or segments of [a-z0-9-] joined by "/", at most 200
// bytes.
var PathPattern = regexp.MustCompile(`^/([a-z0-9-]+(/[a-z0-9-]+)*)?$`)

const maxPathLen = 200

// Resolve is P-06: GET /public/pages/resolve?path=&locale=
func (h *pagesCtrl) Resolve(c *fiber.Ctx) error {
	path := c.Query("path")
	switch {
	case path == "":
		return apperror.Validation(apperror.FieldError{In: apperror.InQuery, Path: "/path", Code: "required"})
	case len(path) > maxPathLen || !PathPattern.MatchString(path):
		return apperror.Validation(apperror.FieldError{In: apperror.InQuery, Path: "/path", Code: "invalid_format",
			Params: map[string]any{"pattern": PathPattern.String(), "max": maxPathLen}})
	}
	neg, err := httpx.Negotiate(c, h.chains)
	if err != nil {
		return err
	}
	d, err := h.svc.Resolve(c.UserContext(), neg, path)
	if errors.Is(err, services.ErrNotFound) {
		return httpx.NotFound(c)
	}
	if err != nil {
		return err
	}
	return httpx.Send(c, &neg, response.Envelope{Success: true, Data: d})
}
