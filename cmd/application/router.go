package application

import (
	"github.com/gofiber/fiber/v2"

	systemroutes "github.com/materm-organization/materm-web-portal-backend/modules/system/routes"
	"github.com/materm-organization/materm-web-portal-backend/pkg/constants"
	"github.com/materm-organization/materm-web-portal-backend/pkg/portalkey"
	"github.com/materm-organization/materm-web-portal-backend/pkg/routepolicy"
)

// SetupRouter mounts every route and returns the registry of their policies:
//
//   - the probes (/health, /healthz, /ready, /readyz) on the root, Public();
//   - everything else under /api/v1 through a routepolicy group that carries the portal key guard
//     (portalkey.Verifier.Middleware), so each route is PortalKey() and a path that matches nothing answers
//     401 without the key.
//
// Content modules (PB-3 and later) register their routes on the returned api group; the group is the
// second result so that cmd/application can pass it on.
func SetupRouter(app *fiber.App, c *Controllers, keys *portalkey.Verifier) (*routepolicy.Registry, *routepolicy.Group) {
	reg := routepolicy.NewRegistry()

	root := routepolicy.NewRoot(app, reg)
	systemroutes.Register(root, c.System)

	api := routepolicy.NewAPI(app, constants.MainRoute, keys.Middleware(), reg)
	// module routes are registered here, e.g. siteroutes.Register(api, c.Site)
	return reg, api
}
