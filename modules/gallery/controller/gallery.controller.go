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
