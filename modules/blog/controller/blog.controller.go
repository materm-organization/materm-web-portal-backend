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
//
// @Summary List blog posts (P-07)
// @Description Published posts, newest first, with the media map next to the list.
// @Tags Blog
// @Accept json
// @Produce json
// @Security PortalKey
// @Param category query string false "Category key (^[a-z0-9_-]+$, max 40)"
// @Param tag query string false "Tag key"
// @Param page query integer false "Page number, 1..MaxPage (default 1)"
// @Param limit query integer false "Page size (default and bounds in the blog service)"
// @Param locale query string false "Content locale (^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$, lo = la). Absent: Accept-Language, then the default locale"
// @Param Accept-Language header string false "Used when locale is absent"
// @Param If-None-Match header string false "Weak ETag of an earlier answer; a match gives 304 without a body"
// @Success 200 {object} models.PostsEnvelope "OK (ETag and Cache-Control set; Content-Language when the locale was negotiated)"
// @Success 304 "Not modified: If-None-Match matched, no body"
// @Failure 401 {object} apperror.Body "Missing or invalid X-Portal-Key"
// @Failure 422 {object} apperror.Body "Invalid parameter (errors[] names it)"
// @Failure 503 {object} apperror.Body "Database unavailable"
// @Router /api/v1/public/posts [get]
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
//
// @Summary Post by slug (P-08)
// @Description A published post. A slug of another locale answers 308 with Location: /blog/<slug> and {data:{redirect:{location,status}}}. Not-found answers are cached 60 s by the backend.
// @Tags Blog
// @Accept json
// @Produce json
// @Security PortalKey
// @Param slug path string true "Post slug in the negotiated locale"
// @Param locale query string false "Content locale (^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$, lo = la). Absent: Accept-Language, then the default locale"
// @Param Accept-Language header string false "Used when locale is absent"
// @Param If-None-Match header string false "Weak ETag of an earlier answer; a match gives 304 without a body"
// @Success 200 {object} response.Envelope{data=models.PostDetail} "OK (ETag and Cache-Control set; Content-Language when the locale was negotiated)"
// @Success 304 "Not modified: If-None-Match matched, no body"
// @Success 308 {object} response.Envelope{data=readkit.RedirectBody} "Permanent redirect to the visitor-locale slug (Location header)"
// @Failure 401 {object} apperror.Body "Missing or invalid X-Portal-Key"
// @Failure 422 {object} apperror.Body "Invalid parameter (errors[] names it)"
// @Failure 404 {object} apperror.Body "Not found (cacheable for 30 s)"
// @Failure 503 {object} apperror.Body "Database unavailable"
// @Router /api/v1/public/posts/{slug} [get]
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
//
// @Summary Post categories (P-09)
// @Description Categories that have published posts.
// @Tags Blog
// @Accept json
// @Produce json
// @Security PortalKey
// @Param locale query string false "Content locale (^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$, lo = la). Absent: Accept-Language, then the default locale"
// @Param Accept-Language header string false "Used when locale is absent"
// @Param If-None-Match header string false "Weak ETag of an earlier answer; a match gives 304 without a body"
// @Success 200 {object} response.Envelope{data=[]models.CategoryInfo} "OK (ETag and Cache-Control set; Content-Language when the locale was negotiated)"
// @Success 304 "Not modified: If-None-Match matched, no body"
// @Failure 401 {object} apperror.Body "Missing or invalid X-Portal-Key"
// @Failure 422 {object} apperror.Body "Invalid parameter (errors[] names it)"
// @Failure 503 {object} apperror.Body "Database unavailable"
// @Router /api/v1/public/categories [get]
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
//
// @Summary Post tags (P-10)
// @Description Tags that have published posts.
// @Tags Blog
// @Accept json
// @Produce json
// @Security PortalKey
// @Param locale query string false "Content locale (^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$, lo = la). Absent: Accept-Language, then the default locale"
// @Param Accept-Language header string false "Used when locale is absent"
// @Param If-None-Match header string false "Weak ETag of an earlier answer; a match gives 304 without a body"
// @Success 200 {object} response.Envelope{data=[]models.TagInfo} "OK (ETag and Cache-Control set; Content-Language when the locale was negotiated)"
// @Success 304 "Not modified: If-None-Match matched, no body"
// @Failure 401 {object} apperror.Body "Missing or invalid X-Portal-Key"
// @Failure 422 {object} apperror.Body "Invalid parameter (errors[] names it)"
// @Failure 503 {object} apperror.Body "Database unavailable"
// @Router /api/v1/public/tags [get]
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
