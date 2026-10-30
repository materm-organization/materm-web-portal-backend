// Package controller is the HTTP layer of the gallery module.
package controller

import (
	"github.com/gofiber/fiber/v2"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/readkit"
	"github.com/materm-organization/materm-web-portal-backend/modules/gallery/services"
	"github.com/materm-organization/materm-web-portal-backend/pkg/localize"
)

// GalleryCtrl serves P-11 and P-12.
type GalleryCtrl interface {
	Albums(c *fiber.Ctx) error
	Album(c *fiber.Ctx) error
}

type galleryCtrl struct {
	svc     services.GalleryService
	locales *localize.Provider
}

// NewGalleryCtrl builds the controller.
func NewGalleryCtrl(svc services.GalleryService, locales *localize.Provider) GalleryCtrl {
	return &galleryCtrl{svc: svc, locales: locales}
}

// Albums is P-11: GET /public/albums.
//
// @Summary List albums (P-11)
// @Description Published albums with their cover, with the media map next to the list.
// @Tags Gallery
// @Accept json
// @Produce json
// @Security PortalKey
// @Param locale query string false "Content locale (^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$, lo = la). Absent: Accept-Language, then the default locale"
// @Param Accept-Language header string false "Used when locale is absent"
// @Param If-None-Match header string false "Weak ETag of an earlier answer; a match gives 304 without a body"
// @Success 200 {object} models.AlbumsEnvelope "OK (ETag and Cache-Control set; Content-Language when the locale was negotiated)"
// @Success 304 "Not modified: If-None-Match matched, no body"
// @Failure 401 {object} apperror.Body "Missing or invalid X-Portal-Key"
// @Failure 422 {object} apperror.Body "Invalid parameter (errors[] names it)"
// @Failure 503 {object} apperror.Body "Database unavailable"
// @Router /api/v1/public/albums [get]
func (h *galleryCtrl) Albums(c *fiber.Ctx) error {
	neg, _, err := readkit.Negotiate(c, h.locales)
	if err != nil {
		return err
	}
	page, err := h.svc.Albums(c.UserContext(), neg.Code)
	if err != nil {
		return err
	}
	return readkit.Send(c, neg, readkit.NewMediaList(page.Items, page.Meta, page.Media))
}

// Album is P-12: GET /public/albums/:key.
//
// @Summary Album by key (P-12)
// @Description A published album with its pictures and media map.
// @Tags Gallery
// @Accept json
// @Produce json
// @Security PortalKey
// @Param key path string true "Album key"
// @Param locale query string false "Content locale (^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$, lo = la). Absent: Accept-Language, then the default locale"
// @Param Accept-Language header string false "Used when locale is absent"
// @Param If-None-Match header string false "Weak ETag of an earlier answer; a match gives 304 without a body"
// @Success 200 {object} response.Envelope{data=models.AlbumDetail} "OK (ETag and Cache-Control set; Content-Language when the locale was negotiated)"
// @Success 304 "Not modified: If-None-Match matched, no body"
// @Failure 401 {object} apperror.Body "Missing or invalid X-Portal-Key"
// @Failure 422 {object} apperror.Body "Invalid parameter (errors[] names it)"
// @Failure 404 {object} apperror.Body "Not found (cacheable for 30 s)"
// @Failure 503 {object} apperror.Body "Database unavailable"
// @Router /api/v1/public/albums/{key} [get]
func (h *galleryCtrl) Album(c *fiber.Ctx) error {
	neg, _, err := readkit.Negotiate(c, h.locales)
	if err != nil {
		return err
	}
	album, err := h.svc.Album(c.UserContext(), neg.Code, c.Params("key"))
	if err != nil {
		return err
	}
	return readkit.Send(c, neg, readkit.OKBody(album))
}
