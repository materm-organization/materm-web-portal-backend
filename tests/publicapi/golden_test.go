package publicapi

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/materm-organization/materm-web-portal-backend/pkg/media"
	"github.com/materm-organization/materm-web-portal-backend/pkg/publiccache"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/jsondiff"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/seedfake"
)

// pageMask ignores generated ids and timestamps of a page body ({success, data: {...}}).
func pageMask(path string) bool {
	if path == ".data.page.public_id" || path == ".data.page.updated_at" {
		return true
	}
	return strings.HasPrefix(path, ".data.sections[") && strings.HasSuffix(path, "].id")
}

var mediaOpts = jsondiff.Options{Tolerated: map[string]bool{"caption": true, "credit": true, "dominant_color": true, "kind": true, "duration_ms": true, "bytes": true}, TolerateUnder: ".data.media."}

// TestEndpointsEqualTheFrontendBundle serves the raw seed through the real services, controllers, pipeline
// and portal key, and compares every body with the bundle the frontend was built against
// (src/content/cms-seed/data). This is the contract test of M2: where doc/08 and the frontend loader differ,
// the loader (the bundle) is right.
func TestEndpointsEqualTheFrontendBundle(t *testing.T) {
	db := seedfake.Load(t)
	st := wire(t, db.Set(), publiccache.New(publiccache.Options{}), media.NewBuilder(media.Config{}), "")
	app := st.App
	extras := map[string]bool{}

	compare := func(t *testing.T, target, goldenFile string, mask func(string) bool) {
		t.Helper()
		r := app.Get(t, target)
		if r.Status != http.StatusOK {
			t.Fatalf("%s: status %d: %s", target, r.Status, r.Body)
		}
		var golden any
		seedfake.ReadJSON(t, filepath.Join(db.Dir, "data", goldenFile), &golden)
		got, err := jsondiff.Generic(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		o := mediaOpts
		o.Mask, o.Extras = mask, extras
		if diffs := jsondiff.Diff(golden, got, o); len(diffs) > 0 {
			t.Fatalf("%s differs from %s:\n%s", target, goldenFile, strings.Join(diffs, "\n"))
		}
	}

	t.Run("P-01 locales", func(t *testing.T) { compare(t, "/api/v1/public/locales", "locales.json", nil) })
	t.Run("P-16 sitemap", func(t *testing.T) { compare(t, "/api/v1/public/sitemap", "sitemap.json", nil) })
	for _, loc := range []string{"la", "en"} {
		q := "?locale=" + loc
		t.Run("P-02 settings "+loc, func(t *testing.T) { compare(t, "/api/v1/public/settings"+q, loc+"/settings.json", nil) })
		t.Run("P-03 ui-strings "+loc, func(t *testing.T) { compare(t, "/api/v1/public/ui-strings"+q, loc+"/ui-strings.json", nil) })
		t.Run("P-05 site "+loc, func(t *testing.T) { compare(t, "/api/v1/public/site"+q, loc+"/site.json", nil) })
		for _, key := range []string{"main", "footer", "social"} {
			t.Run("P-04 menu "+key+" "+loc, func(t *testing.T) {
				compare(t, "/api/v1/public/menus/"+key+q, loc+"/menus/"+key+".json", nil)
			})
		}
		for _, key := range seedfake.PageKeys {
			path := "/"
			if key != "home" {
				path = "/" + key
			}
			t.Run("P-06 page "+key+" "+loc, func(t *testing.T) {
				compare(t, "/api/v1/public/pages/resolve?path="+path+"&locale="+loc, loc+"/pages/"+key+".json", pageMask)
			})
		}
	}
	t.Logf("media item keys beyond the frontend type (additive): %v", extras)
}
