package repositories

import (
	"context"

	"gorm.io/gorm"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/entities"
)

// ---- site -------------------------------------------------------------------------------------------------------

// LocaleRepository reads v_public_locales.
type LocaleRepository interface {
	// ListActive returns the active locales ordered by sort_order, code.
	ListActive(ctx context.Context) ([]entities.PublicLocale, error)
}

// UIStringRepository reads v_public_ui_strings.
type UIStringRepository interface {
	// List returns the resolved overrides of a locale, optionally limited to the given `namespace` values.
	List(ctx context.Context, locale string, namespaces []string) ([]entities.PublicUIString, error)
}

// SettingRepository reads v_public_site_settings.
type SettingRepository interface {
	// List returns the public settings of a locale, optionally limited to groups.
	List(ctx context.Context, locale string, groups []string) ([]entities.PublicSiteSetting, error)
}

// MenuRepository reads v_published_menu_items.
type MenuRepository interface {
	// Items returns the visible items of one menu ordered by parent, sort_order, id.
	Items(ctx context.Context, locale, menuKey string) ([]entities.PublishedMenuItem, error)
}

// ---- pages --------------------------------------------------------------------------------------------------------

// PageRepository reads v_published_pages and v_published_page_sections.
type PageRepository interface {
	FindByPath(ctx context.Context, locale, path string) (*entities.PublishedPage, error)
	FindByKey(ctx context.Context, locale, key string) (*entities.PublishedPage, error)
	// ByIDs resolves page ids to published pages (link resolution); ids that are not published are absent.
	ByIDs(ctx context.Context, locale string, ids []int64) ([]entities.PublishedPage, error)
	// Sections returns the visible sections of a published page ordered by sort_order.
	Sections(ctx context.Context, pageID int64) ([]entities.PublishedPageSection, error)
	// SharedSections returns, for each given shared section id that is published, one row of
	// v_published_page_sections that uses it (the view resolves the shared content into props). It is how a
	// preview learns the content of a shared section, which the portal role cannot read directly. Ids that no
	// published page uses are absent.
	SharedSections(ctx context.Context, ids []int64) ([]entities.PublishedPageSection, error)
}

// ---- taxonomy ----------------------------------------------------------------------------------------------------

// TaxonomyRepository reads v_published_categories and v_published_tags.
type TaxonomyRepository interface {
	Categories(ctx context.Context, locale string) ([]entities.PublishedCategory, error)
	CategoryByKey(ctx context.Context, locale, key string) (*entities.PublishedCategory, error)
	Tags(ctx context.Context, locale string) ([]entities.PublishedTag, error)
}

// ---- posts -------------------------------------------------------------------------------------------------------

// PostFilter narrows a post list. CategoryID is the id (convert the public category key first with
// TaxonomyRepository.CategoryByKey: the index is on the id); TagKey is a tag key.
type PostFilter struct {
	Locale     string
	CategoryID *int64
	TagKey     string
}

// PostRepository reads v_published_posts, v_published_post_slugs, v_published_post_media and
// v_published_post_tags.
type PostRepository interface {
	// List returns one page of cards (no body), newest first (published_at desc, post_id desc), and the total
	// number of posts matching the filter.
	List(ctx context.Context, f PostFilter, limit, offset int) ([]entities.PublishedPostCard, int64, error)
	// ByIDs returns the cards of published posts (blog_strip manual); absent ids are skipped.
	ByIDs(ctx context.Context, locale string, ids []int64) ([]entities.PublishedPostCard, error)
	// FindBySlug is step (1) of doc/08 section 2.3.3: the post the viewer's locale sees under this slug (a real
	// translation wins over a fallback row).
	FindBySlug(ctx context.Context, locale, slug string) (*entities.PublishedPost, error)
	// FindByID is step (3): the post in the viewer's locale.
	FindByID(ctx context.Context, locale string, postID int64) (*entities.PublishedPost, error)
	// FindSlugOwner is step (2): which post owns this slug in some locale (default locale first, then locale
	// code, then post id). ErrNotFound when no live post has it.
	FindSlugOwner(ctx context.Context, slug, defaultLocale string) (*entities.PublishedPostSlug, error)
	Media(ctx context.Context, locale string, postID int64) ([]entities.PublishedPostMedia, error)
	Tags(ctx context.Context, locale string, postIDs []int64) ([]entities.PublishedPostTag, error)
}

// ---- gallery / faq / forms / collections -------------------------------------------------------------------------

// AlbumRepository reads v_published_albums and v_published_album_items.
type AlbumRepository interface {
	List(ctx context.Context, locale string) ([]entities.PublishedAlbum, error)
	FindByKey(ctx context.Context, locale, key string) (*entities.PublishedAlbum, error)
	// Items returns the items of an album ordered by sort_order, id; limit <= 0 means all.
	Items(ctx context.Context, locale string, albumID int64, limit int) ([]entities.PublishedAlbumItem, error)
}

// FAQRepository reads v_published_faq_items.
type FAQRepository interface {
	// Items returns the items of a published group ordered by sort_order, id (empty when unpublished).
	Items(ctx context.Context, locale, groupKey string) ([]entities.PublishedFAQItem, error)
}

// FormRepository reads v_published_forms and v_published_form_fields. (The write, forms_submit_form, belongs
// to the forms module.)
type FormRepository interface {
	Find(ctx context.Context, locale, slug string) (*entities.PublishedForm, error)
	Fields(ctx context.Context, locale, slug string) ([]entities.PublishedFormField, error)
}

// CollectionRepository reads v_published_collection_entries (P2).
type CollectionRepository interface {
	Entries(ctx context.Context, locale, collectionKey string, limit, offset int) ([]entities.PublishedCollectionEntry, int64, error)
}

// ---- seo ------------------------------------------------------------------------------------------------------------

// SEORepository reads v_sitemap and v_active_redirects.
type SEORepository interface {
	Sitemap(ctx context.Context, locale string) ([]entities.SitemapEntry, error)
	// Redirect finds the active redirect for a source path; a redirect of the given locale wins over one for
	// all locales. ErrNotFound when none.
	Redirect(ctx context.Context, path, locale string) (*entities.ActiveRedirect, error)
}

// ---- media ------------------------------------------------------------------------------------------------------------

// AssetRepository reads v_public_assets.
type AssetRepository interface {
	ByIDs(ctx context.Context, locale string, ids []int64) ([]entities.PublicAsset, error)
}

// Set is every repository over one *gorm.DB (the read-only pool).
type Set struct {
	Locales     LocaleRepository
	UIStrings   UIStringRepository
	Settings    SettingRepository
	Menus       MenuRepository
	Pages       PageRepository
	Taxonomy    TaxonomyRepository
	Posts       PostRepository
	Albums      AlbumRepository
	FAQ         FAQRepository
	Forms       FormRepository
	Collections CollectionRepository
	SEO         SEORepository
	Assets      AssetRepository
}

// New builds the Set. db must be the connection of the read-only role.
func New(db *gorm.DB) *Set {
	return &Set{
		Locales: &localeRepo{db}, UIStrings: &uiStringRepo{db}, Settings: &settingRepo{db}, Menus: &menuRepo{db},
		Pages: &pageRepo{db}, Taxonomy: &taxonomyRepo{db}, Posts: &postRepo{db}, Albums: &albumRepo{db},
		FAQ: &faqRepo{db}, Forms: &formRepo{db}, Collections: &collectionRepo{db}, SEO: &seoRepo{db}, Assets: &assetRepo{db},
	}
}
