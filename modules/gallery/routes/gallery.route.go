// Package routes registers the gallery module on the portal-key guarded API group.
package routes

import (
	"github.com/materm-organization/materm-web-portal-backend/modules/content/readkit"
	"github.com/materm-organization/materm-web-portal-backend/modules/gallery/controller"
	"github.com/materm-organization/materm-web-portal-backend/pkg/routepolicy"
)

// Register mounts P-11 and P-12.
func Register(g *routepolicy.Group, ctrl controller.GalleryCtrl) {
	g.Get("/public/albums", routepolicy.PortalKey(), readkit.Guard(ctrl.Albums))
	g.Get("/public/albums/:key", routepolicy.PortalKey(), readkit.Guard(ctrl.Album))
}
