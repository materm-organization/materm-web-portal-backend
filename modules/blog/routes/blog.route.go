// Package routes registers the blog module on the portal-key guarded API group.
package routes

import (
	"github.com/materm-organization/materm-web-portal-backend/modules/blog/controller"
	"github.com/materm-organization/materm-web-portal-backend/modules/content/readkit"
	"github.com/materm-organization/materm-web-portal-backend/pkg/routepolicy"
)

// Register mounts P-07 ... P-10.
func Register(g *routepolicy.Group, ctrl controller.BlogCtrl) {
	g.Get("/public/posts", routepolicy.PortalKey(), readkit.Guard(ctrl.Posts))
	g.Get("/public/posts/:slug", routepolicy.PortalKey(), readkit.Guard(ctrl.Post))
	g.Get("/public/categories", routepolicy.PortalKey(), readkit.Guard(ctrl.Categories))
	g.Get("/public/tags", routepolicy.PortalKey(), readkit.Guard(ctrl.Tags))
}
