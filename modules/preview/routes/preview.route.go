// Package routes mounts the preview module on the portal-key guarded API group.
package routes

import (
	"github.com/materm-organization/materm-web-portal-backend/modules/preview/controller"
	"github.com/materm-organization/materm-web-portal-backend/pkg/routepolicy"
)

// Register adds P-19 at /api/v1/preview/resolve. It sits beside /public, not under it: it is not public
// content (doc/08 section 2.1). The handler is NOT wrapped in readkit.Guard: that would make a 404 cacheable.
func Register(api *routepolicy.Group, ctrl controller.PreviewCtrl) {
	api.Get("/preview/resolve", routepolicy.PortalKey(), ctrl.Resolve)
}
