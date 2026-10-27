// Package services holds the logic of the gallery module (P-11, P-12).
package services

import (
	"context"
	"errors"
	"strconv"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/entities"
	"github.com/materm-organization/materm-web-portal-backend/modules/content/readkit"
	"github.com/materm-organization/materm-web-portal-backend/modules/content/repositories"
	"github.com/materm-organization/materm-web-portal-backend/modules/gallery/models"
	"github.com/materm-organization/materm-web-portal-backend/pkg/apperror"
	"github.com/materm-organization/materm-web-portal-backend/pkg/media"
	"github.com/materm-organization/materm-web-portal-backend/pkg/pagination"
	"github.com/materm-organization/materm-web-portal-backend/pkg/publiccache"
)

// GalleryService serves the album endpoints. locale is already negotiated.
type GalleryService interface {
	Albums(ctx context.Context, locale string) (*models.AlbumsPage, error)
	// Album returns a published album with its items; 404 when the key names none (or it is not published).
	Album(ctx context.Context, locale, key string) (*models.AlbumDetail, error)
}

// Deps are the collaborators of the service.
type Deps struct {
	Content *repositories.Set
	Media   *media.Builder
	Lookup  media.Lookup
	Cache   *publiccache.Cache
}

type galleryService struct{ d Deps }

// NewGalleryService builds the service.
func NewGalleryService(d Deps) GalleryService { return &galleryService{d: d} }

func summary(a entities.PublishedAlbum) models.AlbumSummary {
	s := models.AlbumSummary{ID: a.AlbumID, Key: a.Key, Description: a.Description, ResolvedLocale: a.ResolvedLocale}
	title := a.Title
	s.Title = &title
	if a.CoverMediaID != nil {
		s.Cover = &models.ImageRef{MediaID: *a.CoverMediaID}
	}
	return s
}

// Albums is P-11. The list is not paged (a handful of albums); meta describes the one page so that the shape is
// the same as P-07's (doc/08 section 2.3.5).
func (s *galleryService) Albums(ctx context.Context, locale string) (*models.AlbumsPage, error) {
	page, _, err := publiccache.Get(ctx, s.d.Cache, publiccache.Spec{Resource: "albums", Locale: locale},
		func(ctx context.Context) (*models.AlbumsPage, []string, error) {
			rows, err := s.d.Content.Albums.List(ctx, locale)
			if err != nil {
				return nil, nil, err
			}
			items := make([]models.AlbumSummary, 0, len(rows))
			var ids []int64
			for _, r := range rows {
				items = append(items, summary(r))
				if r.CoverMediaID != nil {
					ids = append(ids, *r.CoverMediaID)
				}
			}
			m, err := s.d.Media.Resolve(ctx, s.d.Lookup, locale, ids)
			if err != nil {
				return nil, nil, err
			}
			limit := len(items)
			if limit < 1 {
				limit = 1
			}
			meta := pagination.NewMeta(int64(len(items)), pagination.ListQuery{Page: 1, Limit: limit})
			return &models.AlbumsPage{Items: items, Meta: meta, Media: m}, []string{"albums:list", "locales"}, nil
		})
	return page, err
}

// Album is P-12.
func (s *galleryService) Album(ctx context.Context, locale, key string) (*models.AlbumDetail, error) {
	if !readkit.KeyPattern.MatchString(key) {
		return nil, apperror.NotFound()
	}
	d, _, err := publiccache.Get(ctx, s.d.Cache,
		publiccache.Spec{Resource: "album", Locale: locale, Params: map[string]string{"key": key}},
		func(ctx context.Context) (*models.AlbumDetail, []string, error) {
			a, err := s.d.Content.Albums.FindByKey(ctx, locale, key)
			if errors.Is(err, repositories.ErrNotFound) {
				return nil, nil, apperror.NotFound()
			}
			if err != nil {
				return nil, nil, err
			}
			rows, err := s.d.Content.Albums.Items(ctx, locale, a.AlbumID, 0)
			if err != nil {
				return nil, nil, err
			}
			items := make([]models.AlbumItem, 0, len(rows))
			var ids []int64
			if a.CoverMediaID != nil {
				ids = append(ids, *a.CoverMediaID)
			}
			for _, r := range rows {
				items = append(items, models.AlbumItem{
					ID: r.AlbumItemID, MediaID: r.MediaID, Alt: r.Alt, CaptionTitle: r.CaptionTitle,
					CaptionDescription: r.CaptionDescription, SortOrder: r.SortOrder,
				})
				ids = append(ids, r.MediaID)
			}
			m, err := s.d.Media.Resolve(ctx, s.d.Lookup, locale, ids)
			if err != nil {
				return nil, nil, err
			}
			return &models.AlbumDetail{AlbumSummary: summary(*a), Locale: locale, Items: items, Media: m},
				[]string{"album:" + strconv.FormatInt(a.AlbumID, 10), "locales"}, nil
		})
	return d, err
}
