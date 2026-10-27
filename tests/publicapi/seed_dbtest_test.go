//go:build dbtest

package publicapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/repositories"
	"github.com/materm-organization/materm-web-portal-backend/pkg/media"
	"github.com/materm-organization/materm-web-portal-backend/pkg/publiccache"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/jsondiff"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/schemadb"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/seedfake"
)

// TestSeededPagesEqualTheFrontendBundle is the M2 acceptance check "golden of portal-backend (la/en) = the
// fixture of portal-frontend": on a database that the ADMIN backend built and seeded (its own
// bootstrap.Migrate() followed by bootstrap.Seed, which loads the web content seed of T1.2.5: the 17 sections
// of the five pages, the post, the album, 24 media rows), the five page bodies served from the real views must
// equal the bundled bodies of materm-web-portal-frontend/src/content/cms-seed.
//
// WHY IT SKIPS BY DEFAULT. The portal repository cannot run the admin's Migrate/Seed (it may not import the
// admin module, and the doc SQL that pkg/testutil/schemadb applies has no web content seed). So the database
// is prepared OUTSIDE this test and handed over through MATERM_APP_SEEDED_DSN (pkg/testutil/schemadb.Seeded):
//
//  1. admin repo: `make testdb-up`; create a scratch database on that cluster;
//  2. admin repo: run bootstrap.Migrate() and bootstrap.Seed against it (its dbtest harness does exactly this:
//     the migrate/seed tests build such a database; the integration check keeps one and prints its DSN);
//  3. portal repo: MATERM_APP_SEEDED_DSN=<that database's superuser DSN> make test-db
//     (or `go test -tags dbtest -run TestSeededPagesEqualTheFrontendBundle ./tests/publicapi/`).
//
// The guards of dbtest apply to that DSN (never `materm`, never port 5432, the cluster must be a scratch one),
// and the pages are read through a throw-away member of cms_portal_ro, i.e. with production privileges.
// Until the variable is set the offline golden TestEndpointsEqualTheFrontendBundle covers the same bodies from
// the raw seed; a SKIP of this test is therefore "not run", never "passed".
func TestSeededPagesEqualTheFrontendBundle(t *testing.T) {
	if os.Getenv(schemadb.EnvSeededDSN) == "" {
		t.Skip("SKIPPED, NOT PASSED: " + schemadb.EnvSeededDSN + " is not set. Prepare a scratch database with the admin " +
			"repository's bootstrap.Migrate() + bootstrap.Seed (T1.2.5 web content seed) and point the variable at it " +
			"(plan: see the comment of this test). The offline golden TestEndpointsEqualTheFrontendBundle covers the " +
			"same bodies from the raw seed.")
	}
	dir := seedfake.Dir()
	if dir == "" {
		t.Skip("materm-web-portal-frontend/src/content/cms-seed is not next to this repository")
	}
	s := schemadb.Seeded(t)
	st := wire(t, repositories.New(s.GormRO(t)), publiccache.New(publiccache.Options{}),
		media.NewBuilder(media.Config{PathIP: "https://files.example.test", StaticPrefix: "static/"}), "https://fallback.example.test")

	// the database was named explicitly, so a missing seed is an error, not a skip
	sections := ownerInt64(t, s, `select count(*) from cms_page_sections ps join cms_pages p on p.id = ps.page_id where p.key = 'our-path'`)
	mediaRows := ownerInt64(t, s, `select count(*) from media_assets where storage_key like 'static/%' and status = 'ready'`)
	if sections == 0 || mediaRows < 24 {
		t.Fatalf("%s names a database without the web content seed (our-path sections=%d, static media=%d): "+
			"run the admin Seed on it", schemadb.EnvSeededDSN, sections, mediaRows)
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
