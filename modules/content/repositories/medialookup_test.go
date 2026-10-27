package repositories

import (
	"context"
	"errors"
	"testing"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/entities"
	"github.com/materm-organization/materm-web-portal-backend/pkg/media"
)

type fakeAssets struct {
	rows []entities.PublicAsset
	err  error
}

func (f fakeAssets) ByIDs(context.Context, string, []int64) ([]entities.PublicAsset, error) {
	return f.rows, f.err
}

func TestMediaLookupBuildsTheMap(t *testing.T) {
	w, alt := 100, "alt"
	l := NewMediaLookup(fakeAssets{rows: []entities.PublicAsset{
		{AssetID: 5, StorageKey: "static/images/a.png", MimeType: "image/png", Width: &w, Alt: &alt, Variants: entities.JSONB(`[]`)},
		{AssetID: 6, StorageKey: "file/x.png", MimeType: "image/png"},
	}})
	m, err := media.NewBuilder(media.Config{}).Resolve(context.Background(), l, "en", []int64{5, 6})
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 1 || m["5"].URL != "/images/a.png" || *m["5"].Alt != "alt" || *m["5"].Width != 100 {
		t.Errorf("%+v", m)
	}
	if _, err = media.NewBuilder(media.Config{}).Resolve(context.Background(), NewMediaLookup(fakeAssets{err: errors.New("db")}), "en", []int64{1}); err == nil {
		t.Error("error must pass through")
	}
}
