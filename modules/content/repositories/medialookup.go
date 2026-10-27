package repositories

import (
	"context"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/entities"
	"github.com/materm-organization/materm-web-portal-backend/pkg/media"
)

// NewMediaLookup adapts the asset repository to pkg/media's Lookup, so that media.Builder.Resolve can load
// the data.media map of a response.
func NewMediaLookup(assets AssetRepository) media.Lookup { return mediaLookup{assets} }

type mediaLookup struct{ assets AssetRepository }

func (m mediaLookup) AssetsByIDs(ctx context.Context, locale string, ids []int64) ([]media.Source, error) {
	rows, err := m.assets.ByIDs(ctx, locale, ids)
	if err != nil {
		return nil, err
	}
	return AssetSources(rows), nil
}

// AssetSources converts view rows to media sources.
func AssetSources(rows []entities.PublicAsset) []media.Source {
	out := make([]media.Source, 0, len(rows))
	for _, a := range rows {
		out = append(out, media.Source{
			ID: a.AssetID, StorageKey: a.StorageKey, Mime: a.MimeType, Width: a.Width, Height: a.Height,
			FocalX: a.FocalX, FocalY: a.FocalY, Blurhash: a.Blurhash, DominantColor: a.DominantColor,
			Alt: a.Alt, Caption: a.Caption, Credit: a.Credit, Variants: a.Variants.Bytes(),
		})
	}
	return out
}
