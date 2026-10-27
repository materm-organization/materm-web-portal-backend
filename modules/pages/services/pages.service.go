// Package services is the logic of the pages module (P-06): find the published page of a path, assemble it
// (pkg/assemble) and cache the finished response.
package services

import (
	"context"
	"errors"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/repositories"
	"github.com/materm-organization/materm-web-portal-backend/modules/pages/models"
	"github.com/materm-organization/materm-web-portal-backend/pkg/assemble"
	"github.com/materm-organization/materm-web-portal-backend/pkg/localize"
	"github.com/materm-organization/materm-web-portal-backend/pkg/publiccache"
)

// ErrNotFound: no published page has this path.
var ErrNotFound = errors.New("pages: not found")

// PageService resolves a path to a page.
type PageService interface {
	Resolve(ctx context.Context, neg localize.Negotiation, path string) (*models.PageData, error)
}

// Deps are the collaborators of the service.
type Deps struct {
	Pages     repositories.PageRepository
	Assembler *assemble.Assembler
	Cache     *publiccache.Cache
}

type pageService struct{ d Deps }

// NewPageService builds the service.
func NewPageService(d Deps) PageService { return &pageService{d: d} }

const resPage = "page"

// Resolve answers the page at path in the negotiated locale. Only found pages are cached (tags: page:<id>,
// pages, settings, locales and what the sections resolve); a 404 is not, because a page scheduled with a
// future published_at appears without any invalidation event and must not be shadowed by a stored 404.
func (s *pageService) Resolve(ctx context.Context, neg localize.Negotiation, path string) (*models.PageData, error) {
	v, _, err := publiccache.Get(ctx, s.d.Cache,
		publiccache.Spec{Resource: resPage, Locale: neg.Code, Params: map[string]string{"path": path}},
		func(ctx context.Context) (*models.PageData, []string, error) {
			page, err := s.d.Pages.FindByPath(ctx, neg.Code, path)
			if errors.Is(err, repositories.ErrNotFound) {
				return nil, nil, ErrNotFound
			}
			if err != nil {
				return nil, nil, err
			}
			secs, err := s.d.Pages.Sections(ctx, page.PageID)
			if err != nil {
				return nil, nil, err
			}
			res, err := s.d.Assembler.Page(ctx, assemble.PageInput{Locale: neg.Code, Chain: neg.Chain, Page: page, Sections: secs})
			if err != nil {
				return nil, nil, err
			}
			return res.Data, res.Tags, nil
		})
	return v, err
}
