// Package controller holds the HTTP handlers of the site module.
package controller

import (
	"errors"
	"regexp"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/readkit"
	"github.com/materm-organization/materm-web-portal-backend/modules/site/services"
	"github.com/materm-organization/materm-web-portal-backend/pkg/apperror"
	"github.com/materm-organization/materm-web-portal-backend/pkg/localize"
	"github.com/materm-organization/materm-web-portal-backend/pkg/response"
)

// SiteCtrl is the HTTP surface of the site module (P-01 ... P-05).
type SiteCtrl interface {
	Locales(c *fiber.Ctx) error
	Settings(c *fiber.Ctx) error
	UIStrings(c *fiber.Ctx) error
	Menu(c *fiber.Ctx) error
	Site(c *fiber.Ctx) error
}

type siteCtrl struct {
	svc    services.SiteService
	chains readkit.ChainSource
}

// NewSiteCtrl builds the controller. chains is the locale provider (localize.Provider).
func NewSiteCtrl(svc services.SiteService, chains readkit.ChainSource) SiteCtrl {
	return &siteCtrl{svc: svc, chains: chains}
}

var (
	groupPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
	menuPattern  = regexp.MustCompile(`^[a-z0-9]+([_-][a-z0-9]+)*$`)
)

// AllowedUINamespaces are the namespaces P-03 accepts in ?ns (doc/08 section 2.2).
var AllowedUINamespaces = map[string]bool{"site": true, "blog": true, "forms": true}

const maxListItems = 12

// P-01 GET /public/locales
//
// @Summary Active locales (P-01)
// @Description Active locales and the default one. Language independent.
// @Tags Site
// @Accept json
// @Produce json
// @Security PortalKey
// @Param If-None-Match header string false "Weak ETag of an earlier answer; a match gives 304 without a body"
// @Success 200 {object} response.Envelope{data=[]assemble.LocaleInfo} "OK (ETag and Cache-Control set; Content-Language when the locale was negotiated)"
// @Success 304 "Not modified: If-None-Match matched, no body"
// @Failure 401 {object} apperror.Body "Missing or invalid X-Portal-Key"
// @Failure 422 {object} apperror.Body "Invalid parameter (errors[] names it)"
// @Failure 503 {object} apperror.Body "Database unavailable"
// @Router /api/v1/public/locales [get]
func (h *siteCtrl) Locales(c *fiber.Ctx) error {
	d, err := h.svc.Locales(c.UserContext())
	if err != nil {
		return err
	}
	return readkit.Send(c, localize.Negotiation{}, response.Envelope{Success: true, Data: d})
}

// P-02 GET /public/settings?locale=&group=brand,footer
//
// @Summary Site settings (P-02)
// @Description Public settings of the requested groups, resolved for the locale, with the media map of the assets they reference.
// @Tags Site
// @Accept json
// @Produce json
// @Security PortalKey
// @Param group query string false "Comma separated setting groups (^[a-z][a-z0-9_]{0,39}$, at most 12); absent = all groups"
// @Param locale query string false "Content locale (^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$, lo = la). Absent: Accept-Language, then the default locale"
// @Param Accept-Language header string false "Used when locale is absent"
// @Param If-None-Match header string false "Weak ETag of an earlier answer; a match gives 304 without a body"
// @Success 200 {object} response.Envelope{data=models.SettingsData} "OK (ETag and Cache-Control set; Content-Language when the locale was negotiated)"
// @Success 304 "Not modified: If-None-Match matched, no body"
// @Failure 401 {object} apperror.Body "Missing or invalid X-Portal-Key"
// @Failure 422 {object} apperror.Body "Invalid parameter (errors[] names it)"
// @Failure 503 {object} apperror.Body "Database unavailable"
// @Router /api/v1/public/settings [get]
func (h *siteCtrl) Settings(c *fiber.Ctx) error {
	groups, err := csvQuery(c, "group", func(v string) bool { return groupPattern.MatchString(v) })
	if err != nil {
		return err
	}
	neg, _, err := readkit.Negotiate(c, h.chains)
	if err != nil {
		return err
	}
	d, err := h.svc.Settings(c.UserContext(), neg, groups)
	if err != nil {
		return err
	}
	return readkit.Send(c, neg, response.Envelope{Success: true, Data: d})
}

// P-03 GET /public/ui-strings?locale=&ns=site,blog
//
// @Summary UI strings (P-03)
// @Description UI strings of the requested namespaces for the locale (fallback chain applied).
// @Tags Site
// @Accept json
// @Produce json
// @Security PortalKey
// @Param ns query string false "Comma separated namespaces: site, blog, forms (at most 12); absent = all"
// @Param locale query string false "Content locale (^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$, lo = la). Absent: Accept-Language, then the default locale"
// @Param Accept-Language header string false "Used when locale is absent"
// @Param If-None-Match header string false "Weak ETag of an earlier answer; a match gives 304 without a body"
// @Success 200 {object} response.Envelope{data=models.UIStringsData} "OK (ETag and Cache-Control set; Content-Language when the locale was negotiated)"
// @Success 304 "Not modified: If-None-Match matched, no body"
// @Failure 401 {object} apperror.Body "Missing or invalid X-Portal-Key"
// @Failure 422 {object} apperror.Body "Invalid parameter (errors[] names it)"
// @Failure 503 {object} apperror.Body "Database unavailable"
// @Router /api/v1/public/ui-strings [get]
func (h *siteCtrl) UIStrings(c *fiber.Ctx) error {
	ns, err := csvQuery(c, "ns", func(v string) bool { return AllowedUINamespaces[v] })
	if err != nil {
		return err
	}
	neg, _, err := readkit.Negotiate(c, h.chains)
	if err != nil {
		return err
	}
	d, err := h.svc.UIStrings(c.UserContext(), neg, ns)
	if err != nil {
		return err
	}
	return readkit.Send(c, neg, response.Envelope{Success: true, Data: d})
}

// P-04 GET /public/menus/:key?locale=
//
// @Summary Menu by key (P-04)
// @Description Items of one menu (for example main, footer, social) for the locale.
// @Tags Site
// @Accept json
// @Produce json
// @Security PortalKey
// @Param key path string true "Menu key (^[a-z0-9]+([_-][a-z0-9]+)*$, at most 64 bytes)"
// @Param locale query string false "Content locale (^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$, lo = la). Absent: Accept-Language, then the default locale"
// @Param Accept-Language header string false "Used when locale is absent"
// @Param If-None-Match header string false "Weak ETag of an earlier answer; a match gives 304 without a body"
// @Success 200 {object} response.Envelope{data=models.MenuResponse} "OK (ETag and Cache-Control set; Content-Language when the locale was negotiated)"
// @Success 304 "Not modified: If-None-Match matched, no body"
// @Failure 401 {object} apperror.Body "Missing or invalid X-Portal-Key"
// @Failure 422 {object} apperror.Body "Invalid parameter (errors[] names it)"
// @Failure 404 {object} apperror.Body "Not found (cacheable for 30 s)"
// @Failure 503 {object} apperror.Body "Database unavailable"
// @Router /api/v1/public/menus/{key} [get]
func (h *siteCtrl) Menu(c *fiber.Ctx) error {
	key := c.Params("key")
	if len(key) > 64 || !menuPattern.MatchString(key) {
		return readkit.NotFound(c) // no menu can have this key
	}
	neg, _, err := readkit.Negotiate(c, h.chains)
	if err != nil {
		return err
	}
	d, err := h.svc.Menu(c.UserContext(), neg, key)
	if errors.Is(err, services.ErrNotFound) {
		return readkit.NotFound(c)
	}
	if err != nil {
		return err
	}
	return readkit.Send(c, neg, response.Envelope{Success: true, Data: d})
}

// P-05 GET /public/site?locale=
//
// @Summary Site layout bundle (P-05)
// @Description Locale, locales, settings, the main/footer/social menus and the UI strings in one call.
// @Tags Site
// @Accept json
// @Produce json
// @Security PortalKey
// @Param locale query string false "Content locale (^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$, lo = la). Absent: Accept-Language, then the default locale"
// @Param Accept-Language header string false "Used when locale is absent"
// @Param If-None-Match header string false "Weak ETag of an earlier answer; a match gives 304 without a body"
// @Success 200 {object} response.Envelope{data=models.SiteData} "OK (ETag and Cache-Control set; Content-Language when the locale was negotiated)"
// @Success 304 "Not modified: If-None-Match matched, no body"
// @Failure 401 {object} apperror.Body "Missing or invalid X-Portal-Key"
// @Failure 422 {object} apperror.Body "Invalid parameter (errors[] names it)"
// @Failure 503 {object} apperror.Body "Database unavailable"
// @Router /api/v1/public/site [get]
func (h *siteCtrl) Site(c *fiber.Ctx) error {
	neg, _, err := readkit.Negotiate(c, h.chains)
	if err != nil {
		return err
	}
	d, err := h.svc.Site(c.UserContext(), neg)
	if err != nil {
		return err
	}
	return readkit.Send(c, neg, response.Envelope{Success: true, Data: d})
}

// csvQuery reads a comma separated query parameter and validates every item; the first bad item is a 422 on
// that parameter. An absent or empty parameter is nil (no filter).
func csvQuery(c *fiber.Ctx, name string, ok func(string) bool) ([]string, error) {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > maxListItems {
		return nil, apperror.Validation(apperror.FieldError{In: apperror.InQuery, Path: "/" + name, Code: "too_many",
			Params: map[string]any{"max": maxListItems}})
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if !ok(p) {
			return nil, apperror.Validation(apperror.FieldError{In: apperror.InQuery, Path: "/" + name, Code: "invalid_value"})
		}
		out = append(out, p)
	}
	return out, nil
}
