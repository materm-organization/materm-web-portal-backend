// Package routes mounts the site module on the API group.
package routes

import (
	"github.com/materm-organization/materm-web-portal-backend/modules/site/controller"
	"github.com/materm-organization/materm-web-portal-backend/pkg/assemble/httpx"
	"github.com/materm-organization/materm-web-portal-backend/pkg/routepolicy"
)

// Register adds P-01 ... P-05 to the /api/v1 group (portal key required).
func Register(api *routepolicy.Group, ctrl controller.SiteCtrl) {
	api.Get("/public/locales", routepolicy.PortalKey(), httpx.Guard(ctrl.Locales))
	api.Get("/public/settings", routepolicy.PortalKey(), httpx.Guard(ctrl.Settings))
	api.Get("/public/ui-strings", routepolicy.PortalKey(), httpx.Guard(ctrl.UIStrings))
	api.Get("/public/menus/:key", routepolicy.PortalKey(), httpx.Guard(ctrl.Menu))
	api.Get("/public/site", routepolicy.PortalKey(), httpx.Guard(ctrl.Site))
}
