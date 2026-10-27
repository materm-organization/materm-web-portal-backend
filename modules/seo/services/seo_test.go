package services

import (
	"context"
	"testing"
	"time"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/entities"
	"github.com/materm-organization/materm-web-portal-backend/modules/content/repositories"
	"github.com/materm-organization/materm-web-portal-backend/pkg/publiccache"
)

type fakeSEO struct{ rows []entities.SitemapEntry }

func (f fakeSEO) Sitemap(context.Context, string) ([]entities.SitemapEntry, error) {
	return f.rows, nil
}
func (fakeSEO) Redirect(context.Context, string, string) (*entities.ActiveRedirect, error) {
	return nil, repositories.ErrNotFound
}

type fakeSettings struct{ rows []entities.PublicSiteSetting }

func (f fakeSettings) List(context.Context, string, []string) ([]entities.PublicSiteSetting, error) {
	return f.rows, nil
}

func TestSitemapOrderBaseURLAndEmptyList(t *testing.T) {
	lm := time.Date(2026, 3, 1, 10, 0, 0, 0, time.FixedZone("x", 7*3600))
	svc := NewSEOService(Deps{
		SEO: fakeSEO{rows: []entities.SitemapEntry{
			{Kind: "post", EntityID: 2, Path: "/blog/b", Lastmod: &lm},
			{Kind: "page", EntityID: 9, Path: "/z", Lastmod: &lm},
			{Kind: "post", EntityID: 1, Path: "/blog/a"},
			{Kind: "page", EntityID: 3, Path: "/a", Lastmod: &lm},
		}},
		Settings:       fakeSettings{},
		Cache:          publiccache.New(publiccache.Options{}),
		DefaultBaseURL: "https://fallback.example/",
	})
	d, err := svc.Sitemap(context.Background(), "la")
	if err != nil {
		t.Fatal(err)
	}
	var order []int64
	for _, e := range d.Entries {
		order = append(order, e.EntityID)
	}
	if len(order) != 4 || order[0] != 3 || order[1] != 9 || order[2] != 1 || order[3] != 2 {
		t.Errorf("pages first, then posts, each by id: %v", order)
	}
	if d.BaseURL != "https://fallback.example" || d.Locale != "la" {
		t.Errorf("head: %+v", d)
	}
	if d.Entries[0].Lastmod != "2026-03-01T03:00:00Z" || d.Entries[2].Lastmod != "" {
		t.Errorf("lastmod: %q %q", d.Entries[0].Lastmod, d.Entries[2].Lastmod)
	}

	// the setting wins over the configured fallback; no rows is [] and not null
	empty := NewSEOService(Deps{
		SEO:      fakeSEO{},
		Settings: fakeSettings{rows: []entities.PublicSiteSetting{{GroupKey: "site", Key: "base_url", ValueType: "url", Value: entities.JSONB(`"https://materm.example/"`)}}},
		Cache:    publiccache.New(publiccache.Options{}), DefaultBaseURL: "https://fallback.example",
	})
	d, _ = empty.Sitemap(context.Background(), "la")
	if d.BaseURL != "https://materm.example" || d.Entries == nil || len(d.Entries) != 0 {
		t.Errorf("empty sitemap: %+v", d)
	}
}
