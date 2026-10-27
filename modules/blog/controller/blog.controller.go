// Package controller is the HTTP layer of the blog module: query parsing, locale negotiation and the answer.
package controller

import (
	"strconv"

	"github.com/gofiber/fiber/v2"

	"github.com/materm-organization/materm-web-portal-backend/modules/blog/models"
	"github.com/materm-organization/materm-web-portal-backend/modules/blog/services"
	"github.com/materm-organization/materm-web-portal-backend/modules/content/readkit"
	"github.com/materm-organization/materm-web-portal-backend/pkg/apperror"
	"github.com/materm-organization/materm-web-portal-backend/pkg/localize"
)

// BlogCtrl serves P-07 ... P-10.
type BlogCtrl interface {
	Posts(c *fiber.Ctx) error
	Post(c *fiber.Ctx) error
	Categories(c *fiber.Ctx) error
	Tags(c *fiber.Ctx) error
}

type blogCtrl struct {
	svc     services.BlogService
	locales *localize.Provider
}

// NewBlogCtrl builds the controller.
func NewBlogCtrl(svc services.BlogService, locales *localize.Provider) BlogCtrl {
	return &blogCtrl{svc: svc, locales: locales}
}

// Posts is P-07: GET /public/posts?category&tag&page&limit.
func (h *blogCtrl) Posts(c *fiber.Ctx) error {
	neg, _, err := readkit.Negotiate(c, h.locales)
	if err != nil {
		return err
	}
	q, err := ParsePostsQuery(c.Query)
	if err != nil {
		return err
	}
	page, err := h.svc.Posts(c.UserContext(), neg.Code, q)
	if err != nil {
		return err
	}
	return readkit.Send(c, neg, readkit.NewMediaList(page.Items, page.Meta, page.Media))
}

// Post is P-08: GET /public/posts/:slug. A slug of another locale answers 308 to the visitor's slug.
func (h *blogCtrl) Post(c *fiber.Ctx) error {
	neg, _, err := readkit.Negotiate(c, h.locales)
	if err != nil {
		return err
	}
	out, err := h.svc.Post(c.UserContext(), neg.Code, c.Params("slug"))
	if err != nil {
		return err
	}
	switch out.Kind {
	case models.KindPost:
		return readkit.Send(c, neg, readkit.OKBody(out.Post))
	case models.KindRedirect:
		return readkit.SendRedirect(c, neg, out.Location)
	}
	return apperror.NotFound()
}

// Categories is P-09.
func (h *blogCtrl) Categories(c *fiber.Ctx) error {
	neg, _, err := readkit.Negotiate(c, h.locales)
	if err != nil {
		return err
	}
	items, err := h.svc.Categories(c.UserContext(), neg.Code)
	if err != nil {
		return err
	}
	return readkit.Send(c, neg, readkit.OKBody(nonNil(items)))
}

// Tags is P-10.
func (h *blogCtrl) Tags(c *fiber.Ctx) error {
	neg, _, err := readkit.Negotiate(c, h.locales)
	if err != nil {
		return err
	}
	items, err := h.svc.Tags(c.UserContext(), neg.Code)
	if err != nil {
		return err
	}
	return readkit.Send(c, neg, readkit.OKBody(nonNil(items)))
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// ParsePostsQuery validates category, tag, page and limit. Everything is a 422 with `in:"query"` field errors
// (all of them at once); a well-formed key that names nothing is NOT an error (it gives an empty list).
func ParsePostsQuery(get func(key string, defaultValue ...string) string) (services.PostsQuery, error) {
	q := services.PostsQuery{Page: 1, Limit: services.DefaultLimit, Category: get("category"), Tag: get("tag")}
	var errs []apperror.FieldError
	bad := func(path, code string, params map[string]any) {
		errs = append(errs, apperror.FieldError{In: apperror.InQuery, Path: path, Code: code, Params: params})
	}
	if raw := get("page"); raw != "" {
		n, err := strconv.Atoi(raw)
		switch {
		case err != nil:
			bad("/page", "invalid_type", map[string]any{"expected": "integer"})
		case n < 1 || n > services.MaxPage:
			bad("/page", "out_of_range", map[string]any{"min": 1, "max": services.MaxPage})
		default:
			q.Page = n
		}
	}
	if raw := get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		switch {
		case err != nil:
			bad("/limit", "invalid_type", map[string]any{"expected": "integer"})
		case n < services.MinLimit || n > services.MaxLimit:
			bad("/limit", "out_of_range", map[string]any{"min": services.MinLimit, "max": services.MaxLimit})
		default:
			q.Limit = n
		}
	}
	if q.Category != "" && (len(q.Category) > 40 || !readkit.KeyPattern.MatchString(q.Category)) {
		bad("/category", "invalid_format", nil)
	}
	if q.Tag != "" && !readkit.TagKeyPattern.MatchString(q.Tag) {
		bad("/tag", "invalid_format", nil)
	}
	if len(errs) > 0 {
		return services.PostsQuery{}, apperror.Validation(errs...)
	}
	return q, nil
}
