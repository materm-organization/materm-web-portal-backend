package repositories

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/entities"
)

type localeRepo struct{ db *gorm.DB }

func (r *localeRepo) ListActive(ctx context.Context) ([]entities.PublicLocale, error) {
	return list[entities.PublicLocale](sel[entities.PublicLocale](r.db.WithContext(ctx)).Order("sort_order, code"))
}

type uiStringRepo struct{ db *gorm.DB }

func (r *uiStringRepo) List(ctx context.Context, locale string, namespaces []string) ([]entities.PublicUIString, error) {
	q := sel[entities.PublicUIString](r.db.WithContext(ctx)).Where("requested_locale = ?", locale)
	if len(namespaces) > 0 {
		q = q.Where("namespace IN ?", namespaces)
	}
	return list[entities.PublicUIString](q.Order("key"))
}

type settingRepo struct{ db *gorm.DB }

func (r *settingRepo) List(ctx context.Context, locale string, groups []string) ([]entities.PublicSiteSetting, error) {
	q := sel[entities.PublicSiteSetting](r.db.WithContext(ctx)).Where("requested_locale = ?", locale)
	if len(groups) > 0 {
		q = q.Where("group_key IN ?", groups)
	}
	return list[entities.PublicSiteSetting](q.Order("group_key, key"))
}

type menuRepo struct{ db *gorm.DB }

func (r *menuRepo) Items(ctx context.Context, locale, menuKey string) ([]entities.PublishedMenuItem, error) {
	return list[entities.PublishedMenuItem](sel[entities.PublishedMenuItem](r.db.WithContext(ctx)).
		Where("requested_locale = ? AND menu_key = ?", locale, menuKey).
		Order("parent_id NULLS FIRST, sort_order, item_id"))
}

type pageRepo struct{ db *gorm.DB }

func (r *pageRepo) FindByPath(ctx context.Context, locale, path string) (*entities.PublishedPage, error) {
	return first[entities.PublishedPage](sel[entities.PublishedPage](r.db.WithContext(ctx)).
		Where("requested_locale = ? AND path = ?", locale, path))
}

func (r *pageRepo) FindByKey(ctx context.Context, locale, key string) (*entities.PublishedPage, error) {
	return first[entities.PublishedPage](sel[entities.PublishedPage](r.db.WithContext(ctx)).
		Where("requested_locale = ? AND key = ?", locale, key))
}

func (r *pageRepo) ByIDs(ctx context.Context, locale string, ids []int64) ([]entities.PublishedPage, error) {
	if len(ids) == 0 {
		return []entities.PublishedPage{}, nil
	}
	if err := checkIDs(ids); err != nil {
		return nil, err
	}
	return list[entities.PublishedPage](sel[entities.PublishedPage](r.db.WithContext(ctx)).
		Where("requested_locale = ? AND page_id IN ?", locale, ids).Order("page_id"))
}

func (r *pageRepo) Sections(ctx context.Context, pageID int64) ([]entities.PublishedPageSection, error) {
	return list[entities.PublishedPageSection](sel[entities.PublishedPageSection](r.db.WithContext(ctx)).
		Where("page_id = ?", pageID).Order("sort_order, section_id"))
}

func (r *pageRepo) SharedSections(ctx context.Context, ids []int64) ([]entities.PublishedPageSection, error) {
	if len(ids) == 0 {
		return []entities.PublishedPageSection{}, nil
	}
	if err := checkIDs(ids); err != nil {
		return nil, err
	}
	return list[entities.PublishedPageSection](sel[entities.PublishedPageSection](r.db.WithContext(ctx)).
		Where("shared_section_id IN ?", ids).Order("shared_section_id, page_id, section_id"))
}

type taxonomyRepo struct{ db *gorm.DB }

func (r *taxonomyRepo) Categories(ctx context.Context, locale string) ([]entities.PublishedCategory, error) {
	return list[entities.PublishedCategory](sel[entities.PublishedCategory](r.db.WithContext(ctx)).
		Where("requested_locale = ?", locale).Order("sort_order, category_id"))
}

func (r *taxonomyRepo) CategoryByKey(ctx context.Context, locale, key string) (*entities.PublishedCategory, error) {
	return first[entities.PublishedCategory](sel[entities.PublishedCategory](r.db.WithContext(ctx)).
		Where("requested_locale = ? AND key = ?", locale, key))
}

func (r *taxonomyRepo) Tags(ctx context.Context, locale string) ([]entities.PublishedTag, error) {
	return list[entities.PublishedTag](sel[entities.PublishedTag](r.db.WithContext(ctx)).
		Where("requested_locale = ?", locale).Order("name, tag_id"))
}

type postRepo struct{ db *gorm.DB }

func (r *postRepo) filtered(ctx context.Context, f PostFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Table("v_published_posts").Where("requested_locale = ?", f.Locale)
	if f.CategoryID != nil {
		q = q.Where("category_id = ?", *f.CategoryID)
	}
	if f.TagKey != "" {
		q = q.Where("post_id IN (SELECT post_id FROM v_published_post_tags WHERE requested_locale = ? AND key = ?)", f.Locale, f.TagKey)
	}
	return q
}

func (r *postRepo) List(ctx context.Context, f PostFilter, limit, offset int) ([]entities.PublishedPostCard, int64, error) {
	var total int64
	if err := r.filtered(ctx, f).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows, err := list[entities.PublishedPostCard](sel[entities.PublishedPostCard](r.filtered(ctx, f)).
		Order("published_at DESC, post_id DESC").Limit(limit).Offset(offset))
	return rows, total, err
}

func (r *postRepo) ByIDs(ctx context.Context, locale string, ids []int64) ([]entities.PublishedPostCard, error) {
	if len(ids) == 0 {
		return []entities.PublishedPostCard{}, nil
	}
	if err := checkIDs(ids); err != nil {
		return nil, err
	}
	return list[entities.PublishedPostCard](sel[entities.PublishedPostCard](r.db.WithContext(ctx)).
		Where("requested_locale = ? AND post_id IN ?", locale, ids))
}

func (r *postRepo) FindBySlug(ctx context.Context, locale, slug string) (*entities.PublishedPost, error) {
	return first[entities.PublishedPost](sel[entities.PublishedPost](r.db.WithContext(ctx)).
		Where("requested_locale = ? AND slug = ?", locale, slug).Order("is_fallback, post_id"))
}

func (r *postRepo) FindByID(ctx context.Context, locale string, postID int64) (*entities.PublishedPost, error) {
	return first[entities.PublishedPost](sel[entities.PublishedPost](r.db.WithContext(ctx)).
		Where("requested_locale = ? AND post_id = ?", locale, postID))
}

func (r *postRepo) FindSlugOwner(ctx context.Context, slug, defaultLocale string) (*entities.PublishedPostSlug, error) {
	return first[entities.PublishedPostSlug](sel[entities.PublishedPostSlug](r.db.WithContext(ctx)).
		Where("slug = ?", slug).Clauses(clause.OrderBy{Expression: clause.Expr{SQL: "(locale_code = ?) DESC, locale_code, post_id", Vars: []any{defaultLocale}}}))
}

func (r *postRepo) Media(ctx context.Context, locale string, postID int64) ([]entities.PublishedPostMedia, error) {
	return list[entities.PublishedPostMedia](sel[entities.PublishedPostMedia](r.db.WithContext(ctx)).
		Where("requested_locale = ? AND post_id = ?", locale, postID).Order("sort_order, post_media_id"))
}

func (r *postRepo) Tags(ctx context.Context, locale string, postIDs []int64) ([]entities.PublishedPostTag, error) {
	if len(postIDs) == 0 {
		return []entities.PublishedPostTag{}, nil
	}
	if err := checkIDs(postIDs); err != nil {
		return nil, err
	}
	return list[entities.PublishedPostTag](sel[entities.PublishedPostTag](r.db.WithContext(ctx)).
		Where("requested_locale = ? AND post_id IN ?", locale, postIDs).Order("post_id, name, tag_id"))
}

type albumRepo struct{ db *gorm.DB }

func (r *albumRepo) List(ctx context.Context, locale string) ([]entities.PublishedAlbum, error) {
	return list[entities.PublishedAlbum](sel[entities.PublishedAlbum](r.db.WithContext(ctx)).
		Where("requested_locale = ?", locale).Order("published_at DESC, album_id DESC"))
}

func (r *albumRepo) FindByKey(ctx context.Context, locale, key string) (*entities.PublishedAlbum, error) {
	return first[entities.PublishedAlbum](sel[entities.PublishedAlbum](r.db.WithContext(ctx)).
		Where("requested_locale = ? AND key = ?", locale, key))
}

func (r *albumRepo) Items(ctx context.Context, locale string, albumID int64, limit int) ([]entities.PublishedAlbumItem, error) {
	q := sel[entities.PublishedAlbumItem](r.db.WithContext(ctx)).
		Where("requested_locale = ? AND album_id = ?", locale, albumID).Order("sort_order, album_item_id")
	if limit > 0 {
		q = q.Limit(limit)
	}
	return list[entities.PublishedAlbumItem](q)
}

type faqRepo struct{ db *gorm.DB }

func (r *faqRepo) Items(ctx context.Context, locale, groupKey string) ([]entities.PublishedFAQItem, error) {
	return list[entities.PublishedFAQItem](sel[entities.PublishedFAQItem](r.db.WithContext(ctx)).
		Where("requested_locale = ? AND group_key = ?", locale, groupKey).Order("sort_order, faq_item_id"))
}

type formRepo struct{ db *gorm.DB }

func (r *formRepo) Find(ctx context.Context, locale, slug string) (*entities.PublishedForm, error) {
	return first[entities.PublishedForm](sel[entities.PublishedForm](r.db.WithContext(ctx)).
		Where("requested_locale = ? AND slug = ?", locale, slug))
}

func (r *formRepo) Fields(ctx context.Context, locale, slug string) ([]entities.PublishedFormField, error) {
	return list[entities.PublishedFormField](sel[entities.PublishedFormField](r.db.WithContext(ctx)).
		Where("requested_locale = ? AND form_slug = ?", locale, slug).Order("field_key"))
}

type collectionRepo struct{ db *gorm.DB }

func (r *collectionRepo) Entries(ctx context.Context, locale, key string, limit, offset int) ([]entities.PublishedCollectionEntry, int64, error) {
	base := func() *gorm.DB {
		return r.db.WithContext(ctx).Table("v_published_collection_entries").Where("requested_locale = ? AND collection_key = ?", locale, key)
	}
	var total int64
	if err := base().Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows, err := list[entities.PublishedCollectionEntry](sel[entities.PublishedCollectionEntry](base()).
		Order("sort_order, published_at DESC, entry_id DESC").Limit(limit).Offset(offset))
	return rows, total, err
}

type seoRepo struct{ db *gorm.DB }

func (r *seoRepo) Sitemap(ctx context.Context, locale string) ([]entities.SitemapEntry, error) {
	return list[entities.SitemapEntry](sel[entities.SitemapEntry](r.db.WithContext(ctx)).
		Where("requested_locale = ?", locale).Order("kind, path"))
}

func (r *seoRepo) Redirect(ctx context.Context, path, locale string) (*entities.ActiveRedirect, error) {
	return first[entities.ActiveRedirect](sel[entities.ActiveRedirect](r.db.WithContext(ctx)).
		Where("source_path = ? AND (locale_code IS NULL OR locale_code = '' OR locale_code = ?)", path, locale).
		Clauses(clause.OrderBy{Expression: clause.Expr{SQL: "CASE WHEN locale_code = ? THEN 0 ELSE 1 END", Vars: []any{locale}}}))
}

type assetRepo struct{ db *gorm.DB }

func (r *assetRepo) ByIDs(ctx context.Context, locale string, ids []int64) ([]entities.PublicAsset, error) {
	if len(ids) == 0 {
		return []entities.PublicAsset{}, nil
	}
	if err := checkIDs(ids); err != nil {
		return nil, err
	}
	return list[entities.PublicAsset](sel[entities.PublicAsset](r.db.WithContext(ctx)).
		Where("requested_locale = ? AND asset_id IN ?", locale, ids).Order("asset_id"))
}
