//go:build dbtest

package publicapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/jsondiff"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/seedfake"
)

// TestSeededPagesEqualTheFrontendBundle is the M2 acceptance check "golden of portal-backend (la/en) = the
// fixture of portal-frontend": when the scratch schema holds the web content seed (T1.2.5: the 17 sections of
// the five pages, the post, the album, 24 media rows), the five page bodies served from the real views must
// equal the bundled bodies. Until that seed is loaded into the scratch schema the test skips and says why.
func TestSeededPagesEqualTheFrontendBundle(t *testing.T) {
	st, s := dbStack(t)
	dir := seedfake.Dir()
	if dir == "" {
		t.Skip("materm-web-portal-frontend/src/content/cms-seed is not next to this repository")
	}
	sections := ownerInt64(t, s, `select count(*) from cms_page_sections ps join cms_pages p on p.id = ps.page_id where p.key = 'our-path'`)
	media := ownerInt64(t, s, `select count(*) from media_assets where storage_key like 'static/%' and status = 'ready'`)
	if sections == 0 || media < 24 {
		if !siteSeedDelivered() {
			t.Skip("SKIPPED, NOT PASSED: the web content seed (T1.2.5, ../materm-admin-backend/bootstrap/seed_site*.go or " +
				"migrations/seeds/site/) has not been delivered yet; the offline golden TestEndpointsEqualTheFrontendBundle " +
				"covers the same bodies from the raw seed")
		}
		t.Skipf("SKIPPED, NOT PASSED: the web content seed exists in the admin repository but is not loaded into the "+
			"scratch schema (our-path sections=%d, static media=%d): pkg/testutil/schemadb needs a hook that loads it "+
			"(T1.2.12, integrator); then this test compares the five pages la/en with the frontend bundle", sections, media)
	}

	opts := jsondiff.Options{Mask: pageMask, Tolerated: map[string]bool{"caption": true, "credit": true, "dominant_color": true},
		TolerateUnder: ".data.media.", Extras: map[string]bool{}}
	for _, loc := range []string{"la", "en"} {
		for _, key := range seedfake.PageKeys {
			path := "/"
			if key != "home" {
				path = "/" + key
			}
			var golden any
			seedfake.ReadJSON(t, filepath.Join(dir, "data", loc, "pages", key+".json"), &golden)
			r := st.App.Get(t, "/api/v1/public/pages/resolve?path="+path+"&locale="+loc)
			if r.Status != 200 {
				t.Errorf("%s %s: %d %s", loc, key, r.Status, r.Body)
				continue
			}
			got, _ := jsondiff.Generic(r.Body)
			if diffs := jsondiff.Diff(golden, got, opts); len(diffs) > 0 {
				t.Errorf("%s %s differs from the frontend bundle:\n%s", loc, key, strings.Join(diffs, "\n"))
			}
		}
	}
}

// siteSeedDelivered reports whether T1.2.5 left its seed in the admin repository.
func siteSeedDelivered() bool {
	wd, _ := os.Getwd()
	for dir := wd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			admin := filepath.Join(filepath.Dir(dir), "materm-admin-backend")
			if m, _ := filepath.Glob(filepath.Join(admin, "bootstrap", "seed_site*.go")); len(m) > 0 {
				return true
			}
			_, err := os.Stat(filepath.Join(admin, "migrations", "seeds", "site"))
			return err == nil
		}
		if dir == filepath.Dir(dir) {
			return false
		}
	}
}
