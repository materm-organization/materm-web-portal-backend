// Package routes mounts the probes of the system module on the unauthenticated root.
package routes

import (
	"github.com/materm-organization/materm-web-portal-backend/modules/system/controller"
	"github.com/materm-organization/materm-web-portal-backend/pkg/routepolicy"
)

// Register adds /health, /healthz (liveness) and /ready, /readyz (readiness) to root.
func Register(root *routepolicy.Group, ctrl controller.SystemCtrl) {
	root.Get("/health", routepolicy.Public(), ctrl.Health)
	root.Get("/healthz", routepolicy.Public(), ctrl.Health)
	root.Get("/ready", routepolicy.Public(), ctrl.Ready)
	root.Get("/readyz", routepolicy.Public(), ctrl.Ready)
}
