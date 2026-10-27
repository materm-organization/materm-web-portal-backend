// Package services is the logic of the seo module: the sitemap (P-16). Redirects (P-17) are P2 and are not
// registered; robots.txt comes from the frontend's environment (FR-SEO-04), so there is no robots endpoint.
package services

import (
	"context"
	"sort"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/repositories"
	"github.com/materm-organization/materm-web-portal-backend/modules/seo/models"
	"github.com/materm-organization/materm-web-portal-backend/pkg/assemble"
	"github.com/materm-organization/materm-web-portal-backend/pkg/publiccache"
)

// SEOService serves the sitemap.
type SEOService interface {
	// Sitemap answers P-16 for the default locale (the sitemap lists canonical URLs).
	Sitemap(ctx context.Context, defaultLocale string) (*models.SitemapData, error)
}

// Deps are the collaborators of the service.
type Deps struct {
	SEO      repositories.SEORepository
	Settings repositories.SettingRepository
	Cache    *publiccache.Cache
	// DefaultBaseURL is site.defaultBaseUrl: used when the setting site.base_url is blank.
	DefaultBaseURL string
}

type seoService struct{ d Deps }

// NewSEOService builds the service.
func NewSEOService(d Deps) SEOService { return &seoService{d: d} }

const resSitemap = "sitemap"

func (s *seoService) Sitemap(ctx context.Context, defaultLocale string) (*models.SitemapData, error) {
	v, _, err := publiccache.Get(ctx, s.d.Cache, publiccache.Spec{Resource: resSitemap, Locale: defaultLocale},
		func(ctx context.Context) (*models.SitemapData, []string, error) {
			rows, err := s.d.SEO.Sitemap(ctx, defaultLocale)
			if err != nil {
				return nil, nil, err
			}
			srows, err := s.d.Settings.List(ctx, defaultLocale, []string{"site"})
			if err != nil {
				return nil, nil, err
			}
			settings, _ := assemble.BuildSettings(srows)
			entries := make([]models.SitemapEntry, 0, len(rows))
			for _, r := range rows {
				e := models.SitemapEntry{Kind: r.Kind, EntityID: r.EntityID, Path: r.Path}
				if r.Lastmod != nil {
					e.Lastmod = assemble.ISO(*r.Lastmod)
				}
				entries = append(entries, e)
			}
			// pages first, then posts; inside a kind by id (stable and independent of the database collation)
			sort.SliceStable(entries, func(i, j int) bool {
				if entries[i].Kind != entries[j].Kind {
					return entries[i].Kind < entries[j].Kind
				}
				return entries[i].EntityID < entries[j].EntityID
			})
			return &models.SitemapData{Locale: defaultLocale, BaseURL: settings.BaseURL(s.d.DefaultBaseURL), Entries: entries},
				[]string{"sitemap", "pages", "posts:list", "settings", "locales"}, nil
		})
	return v, err
}
