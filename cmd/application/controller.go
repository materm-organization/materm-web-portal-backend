package application

import (
	blogcontroller "github.com/materm-organization/materm-web-portal-backend/modules/blog/controller"
	faqcontroller "github.com/materm-organization/materm-web-portal-backend/modules/faq/controller"
	formscontroller "github.com/materm-organization/materm-web-portal-backend/modules/forms/controller"
	gallerycontroller "github.com/materm-organization/materm-web-portal-backend/modules/gallery/controller"
	pagescontroller "github.com/materm-organization/materm-web-portal-backend/modules/pages/controller"
	previewcontroller "github.com/materm-organization/materm-web-portal-backend/modules/preview/controller"
	seocontroller "github.com/materm-organization/materm-web-portal-backend/modules/seo/controller"
	sitecontroller "github.com/materm-organization/materm-web-portal-backend/modules/site/controller"
	systemcontroller "github.com/materm-organization/materm-web-portal-backend/modules/system/controller"
)

// Controllers holds the HTTP controllers of every module.
type Controllers struct {
	System  systemcontroller.SystemCtrl
	Site    sitecontroller.SiteCtrl
	Pages   pagescontroller.PagesCtrl
	SEO     seocontroller.SEOCtrl
	Blog    blogcontroller.BlogCtrl
	Gallery gallerycontroller.GalleryCtrl
	Faq     faqcontroller.FaqCtrl
	Forms   formscontroller.FormCtrl
	Preview previewcontroller.PreviewCtrl
}

// SetupControllers builds the controllers from the services. Every content controller negotiates the locale
// through the ONE locale provider of the services (s.Locales).
func SetupControllers(s *Services) *Controllers {
	return &Controllers{
		System:  systemcontroller.NewSystemCtrl(s.System),
		Site:    sitecontroller.NewSiteCtrl(s.Site, s.Locales),
		Pages:   pagescontroller.NewPagesCtrl(s.Pages, s.Locales),
		SEO:     seocontroller.NewSEOCtrl(s.SEO, s.Locales),
		Blog:    blogcontroller.NewBlogCtrl(s.Blog, s.Locales),
		Gallery: gallerycontroller.NewGalleryCtrl(s.Gallery, s.Locales),
		Faq:     faqcontroller.NewFaqCtrl(s.Faq, s.Locales),
		Forms:   formscontroller.NewFormCtrl(s.Forms, s.Locales),
		Preview: previewcontroller.NewPreviewCtrl(s.Preview),
	}
}
