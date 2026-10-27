// Package controller holds the HTTP handlers of the site module.
package controller

import (
	"errors"
	"regexp"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/materm-organization/materm-web-portal-backend/modules/site/services"
	"github.com/materm-organization/materm-web-portal-backend/pkg/apperror"
	"github.com/materm-organization/materm-web-portal-backend/pkg/assemble/httpx"
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
	chains httpx.ChainSource
}

// NewSiteCtrl builds the controller. chains is the locale provider (localize.Provider).
func NewSiteCtrl(svc services.SiteService, chains httpx.ChainSource) SiteCtrl {
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
func (h *siteCtrl) Locales(c *fiber.Ctx) error {
	d, err := h.svc.Locales(c.UserContext())
	if err != nil {
		return err
	}
	return httpx.Send(c, nil, response.Envelope{Success: true, Data: d})
}

// P-02 GET /public/settings?locale=&group=brand,footer
func (h *siteCtrl) Settings(c *fiber.Ctx) error {
	groups, err := csvQuery(c, "group", func(v string) bool { return groupPattern.MatchString(v) })
	if err != nil {
		return err
	}
	neg, err := httpx.Negotiate(c, h.chains)
	if err != nil {
		return err
	}
	d, err := h.svc.Settings(c.UserContext(), neg, groups)
	if err != nil {
		return err
	}
	return httpx.Send(c, &neg, response.Envelope{Success: true, Data: d})
}

// P-03 GET /public/ui-strings?locale=&ns=site,blog
func (h *siteCtrl) UIStrings(c *fiber.Ctx) error {
	ns, err := csvQuery(c, "ns", func(v string) bool { return AllowedUINamespaces[v] })
	if err != nil {
		return err
	}
	neg, err := httpx.Negotiate(c, h.chains)
	if err != nil {
		return err
	}
	d, err := h.svc.UIStrings(c.UserContext(), neg, ns)
	if err != nil {
		return err
	}
	return httpx.Send(c, &neg, response.Envelope{Success: true, Data: d})
}

// P-04 GET /public/menus/:key?locale=
func (h *siteCtrl) Menu(c *fiber.Ctx) error {
	key := c.Params("key")
	if len(key) > 64 || !menuPattern.MatchString(key) {
		return httpx.NotFound(c) // no menu can have this key
	}
	neg, err := httpx.Negotiate(c, h.chains)
	if err != nil {
		return err
	}
	d, err := h.svc.Menu(c.UserContext(), neg, key)
	if errors.Is(err, services.ErrNotFound) {
		return httpx.NotFound(c)
	}
	if err != nil {
		return err
	}
	return httpx.Send(c, &neg, response.Envelope{Success: true, Data: d})
}

// P-05 GET /public/site?locale=
func (h *siteCtrl) Site(c *fiber.Ctx) error {
	neg, err := httpx.Negotiate(c, h.chains)
	if err != nil {
		return err
	}
	d, err := h.svc.Site(c.UserContext(), neg)
	if err != nil {
		return err
	}
	return httpx.Send(c, &neg, response.Envelope{Success: true, Data: d})
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
