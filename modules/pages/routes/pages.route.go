// Package routes mounts the pages module on the API group.
package routes

import (
	"github.com/materm-organization/materm-web-portal-backend/modules/content/readkit"
	"github.com/materm-organization/materm-web-portal-backend/modules/pages/controller"
	"github.com/materm-organization/materm-web-portal-backend/pkg/routepolicy"
)

// Register adds P-06 to the /api/v1 group (portal key required). The path is /public/pages/resolve, the one
// the portal-frontend loader calls (doc/08 section 2.2, openapi-portal.yaml).
func Register(api *routepolicy.Group, ctrl controller.PagesCtrl) {
	api.Get("/public/pages/resolve", routepolicy.PortalKey(), readkit.Guard(ctrl.Resolve))
}
