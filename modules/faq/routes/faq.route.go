// Package routes registers the faq module on the portal-key guarded API group.
package routes

import (
	"github.com/materm-organization/materm-web-portal-backend/modules/content/readkit"
	"github.com/materm-organization/materm-web-portal-backend/modules/faq/controller"
	"github.com/materm-organization/materm-web-portal-backend/pkg/routepolicy"
)

// Register mounts P-13.
func Register(g *routepolicy.Group, ctrl controller.FaqCtrl) {
	g.Get("/public/faq/:group_key", routepolicy.PortalKey(), readkit.Guard(ctrl.Group))
}
