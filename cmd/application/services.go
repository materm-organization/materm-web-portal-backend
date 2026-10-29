package application

import (
	"context"
	"time"

	"github.com/materm-organization/materm-web-portal-backend/bootstrap"
	blogservices "github.com/materm-organization/materm-web-portal-backend/modules/blog/services"
	contentrepositories "github.com/materm-organization/materm-web-portal-backend/modules/content/repositories"
	faqservices "github.com/materm-organization/materm-web-portal-backend/modules/faq/services"
	formsservices "github.com/materm-organization/materm-web-portal-backend/modules/forms/services"
	galleryservices "github.com/materm-organization/materm-web-portal-backend/modules/gallery/services"
	pagesservices "github.com/materm-organization/materm-web-portal-backend/modules/pages/services"
	previewservices "github.com/materm-organization/materm-web-portal-backend/modules/preview/services"
	seoservices "github.com/materm-organization/materm-web-portal-backend/modules/seo/services"
	siteservices "github.com/materm-organization/materm-web-portal-backend/modules/site/services"
	systemservices "github.com/materm-organization/materm-web-portal-backend/modules/system/services"
	"github.com/materm-organization/materm-web-portal-backend/pkg/assemble"
	"github.com/materm-organization/materm-web-portal-backend/pkg/iphash"
	"github.com/materm-organization/materm-web-portal-backend/pkg/localize"
	"github.com/materm-organization/materm-web-portal-backend/pkg/media"
	"github.com/materm-organization/materm-web-portal-backend/pkg/publiccache"
	"github.com/materm-organization/materm-web-portal-backend/pkg/ratelimit"
)

// Services holds the services of every module.
type Services struct {
	System systemservices.SystemService
	// Locales hands out the active-locale Chain (process copy 60 s, Redis copy 10 min, tag "locales").
	Locales *localize.Provider
	// Media builds public media URLs (file.path_ip, media.staticPrefix) and loads data.media maps.
	Media *media.Builder
	// MediaLookup loads assets for Media.Resolve.
	MediaLookup media.Lookup
	// Assembler turns a page's sections into the P-06 payload (pkg/assemble); the pages service uses it.
	Assembler *assemble.Assembler

	Site    siteservices.SiteService       // P-01 ... P-05
	Pages   pagesservices.PageService      // P-06
	SEO     seoservices.SEOService         // P-16
	Blog    blogservices.BlogService       // P-07 ... P-10
	Gallery galleryservices.GalleryService // P-11, P-12
	Faq     faqservices.FaqService         // P-13
	Forms   formsservices.FormService      // P-14, P-15
	Preview previewservices.PreviewService // P-19
}

// localesResource is the cache resource of the locale chain.
const localesResource = "locales"

// localesTTL is how long the Redis copy of the chain lives (doc/08 section 2.7).
const localesTTL = 10 * time.Minute

// SetupServices builds the services from the repositories and the configuration (site.defaultBaseUrl and the
// forms.* limits come from env; env.Strict() is staging/prod, where a Redis outage makes /ready degraded and a
// submission refuse with 503).
func SetupServices(repos *Repositories, env *bootstrap.Env) *Services {
	content, cache, rc, keys := repos.Content, repos.PublicCache, repos.RC, repos.Keys
	baseURL := env.Site.DefaultBaseURL

	locales := localize.NewProvider(localeFetcher(repos), localize.DefaultProviderTTL)
	lookup := contentrepositories.NewMediaLookup(content.Assets)
	assembler := assemble.New(assemble.Deps{
		Locales: content.Locales, Pages: content.Pages, Posts: content.Posts, Taxonomy: content.Taxonomy,
		Albums: content.Albums, FAQ: content.FAQ, Forms: content.Forms, Settings: content.Settings,
		Shared: content.Pages,
		Media:  repos.Media, MediaLookup: lookup, DefaultBaseURL: baseURL,
	})

	blogDeps := blogservices.Deps{
		Content: content, Media: repos.Media, Lookup: lookup, Cache: cache, Locales: locales, DefaultBaseURL: baseURL,
	}

	return &Services{
		Locales:     locales,
		Media:       repos.Media,
		MediaLookup: lookup,
		Assembler:   assembler,
		System: systemservices.NewSystemService(systemservices.Deps{
			PingDB:    systemservices.DBPinger(repos.DB),
			PingRedis: systemservices.RedisPinger(repos.Redis),
			Strict:    env.Strict(),
		}),
		Site: siteservices.NewSiteService(siteservices.Deps{
			Locales: content.Locales, UIStrings: content.UIStrings, Settings: content.Settings, Menus: content.Menus,
			Cache: cache, Media: repos.Media, MediaLookup: lookup,
		}),
		Pages: pagesservices.NewPageService(pagesservices.Deps{Pages: content.Pages, Assembler: assembler, Cache: cache}),
		SEO: seoservices.NewSEOService(seoservices.Deps{
			SEO: content.SEO, Settings: content.Settings, Cache: cache, DefaultBaseURL: baseURL,
		}),
		Blog: blogservices.NewBlogService(blogDeps),
		Gallery: galleryservices.NewGalleryService(galleryservices.Deps{
			Content: content, Media: repos.Media, Lookup: lookup, Cache: cache,
		}),
		Faq: faqservices.NewFaqService(faqservices.Deps{Content: content, Cache: cache}),
		Preview: previewservices.NewPreviewService(previewservices.Deps{
			Tokens: repos.PreviewTokens, Assembler: assembler, Chains: locales, Redis: rc,
			// posts (PT-18): the same page builder as P-08, fed from the snapshot
			Posts:   previewservices.NewPostRenderer(previewservices.PostSources{Set: content, Composer: blogservices.NewComposer(blogDeps)}),
			Limiter: ratelimit.NewRedis(repos.Redis), Keys: keys,
			Options: previewservices.Options{
				CacheTTL: env.Preview.CacheTTL, TokenPerMinute: env.Preview.TokenPerMinute, KeyPerMinute: env.Preview.KeyPerMinute,
			},
		}),
		Forms: formsservices.NewFormService(formsservices.Deps{
			Forms: content.Forms, Submitter: repos.Submissions, Cache: cache,
			Hasher: iphash.New(rc, keys), Limiter: ratelimit.NewRedis(repos.Redis), Redis: rc, Keys: keys,
			Options: formsservices.Options{
				DailyLimit: env.Forms.DailyLimit, MinFillSeconds: env.Forms.MinFillSeconds, MaxClockSkew: env.Forms.MaxClockSkew,
				KeyPerMinute: env.Forms.KeyPerMinute, KeyPerDay: env.Forms.KeyPerDay, DedupeTTL: env.Forms.DedupeTTL,
			},
		}),
	}
}

// localeFetcher loads the active locales through the response cache (tag "locales": a locale change bumps
// the generation, so the chain is reloaded at once on every instance after the process copy expires).
func localeFetcher(repos *Repositories) localize.FetchFunc {
	return func(ctx context.Context) ([]localize.Locale, error) {
		rows, _, err := publiccache.Get(ctx, repos.PublicCache,
			publiccache.Spec{Resource: localesResource, Locale: "-", TTL: localesTTL},
			func(ctx context.Context) ([]localize.Locale, []string, error) {
				ls, lerr := repos.Content.Locales.ListActive(ctx)
				if lerr != nil {
					return nil, nil, lerr
				}
				out := make([]localize.Locale, 0, len(ls))
				for _, l := range ls {
					fb := ""
					if l.FallbackCode != nil {
						fb = *l.FallbackCode
					}
					out = append(out, localize.Locale{Code: l.Code, FallbackCode: fb, IsDefault: l.IsDefault})
				}
				return out, []string{"locales"}, nil
			})
		return rows, err
	}
}
