// Package routes mounts the seo module on the API group.
package routes

import (
	"github.com/materm-organization/materm-web-portal-backend/modules/content/readkit"
	"github.com/materm-organization/materm-web-portal-backend/modules/seo/controller"
	"github.com/materm-organization/materm-web-portal-backend/pkg/routepolicy"
)

// Register adds P-16 to the /api/v1 group (portal key required).
func Register(api *routepolicy.Group, ctrl controller.SEOCtrl) {
	api.Get("/public/sitemap", routepolicy.PortalKey(), readkit.Guard(ctrl.Sitemap))
}
