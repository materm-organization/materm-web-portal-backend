// Package controller is the HTTP layer of the forms module: it parses the request, negotiates the locale and
// hands DTOs to the service. It never builds an error body (it returns apperror values) and never logs the
// body, the query, the address or the user agent of a submission (doc/08 section 2.10).
package controller

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/readkit"
	"github.com/materm-organization/materm-web-portal-backend/modules/forms/models"
	"github.com/materm-organization/materm-web-portal-backend/modules/forms/services"
	"github.com/materm-organization/materm-web-portal-backend/pkg/apperror"
	"github.com/materm-organization/materm-web-portal-backend/pkg/clientip"
	"github.com/materm-organization/materm-web-portal-backend/pkg/localize"
	"github.com/materm-organization/materm-web-portal-backend/pkg/portalkey"
	"github.com/materm-organization/materm-web-portal-backend/pkg/ratelimit"
	"github.com/materm-organization/materm-web-portal-backend/pkg/response"
)

// FormCtrl serves P-14 and P-15.
type FormCtrl interface {
	Definition(c *fiber.Ctx) error
	Submit(c *fiber.Ctx) error
}

type formCtrl struct {
	svc     services.FormService
	locales *localize.Provider
}

// NewFormCtrl builds the controller.
func NewFormCtrl(svc services.FormService, locales *localize.Provider) FormCtrl {
	return &formCtrl{svc: svc, locales: locales}
}

// Definition is P-14: GET /public/forms/:slug.
func (h *formCtrl) Definition(c *fiber.Ctx) error {
	neg, _, err := readkit.Negotiate(c, h.locales)
	if err != nil {
		return err
	}
	def, err := h.svc.Definition(c.UserContext(), neg.Code, c.Params("slug"))
	if err != nil {
		return err
	}
	return readkit.Send(c, neg, readkit.OKBody(def))
}

// Submit is P-15: POST /public/forms/:slug/submissions. The answer is 200 {status:"received"} for a normal and
// for a spam submission alike.
func (h *formCtrl) Submit(c *fiber.Ctx) error {
	body := c.Body()
	if !c.Is("json") {
		return apperror.UnsupportedMediaType()
	}
	req, err := decodeSubmission(body)
	if err != nil {
		return err
	}
	locale, err := h.negotiateBodyLocale(c, req.Locale)
	if err != nil {
		return err
	}
	visitor := services.Visitor{
		IP: clientip.Resolve(c).Addr, UserAgent: clientip.UserAgent(c), KeyID: KeyID(c.Get(portalkey.Header)),
	}
	if err := h.svc.Submit(c.UserContext(), services.SubmitInput{
		Slug: c.Params("slug"), Locale: locale, BodySize: len(body), Request: req, Visitor: visitor,
	}); err != nil {
		return err
	}
	return response.OK(c, models.Received{Status: models.StatusReceived})
}

// KeyID names a portal key without revealing it: the first 16 hex characters of its sha256. It is the key of
// the aggregate rate limit (doc/10 R-020).
func KeyID(presented string) string { return ratelimit.Hash(presented)[:16] }

// negotiateBodyLocale is ResolveLocale for the body's `locale` (a POST has no ?locale and ignores
// Accept-Language): a malformed code is a 422 at /locale in the body, an inactive one is the default locale.
func (h *formCtrl) negotiateBodyLocale(c *fiber.Ctx, requested string) (string, error) {
	chain, err := h.locales.Chain(c.UserContext())
	if err != nil {
		return "", apperror.ServiceUnavailable(err, 0)
	}
	requested = strings.TrimSpace(requested)
	if requested != "" && !localize.ValidQueryLocale(requested) {
		return "", apperror.Validation(apperror.Field("/locale", "invalid_format"))
	}
	neg, aerr := chain.Negotiate(requested, "")
	if aerr != nil {
		return "", aerr
	}
	return neg.Code, nil
}

// decodeSubmission parses the body strictly: one JSON object, only the documented members. Malformed JSON is
// a 400; an unknown member or a member of the wrong type is a 422 naming the member (never its value).
func decodeSubmission(body []byte) (models.SubmissionRequest, error) {
	var req models.SubmissionRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err := dec.Decode(&req); err != nil {
		var typeErr *json.UnmarshalTypeError
		var syntaxErr *json.SyntaxError
		switch {
		case errors.As(err, &typeErr):
			return req, apperror.Validation(apperror.Field("/"+pointer(typeErr.Field), "invalid_type"))
		case errors.As(err, &syntaxErr), errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
			return req, apperror.BadRequest()
		case strings.HasPrefix(err.Error(), "json: unknown field "):
			name := strings.Trim(strings.TrimPrefix(err.Error(), "json: unknown field "), `"`)
			return req, apperror.Validation(apperror.Field("/"+pointer(name), "unknown_field"))
		}
		return req, apperror.BadRequest()
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return req, apperror.BadRequest() // trailing data after the object
	}
	if req.Data == nil {
		req.Data = map[string]any{}
	}
	return req, nil
}

// pointer makes a client-chosen member name safe to echo in a JSON Pointer: bounded, escaped, no controls.
func pointer(s string) string {
	if len(s) > 60 {
		s = s[:60]
	}
	s = strings.ReplaceAll(s, "~", "~0")
	s = strings.ReplaceAll(s, "/", "~1")
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '?'
		}
		return r
	}, s)
}
