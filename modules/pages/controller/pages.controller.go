// Package controller holds the HTTP handlers of the pages module.
package controller

import (
	"errors"
	"regexp"

	"github.com/gofiber/fiber/v2"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/readkit"
	"github.com/materm-organization/materm-web-portal-backend/modules/pages/services"
	"github.com/materm-organization/materm-web-portal-backend/pkg/apperror"
	"github.com/materm-organization/materm-web-portal-backend/pkg/response"
)

// PagesCtrl is the HTTP surface of the pages module.
type PagesCtrl interface {
	Resolve(c *fiber.Ctx) error
}

type pagesCtrl struct {
	svc    services.PageService
	chains readkit.ChainSource
}

// NewPagesCtrl builds the controller. chains is the locale provider (localize.Provider).
func NewPagesCtrl(svc services.PageService, chains readkit.ChainSource) PagesCtrl {
	return &pagesCtrl{svc: svc, chains: chains}
}

// PathPattern is the CHECK of cms_pages.path (doc/03): "/" or segments of [a-z0-9-] joined by "/", at most 200
// bytes.
var PathPattern = regexp.MustCompile(`^/([a-z0-9-]+(/[a-z0-9-]+)*)?$`)

const maxPathLen = 200

// Resolve is P-06: GET /public/pages/resolve?path=&locale=
//
// @Summary Resolve a page by path (P-06)
// @Description Returns the published page at path with its sections assembled (props resolved, media map included).
// @Tags Pages
// @Accept json
// @Produce json
// @Security PortalKey
// @Param path query string true "Page path: / or lowercase segments joined by / (max 200 bytes)"
// @Param locale query string false "Content locale (^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$, lo = la). Absent: Accept-Language, then the default locale"
// @Param Accept-Language header string false "Used when locale is absent"
// @Param If-None-Match header string false "Weak ETag of an earlier answer; a match gives 304 without a body"
// @Success 200 {object} response.Envelope{data=assemble.PageData} "OK (ETag and Cache-Control set; Content-Language when the locale was negotiated)"
// @Success 304 "Not modified: If-None-Match matched, no body"
// @Failure 401 {object} apperror.Body "Missing or invalid X-Portal-Key"
// @Failure 422 {object} apperror.Body "Invalid parameter (errors[] names it)"
// @Failure 404 {object} apperror.Body "Not found (cacheable for 30 s)"
// @Failure 503 {object} apperror.Body "Database unavailable"
// @Router /api/v1/public/pages/resolve [get]
func (h *pagesCtrl) Resolve(c *fiber.Ctx) error {
	path := c.Query("path")
	switch {
	case path == "":
		return apperror.Validation(apperror.FieldError{In: apperror.InQuery, Path: "/path", Code: "required"})
	case len(path) > maxPathLen || !PathPattern.MatchString(path):
		return apperror.Validation(apperror.FieldError{In: apperror.InQuery, Path: "/path", Code: "invalid_format",
			Params: map[string]any{"pattern": PathPattern.String(), "max": maxPathLen}})
	}
	neg, _, err := readkit.Negotiate(c, h.chains)
	if err != nil {
		return err
	}
	d, err := h.svc.Resolve(c.UserContext(), neg, path)
	if errors.Is(err, services.ErrNotFound) {
		return readkit.NotFound(c)
	}
	if err != nil {
		return err
	}
	return readkit.Send(c, neg, response.Envelope{Success: true, Data: d})
}
