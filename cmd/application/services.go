package application

import (
	"context"
	"time"

	contentrepositories "github.com/materm-organization/materm-web-portal-backend/modules/content/repositories"
	systemservices "github.com/materm-organization/materm-web-portal-backend/modules/system/services"
	"github.com/materm-organization/materm-web-portal-backend/pkg/localize"
	"github.com/materm-organization/materm-web-portal-backend/pkg/media"
	"github.com/materm-organization/materm-web-portal-backend/pkg/publiccache"
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
}

// localesResource is the cache resource of the locale chain.
const localesResource = "locales"

// localesTTL is how long the Redis copy of the chain lives (doc/08 section 2.7).
const localesTTL = 10 * time.Minute

// SetupServices builds the services from the repositories. strict is staging/prod (a Redis outage makes
// /ready answer 503).
func SetupServices(repos *Repositories, strict bool) *Services {
	return &Services{
		Locales:     localize.NewProvider(localeFetcher(repos), localize.DefaultProviderTTL),
		Media:       repos.Media,
		MediaLookup: contentrepositories.NewMediaLookup(repos.Content.Assets),
		System: systemservices.NewSystemService(systemservices.Deps{
			PingDB:    systemservices.DBPinger(repos.DB),
			PingRedis: systemservices.RedisPinger(repos.Redis),
			Strict:    strict,
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
