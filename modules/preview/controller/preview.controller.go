// Package controller is the HTTP layer of the preview module. It reads the token from the X-Preview-Token
// header only (never the query string), never logs or echoes it, and marks every answer - success or error -
// as uncacheable and not for indexing.
package controller

import (
	"regexp"

	"github.com/gofiber/fiber/v2"

	"github.com/materm-organization/materm-web-portal-backend/modules/preview/services"
	"github.com/materm-organization/materm-web-portal-backend/pkg/apperror"
	"github.com/materm-organization/materm-web-portal-backend/pkg/localize"
	"github.com/materm-organization/materm-web-portal-backend/pkg/portalkey"
	"github.com/materm-organization/materm-web-portal-backend/pkg/ratelimit"
	"github.com/materm-organization/materm-web-portal-backend/pkg/response"
)

// HeaderToken carries the raw preview token.
const HeaderToken = "X-Preview-Token"

// Bounds of openapi-portal.yaml (X-Preview-Token minLength 32, maxLength 256; path maxLength 300).
const (
	minTokenLen = 32
	maxTokenLen = 256
	maxPathLen  = 300
)

// tokenPattern: base64url and a few more URI-unreserved characters.
var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)

// PreviewCtrl is the HTTP surface of the preview module.
type PreviewCtrl interface {
	Resolve(c *fiber.Ctx) error
}

type previewCtrl struct{ svc services.PreviewService }

// NewPreviewCtrl builds the controller.
func NewPreviewCtrl(svc services.PreviewService) PreviewCtrl { return &previewCtrl{svc: svc} }

// Resolve is P-19: GET /preview/resolve (header X-Preview-Token, ?locale, ?path).
func (h *previewCtrl) Resolve(c *fiber.Ctx) error {
	// before anything can fail, so that error answers carry them too
	c.Set(fiber.HeaderCacheControl, "no-store")
	c.Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	c.Vary(HeaderToken)

	token := c.Get(HeaderToken)
	switch {
	case token == "":
		return apperror.Validation(apperror.FieldError{In: apperror.InHeader, Path: "/" + HeaderToken, Code: "required"})
	case len(token) < minTokenLen || len(token) > maxTokenLen:
		return apperror.Validation(apperror.FieldError{In: apperror.InHeader, Path: "/" + HeaderToken, Code: "invalid_length",
			Params: map[string]any{"min": minTokenLen, "max": maxTokenLen}})
	case !tokenPattern.MatchString(token):
		return apperror.Validation(apperror.FieldError{In: apperror.InHeader, Path: "/" + HeaderToken, Code: "invalid_format"})
	}
	path := c.Query("path")
	if len(path) > maxPathLen {
		return apperror.Validation(apperror.FieldError{In: apperror.InQuery, Path: "/path", Code: "too_long", Params: map[string]any{"max": maxPathLen}})
	}
	qloc := c.Query("locale")
	if qloc != "" && !localize.ValidQueryLocale(qloc) {
		return apperror.Validation(apperror.FieldError{In: apperror.InQuery, Path: "/locale", Code: "invalid_format"})
	}

	res, err := h.svc.Resolve(c.UserContext(), services.Input{
		Token: token, Path: path, QueryLocale: qloc, AcceptLanguage: c.Get(fiber.HeaderAcceptLanguage),
		KeyID: ratelimit.Hash(c.Get(portalkey.Header))[:16],
	})
	if err != nil {
		return err
	}
	if res.Neg.ContentLanguage != "" {
		c.Set(fiber.HeaderContentLanguage, res.Neg.ContentLanguage)
	}
	if res.Neg.Vary {
		c.Vary(fiber.HeaderAcceptLanguage)
	}
	return response.OK(c, res.Data)
}
