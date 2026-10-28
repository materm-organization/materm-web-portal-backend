package application

import (
	"github.com/gofiber/fiber/v2"

	blogroutes "github.com/materm-organization/materm-web-portal-backend/modules/blog/routes"
	faqroutes "github.com/materm-organization/materm-web-portal-backend/modules/faq/routes"
	formsroutes "github.com/materm-organization/materm-web-portal-backend/modules/forms/routes"
	galleryroutes "github.com/materm-organization/materm-web-portal-backend/modules/gallery/routes"
	pagesroutes "github.com/materm-organization/materm-web-portal-backend/modules/pages/routes"
	previewroutes "github.com/materm-organization/materm-web-portal-backend/modules/preview/routes"
	seoroutes "github.com/materm-organization/materm-web-portal-backend/modules/seo/routes"
	siteroutes "github.com/materm-organization/materm-web-portal-backend/modules/site/routes"
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
// Every content module registers on the api group below (P-19 preview is the last one); the
// group is the second result so that tests can add routes of their own.
func SetupRouter(app *fiber.App, c *Controllers, keys *portalkey.Verifier) (*routepolicy.Registry, *routepolicy.Group) {
	reg := routepolicy.NewRegistry()

	root := routepolicy.NewRoot(app, reg)
	systemroutes.Register(root, c.System)

	api := routepolicy.NewAPI(app, constants.MainRoute, keys.Middleware(), reg)
	siteroutes.Register(api, c.Site)       // P-01 ... P-05
	pagesroutes.Register(api, c.Pages)     // P-06
	seoroutes.Register(api, c.SEO)         // P-16
	blogroutes.Register(api, c.Blog)       // P-07 ... P-10
	galleryroutes.Register(api, c.Gallery) // P-11, P-12
	faqroutes.Register(api, c.Faq)         // P-13
	formsroutes.Register(api, c.Forms)     // P-14, P-15
	previewroutes.Register(api, c.Preview) // P-19 (under /preview, not /public)
	return reg, api
}
