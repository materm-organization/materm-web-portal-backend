// Package routes mounts the site module on the API group.
package routes

import (
	"github.com/materm-organization/materm-web-portal-backend/modules/content/readkit"
	"github.com/materm-organization/materm-web-portal-backend/modules/site/controller"
	"github.com/materm-organization/materm-web-portal-backend/pkg/routepolicy"
)

// Register adds P-01 ... P-05 to the /api/v1 group (portal key required).
func Register(api *routepolicy.Group, ctrl controller.SiteCtrl) {
	api.Get("/public/locales", routepolicy.PortalKey(), readkit.Guard(ctrl.Locales))
	api.Get("/public/settings", routepolicy.PortalKey(), readkit.Guard(ctrl.Settings))
	api.Get("/public/ui-strings", routepolicy.PortalKey(), readkit.Guard(ctrl.UIStrings))
	api.Get("/public/menus/:key", routepolicy.PortalKey(), readkit.Guard(ctrl.Menu))
	api.Get("/public/site", routepolicy.PortalKey(), readkit.Guard(ctrl.Site))
}
