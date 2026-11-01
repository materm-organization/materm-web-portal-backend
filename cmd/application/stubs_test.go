package application

import (
	"context"

	blogmodels "github.com/materm-organization/materm-web-portal-backend/modules/blog/models"
	blogservices "github.com/materm-organization/materm-web-portal-backend/modules/blog/services"
	faqmodels "github.com/materm-organization/materm-web-portal-backend/modules/faq/models"
	formsmodels "github.com/materm-organization/materm-web-portal-backend/modules/forms/models"
	formsservices "github.com/materm-organization/materm-web-portal-backend/modules/forms/services"
	gallerymodels "github.com/materm-organization/materm-web-portal-backend/modules/gallery/models"
	pagesmodels "github.com/materm-organization/materm-web-portal-backend/modules/pages/models"
	previewservices "github.com/materm-organization/materm-web-portal-backend/modules/preview/services"
	seomodels "github.com/materm-organization/materm-web-portal-backend/modules/seo/models"
	sitemodels "github.com/materm-organization/materm-web-portal-backend/modules/site/models"
	"github.com/materm-organization/materm-web-portal-backend/pkg/apperror"
	"github.com/materm-organization/materm-web-portal-backend/pkg/localize"
)

// Stub services for the tests that build Services by hand: every content method answers 404, which is all the
// route-policy tests need (they assert "401 or not", not content). The content itself is tested per module and
// in tests/publicapi + tests/readapi.

type stubSite struct{}

func (stubSite) Locales(context.Context) ([]sitemodels.LocaleInfo, error) {
	return nil, apperror.NotFound()
}
func (stubSite) Settings(context.Context, localize.Negotiation, []string) (*sitemodels.SettingsData, error) {
	return nil, apperror.NotFound()
}
func (stubSite) UIStrings(context.Context, localize.Negotiation, []string) (*sitemodels.UIStringsData, error) {
	return nil, apperror.NotFound()
}
func (stubSite) Menu(context.Context, localize.Negotiation, string) (*sitemodels.MenuResponse, error) {
	return nil, apperror.NotFound()
}
func (stubSite) Site(context.Context, localize.Negotiation) (*sitemodels.SiteData, error) {
	return nil, apperror.NotFound()
}

type stubPages struct{}

func (stubPages) Resolve(context.Context, localize.Negotiation, string) (*pagesmodels.PageData, error) {
	return nil, apperror.NotFound()
}

type stubSEO struct{}

func (stubSEO) Sitemap(context.Context, string) (*seomodels.SitemapData, error) {
	return nil, apperror.NotFound()
}

func (stubSEO) Redirect(context.Context, string, string) (*seomodels.RedirectData, error) {
	return nil, apperror.NotFound()
}

type stubBlog struct{}

func (stubBlog) Posts(context.Context, string, blogservices.PostsQuery) (*blogmodels.PostsPage, error) {
	return nil, apperror.NotFound()
}
func (stubBlog) Post(context.Context, string, string) (*blogmodels.PostOutcome, error) {
	return &blogmodels.PostOutcome{Kind: blogmodels.KindNotFound}, nil
}
func (stubBlog) Categories(context.Context, string) ([]blogmodels.CategoryInfo, error) {
	return nil, apperror.NotFound()
}
func (stubBlog) Tags(context.Context, string) ([]blogmodels.TagInfo, error) {
	return nil, apperror.NotFound()
}

type stubGallery struct{}

func (stubGallery) Albums(context.Context, string) (*gallerymodels.AlbumsPage, error) {
	return nil, apperror.NotFound()
}
func (stubGallery) Album(context.Context, string, string) (*gallerymodels.AlbumDetail, error) {
	return nil, apperror.NotFound()
}

type stubFaq struct{}

func (stubFaq) Group(context.Context, string, string) (*faqmodels.FaqGroup, error) {
	return nil, apperror.NotFound()
}

type stubForms struct{}

func (stubForms) Definition(context.Context, string, string) (*formsmodels.FormDefinition, error) {
	return nil, apperror.NotFound()
}
func (stubForms) Submit(context.Context, formsservices.SubmitInput) error { return apperror.NotFound() }

type stubPreview struct{}

func (stubPreview) Resolve(context.Context, previewservices.Input) (*previewservices.Result, error) {
	return nil, previewservices.InvalidToken()
}

// stubServices is Services with every content module stubbed; the caller fills System and Locales.
func stubServices() *Services {
	return &Services{
		Site: stubSite{}, Pages: stubPages{}, SEO: stubSEO{}, Blog: stubBlog{}, Gallery: stubGallery{}, Faq: stubFaq{}, Forms: stubForms{}, Preview: stubPreview{},
	}
}
