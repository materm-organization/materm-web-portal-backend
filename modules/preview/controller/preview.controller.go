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
//
// @Summary Resolve a draft preview (P-19)
// @Description Returns the frozen page or post snapshot that a preview token points at. The token travels only in the X-Preview-Token header (never the query string). Every answer, errors included, is Cache-Control: no-store and X-Robots-Tag: noindex, nofollow, noarchive. Requires the portal key as well.
// @Tags Preview
// @Accept json
// @Produce json
// @Security PortalKey
// @Param X-Preview-Token header string true "Preview token (32..256 chars of [A-Za-z0-9._~-])"
// @Param path query string false "Optional page path the token must belong to (max 300 bytes)"
// @Param locale query string false "Content locale (^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$, lo = la). Absent: Accept-Language, then the default locale"
// @Param Accept-Language header string false "Used when locale is absent"
// @Success 200 {object} response.Envelope{data=models.PreviewData} "Snapshot (data.kind is page or post)"
// @Failure 401 {object} apperror.Body "Missing or invalid X-Portal-Key, or invalid, expired or unknown preview token (code INVALID_TOKEN)"
// @Failure 422 {object} apperror.Body "Malformed X-Preview-Token, path or locale"
// @Failure 429 {object} apperror.Body "Too many preview attempts (Retry-After set)"
// @Failure 503 {object} apperror.Body "Database unavailable"
// @Router /api/v1/preview/resolve [get]
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
