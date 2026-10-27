// Package application wires the portal backend by hand: repositories -> services -> controllers -> router.
// Setup is the ONE function that does it, used by cmd/server and by the API tests, so a boot test proves
// the same wiring production runs. Each of repositories.go, services.go, controller.go and router.go is the
// only place where its layer is assembled (add a module there).
package application

import (
	"context"

	"github.com/gofiber/fiber/v2"

	"github.com/materm-organization/materm-web-portal-backend/bootstrap"
	"github.com/materm-organization/materm-web-portal-backend/pkg/logs"
	"github.com/materm-organization/materm-web-portal-backend/pkg/portalkey"
	"github.com/materm-organization/materm-web-portal-backend/pkg/publiccache"
	"github.com/materm-organization/materm-web-portal-backend/pkg/routepolicy"
)

// Runtime is a started server wiring.
type Runtime struct {
	Fiber    *fiber.App
	Repos    *Repositories
	Services *Services
	Registry *routepolicy.Registry
	// API is the /api/v1 group behind the portal key guard.
	API *routepolicy.Group
	// Invalidation is the cache invalidation subscriber; nil when it is not running (no Redis, cache off, or no
	// cache.invalidateKey outside dev).
	Invalidation *publiccache.Subscriber
}

// Setup connects `database.admin` (read-only role; nothing is migrated, seeded or written), builds
// repositories, services, controllers and routes on app.Fiber. It returns an error - and closes what it
// opened - when the database cannot be reached or the role is not acceptable outside dev.
func Setup(ctx context.Context, app *bootstrap.Application) (*Runtime, error) {
	repos, err := SetupRepositories(ctx, app.Env, app.Redis)
	if err != nil {
		return nil, err
	}
	invalidation, err := SetupInvalidation(ctx, app.Env, repos)
	if err != nil {
		CloseRepositories(repos)
		return nil, err
	}
	services := SetupServices(repos, app.Env)
	controllers := SetupControllers(services)
	keys := portalkey.New(app.Env.Portal.FrontendKeys)
	reg, api := SetupRouter(app.Fiber, controllers, keys)
	return &Runtime{Fiber: app.Fiber, Repos: repos, Services: services, Registry: reg, API: api, Invalidation: invalidation}, nil
}

// Close stops the invalidation subscriber (cancels its loop and a pending second delete, closes the
// subscription, waits for the goroutines) and then closes the database pool. It does not touch Redis or
// Fiber: whoever created them owns them. Safe to call more than once.
func (rt *Runtime) Close() {
	if rt.Invalidation != nil {
		if err := rt.Invalidation.Close(); err != nil {
			logs.Error("closing the invalidation subscriber failed", logs.Err(err))
		}
		rt.Invalidation = nil
	}
	CloseRepositories(rt.Repos)
}
