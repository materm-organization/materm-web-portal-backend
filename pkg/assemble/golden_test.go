package assemble

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/repositories"
	"github.com/materm-organization/materm-web-portal-backend/pkg/media"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/jsondiff"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/seedfake"
)

// pageMask ignores the values that are generated, not authored: ids and timestamps.
func pageMask(path string) bool {
	if path == ".page.public_id" || path == ".page.updated_at" {
		return true
	}
	return strings.HasPrefix(path, ".sections[") && strings.HasSuffix(path, "].id")
}

func assemblerOver(set *repositories.Set) *Assembler {
	return New(Deps{
		Locales: set.Locales, Pages: set.Pages, Posts: set.Posts, Taxonomy: set.Taxonomy, Albums: set.Albums, FAQ: set.FAQ,
		Forms: set.Forms, Settings: set.Settings, Media: media.NewBuilder(media.Config{}), MediaLookup: repositories.NewMediaLookup(set.Assets),
	})
}

// TestGoldenPagesEqualTheFrontendBundle assembles the five known pages in both locales from the raw seed (the
// inputs of the frontend's own generator, served by pkg/testutil/seedfake) and compares them with the bundled
// bodies the frontend was built against (src/content/cms-seed/data/<locale>/pages/<key>.json).
func TestGoldenPagesEqualTheFrontendBundle(t *testing.T) {
	db := seedfake.Load(t)
	set := db.Set()
	a := assemblerOver(set)
	opts := jsondiff.Options{Mask: pageMask, Tolerated: map[string]bool{"caption": true, "credit": true, "dominant_color": true},
		TolerateUnder: ".media.", Extras: map[string]bool{}}
	ctx := context.Background()
	for _, loc := range []string{"la", "en"} {
		for _, key := range seedfake.PageKeys {
			t.Run(loc+"/"+key, func(t *testing.T) {
				var golden struct {
					Data any `json:"data"`
				}
				seedfake.ReadJSON(t, filepath.Join(db.Dir, "data", loc, "pages", key+".json"), &golden)
				page, err := set.Pages.FindByKey(ctx, loc, key)
				if err != nil {
					t.Fatal(err)
				}
				secs, _ := set.Pages.Sections(ctx, page.PageID)
				res, err := a.Page(ctx, PageInput{Locale: loc, Chain: db.Chain(loc), Page: page, Sections: secs})
				if err != nil {
					t.Fatal(err)
				}
				got, err := jsondiff.ToGeneric(res.Data)
				if err != nil {
					t.Fatal(err)
				}
				if diffs := jsondiff.Diff(golden.Data, got, opts); len(diffs) > 0 {
					t.Fatalf("%d differences from the frontend bundle:\n%s", len(diffs), strings.Join(diffs, "\n"))
				}
				if a.Skipped() != 0 {
					t.Errorf("sections were skipped: %d", a.Skipped())
				}
			})
		}
	}
	t.Logf("media item keys beyond the frontend type (additive): %v", opts.Extras)
}
