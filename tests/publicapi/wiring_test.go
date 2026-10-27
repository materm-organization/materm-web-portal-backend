// Package publicapi tests the public read endpoints of the site, pages and seo modules (P-01 ... P-06, P-16)
// through HTTP, with the real middleware pipeline and the real services and controllers. wire() is the wiring
// the integrator (T1.2.12) writes into cmd/application/{services,controller,router}.go, line for line.
package publicapi

import (
	"context"
	"testing"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/repositories"
	pagescontroller "github.com/materm-organization/materm-web-portal-backend/modules/pages/controller"
	pagesroutes "github.com/materm-organization/materm-web-portal-backend/modules/pages/routes"
	pagesservices "github.com/materm-organization/materm-web-portal-backend/modules/pages/services"
	seocontroller "github.com/materm-organization/materm-web-portal-backend/modules/seo/controller"
	seoroutes "github.com/materm-organization/materm-web-portal-backend/modules/seo/routes"
	seoservices "github.com/materm-organization/materm-web-portal-backend/modules/seo/services"
	sitecontroller "github.com/materm-organization/materm-web-portal-backend/modules/site/controller"
	siteroutes "github.com/materm-organization/materm-web-portal-backend/modules/site/routes"
	siteservices "github.com/materm-organization/materm-web-portal-backend/modules/site/services"
	"github.com/materm-organization/materm-web-portal-backend/pkg/assemble"
	"github.com/materm-organization/materm-web-portal-backend/pkg/localize"
	"github.com/materm-organization/materm-web-portal-backend/pkg/media"
	"github.com/materm-organization/materm-web-portal-backend/pkg/publiccache"
	"github.com/materm-organization/materm-web-portal-backend/pkg/routepolicy"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/apitest"
)

// stack is what the application would build for these modules.
type stack struct {
	App       *apitest.App
	Assembler *assemble.Assembler
	Provider  *localize.Provider
}

// wire builds the three modules over repos, a response cache (inactive when it has no Redis), the media
// builder and the base URL fallback.
func wire(t testing.TB, repos *repositories.Set, cache *publiccache.Cache, mb *media.Builder, defaultBaseURL string) *stack {
	t.Helper()

	// -- cmd/application/services.go
	// In production: use s.Locales (the one provider of Setup) instead of building another, repos.Media / s.MediaLookup,
	// and env.Site.DefaultBaseURL (SetupServices does not receive env today: pass it in or carry it on Repositories).
	locales := localize.NewProvider(func(ctx context.Context) ([]localize.Locale, error) {
		rows, err := repos.Locales.ListActive(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]localize.Locale, 0, len(rows))
		for _, l := range rows {
			fb := ""
			if l.FallbackCode != nil {
				fb = *l.FallbackCode
			}
			out = append(out, localize.Locale{Code: l.Code, FallbackCode: fb, IsDefault: l.IsDefault})
		}
		return out, nil
	}, localize.DefaultProviderTTL)
	lookup := repositories.NewMediaLookup(repos.Assets)
	assembler := assemble.New(assemble.Deps{
		Locales: repos.Locales, Pages: repos.Pages, Posts: repos.Posts, Taxonomy: repos.Taxonomy, Albums: repos.Albums,
		FAQ: repos.FAQ, Forms: repos.Forms, Settings: repos.Settings,
		Media: mb, MediaLookup: lookup, DefaultBaseURL: defaultBaseURL,
	})
	siteSvc := siteservices.NewSiteService(siteservices.Deps{
		Locales: repos.Locales, UIStrings: repos.UIStrings, Settings: repos.Settings, Menus: repos.Menus,
		Cache: cache, Media: mb, MediaLookup: lookup,
	})
	pageSvc := pagesservices.NewPageService(pagesservices.Deps{Pages: repos.Pages, Assembler: assembler, Cache: cache})
	seoSvc := seoservices.NewSEOService(seoservices.Deps{SEO: repos.SEO, Settings: repos.Settings, Cache: cache, DefaultBaseURL: defaultBaseURL})

	// -- cmd/application/controller.go
	siteCtrl := sitecontroller.NewSiteCtrl(siteSvc, locales)
	pagesCtrl := pagescontroller.NewPagesCtrl(pageSvc, locales)
	seoCtrl := seocontroller.NewSEOCtrl(seoSvc, locales)

	// -- cmd/application/router.go (on the api group returned by routepolicy.NewAPI)
	app := apitest.New(func(api *routepolicy.Group) {
		siteroutes.Register(api, siteCtrl)
		pagesroutes.Register(api, pagesCtrl)
		seoroutes.Register(api, seoCtrl)
	})
	return &stack{App: app, Assembler: assembler, Provider: locales}
}
