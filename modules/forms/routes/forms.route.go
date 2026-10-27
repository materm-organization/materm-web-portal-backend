// Package routes registers the forms module on the portal-key guarded API group.
package routes

import (
	"github.com/materm-organization/materm-web-portal-backend/modules/content/readkit"
	"github.com/materm-organization/materm-web-portal-backend/modules/forms/controller"
	"github.com/materm-organization/materm-web-portal-backend/pkg/routepolicy"
)

// Register mounts P-14 and P-15. Both need the portal key (every route of the API group does); the answer to
// a submission is never cacheable, success or error.
func Register(g *routepolicy.Group, ctrl controller.FormCtrl) {
	g.Get("/public/forms/:slug", routepolicy.PortalKey(), readkit.Guard(ctrl.Definition))
	g.Post("/public/forms/:slug/submissions", routepolicy.PortalKey(), readkit.NoStore(ctrl.Submit))
}
