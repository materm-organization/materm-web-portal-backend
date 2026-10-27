//go:build dbtest

package repositories_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/materm-organization/materm-web-portal-backend/bootstrap"
	"github.com/materm-organization/materm-web-portal-backend/modules/content/entities"
	"github.com/materm-organization/materm-web-portal-backend/modules/content/repositories"
	"github.com/materm-organization/materm-web-portal-backend/pkg/localize"
	"github.com/materm-organization/materm-web-portal-backend/pkg/media"
	"github.com/materm-organization/materm-web-portal-backend/pkg/richtext"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/schemadb"
)

// The 21 views the portal role may read (doc/03 section 5, 011_roles_grants.sql).
var grantedViews = []string{
	"v_public_locales", "v_public_ui_strings", "v_published_pages", "v_published_page_sections", "v_published_menu_items",
	"v_published_categories", "v_published_tags", "v_published_posts", "v_published_post_tags", "v_published_post_slugs",
	"v_published_post_media", "v_published_albums", "v_published_album_items", "v_published_faq_items", "v_public_site_settings",
	"v_published_collection_entries", "v_sitemap", "v_active_redirects", "v_published_forms", "v_published_form_fields", "v_public_assets",
}

func setup(t *testing.T) (*schemadb.Schema, *repositories.Set) {
	t.Helper()
	s := schemadb.Shared(t)
	return s, repositories.New(s.GormRO(t))
}

func sqlState(err error) string {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

func str(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

// ---- the role --------------------------------------------------------------------------------------------------------

func TestRoleReadsTheViewsAndNothingElse(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()

	var cur string
	var super, owner bool
	if err := s.RO.QueryRowContext(ctx, `select current_user, (select rolsuper from pg_roles where rolname = current_user),
		(select pg_get_userbyid(datdba) = current_user from pg_database where datname = current_database())`).Scan(&cur, &super, &owner); err != nil {
		t.Fatal(err)
	}
	if cur != s.ROUser || super || owner {
		t.Fatalf("connected as %q super=%v owner=%v", cur, super, owner)
	}

	for _, v := range grantedViews {
		var n int
		if err := s.RO.QueryRowContext(ctx, `select count(*) from `+v).Scan(&n); err != nil {
			t.Errorf("%s: %v", v, err)
		} else if n == 0 {
			t.Errorf("%s has no fixture rows: the repository test for it would prove nothing", v)
		}
	}

	denied := []string{
		// base tables (drafts, personal data, internals)
		"cms_pages", "cms_posts", "cms_post_translations", "cms_content_revisions", "cms_preview_tokens", "forms_form_submissions", "forms_forms",
		"iam_users", "ops_outbox", "ops_audit_logs", "media_assets", "cms_locales", "cms_section_types",
		// views that exist but are not granted
		"v_locale_resolution", "v_user_permissions",
	}
	for _, rel := range denied {
		_, err := s.RO.ExecContext(ctx, `select 1 from `+rel+` limit 1`)
		if sqlState(err) != "42501" {
			t.Errorf("reading %s must be denied with 42501, got %v", rel, err)
		}
	}

	writes := []string{
		`insert into cms_locales (code, name, native_name, short_label) values ('xx','x','x','X')`,
		`update v_public_locales set name = 'x'`,
		`delete from v_public_locales`,
		`insert into forms_form_submissions (form_id, data) values (1, '{}')`,
		`insert into ops_outbox (event_type, aggregate_type, tags, payload) values ('x','x','{}','{}')`,
		`truncate cms_pages`,
		`create table portal_should_not_exist (id int)`,
		`create view portal_should_not_exist_v as select 1`,
	}
	for _, stmt := range writes {
		if _, err := s.RO.ExecContext(ctx, stmt); sqlState(err) != "42501" {
			t.Errorf("%q must be denied with 42501, got %v", stmt, err)
		}
	}

	// functions: exactly the two SECURITY DEFINER ones
	canExec := func(sig string) bool {
		var ok bool
		if err := s.RO.QueryRowContext(ctx, `select has_function_privilege(current_user, $1::regprocedure, 'execute')`, sig).Scan(&ok); err != nil {
			t.Fatalf("%s: %v", sig, err)
		}
		return ok
	}
	for _, sig := range []string{
		"forms_submit_form(text, text, jsonb, bytea, text, smallint, text, timestamptz, text)", "cms_consume_preview_token(bytea)",
	} {
		if !canExec(sig) {
			t.Errorf("%s must be executable", sig)
		}
	}
	for _, sig := range []string{"util_uuid_v7()", "forms_purge_expired_submissions(integer)", "forms_erase_by_email(text)", "ops_claim_outbox(integer, text)", "cms_upsert_section_type(jsonb)"} {
		if canExec(sig) {
			t.Errorf("%s must not be executable by the portal role", sig)
		}
	}
}

// The test role is "exactly cms_portal_ro": for every relation and function of public, both answer the same.
func TestRoleHasExactlyThePrivilegesOfCmsPortalRo(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	rows, err := s.DB.SQL.QueryContext(ctx, `
		select c.oid::regclass::text,
		       (select string_agg(p, ',' order by p) from unnest(array['select','insert','update','delete','truncate','references','trigger']) p
		         where has_table_privilege($1, c.oid, p)) as mine,
		       (select string_agg(p, ',' order by p) from unnest(array['select','insert','update','delete','truncate','references','trigger']) p
		         where has_table_privilege('cms_portal_ro', c.oid, p)) as prod
		from pg_class c where c.relnamespace = 'public'::regnamespace and c.relkind in ('r','p','v','m')`, s.ROUser)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var rel string
		var mine, prod sql.NullString
		if err = rows.Scan(&rel, &mine, &prod); err != nil {
			t.Fatal(err)
		}
		n++
		if mine != prod {
			t.Errorf("%s: test role has %q, cms_portal_ro has %q", rel, mine.String, prod.String)
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if n < 60 {
		t.Errorf("only %d relations compared", n)
	}
	var diff int
	if err = s.DB.SQL.QueryRowContext(ctx, `select count(*) from pg_proc p where p.pronamespace = 'public'::regnamespace and p.prokind = 'f'
		and has_function_privilege($1, p.oid, 'execute') <> has_function_privilege('cms_portal_ro', p.oid, 'execute')`, s.ROUser).Scan(&diff); err != nil {
		t.Fatal(err)
	}
	if diff != 0 {
		t.Errorf("%d functions differ", diff)
	}
	var selectable int
	if err = s.DB.SQL.QueryRowContext(ctx, `select count(*) from pg_class c where c.relnamespace = 'public'::regnamespace and c.relkind in ('r','p','v')
		and has_table_privilege($1, c.oid, 'select')`, s.ROUser).Scan(&selectable); err != nil {
		t.Fatal(err)
	}
	if selectable != len(grantedViews) {
		t.Errorf("the role can SELECT %d relations, want exactly the %d views", selectable, len(grantedViews))
	}
}

// bootstrap.CheckRuntimeRole (the start-up check) accepts this role even in strict mode.
func TestStartupRoleCheckAcceptsTheReadOnlyRole(t *testing.T) {
	s := schemadb.Shared(t)
	if _, err := bootstrap.CheckRuntimeRole(context.Background(), s.GormRO(t), true); err != nil {
		t.Errorf("strict check refused the read-only role: %v", err)
	}
}

// ---- entities vs the database ----------------------------------------------------------------------------------------

// Every column an entity names exists in its view, and between them the entities cover every column of every
// granted view: a renamed or added column in the SQL cannot go unnoticed.
func TestEntitiesMatchTheViewColumns(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	dbCols := map[string]map[string]bool{}
	for _, v := range grantedViews {
		dbCols[v] = map[string]bool{}
	}
	rows, err := s.DB.SQL.QueryContext(ctx, `select table_name, column_name from information_schema.columns where table_schema = 'public' and table_name like 'v\_%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var tbl, col string
		if err = rows.Scan(&tbl, &col); err != nil {
			t.Fatal(err)
		}
		if m, ok := dbCols[tbl]; ok {
			m[col] = true
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	covered := map[string]map[string]bool{}
	for _, e := range entities.All() {
		view, err := repositories.TableOf(e)
		if err != nil {
			t.Fatal(err)
		}
		cols, err := repositories.Columns(e)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := dbCols[view]; !ok {
			t.Errorf("%T maps %q, which is not one of the 21 granted views", e, view)
			continue
		}
		if covered[view] == nil {
			covered[view] = map[string]bool{}
		}
		for _, c := range cols {
			if !dbCols[view][c] {
				t.Errorf("%T: column %q does not exist in %s", e, c, view)
			}
			covered[view][c] = true
		}
	}
	for _, v := range grantedViews {
		if len(covered[v]) == 0 {
			t.Errorf("no entity reads %s", v)
		}
		var missing []string
		for c := range dbCols[v] {
			if !covered[v][c] {
				missing = append(missing, c)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Errorf("%s: columns not mapped by any entity: %v", v, missing)
		}
	}
}

// ---- repositories ------------------------------------------------------------------------------------------------------

func TestSiteRepositories(t *testing.T) {
	_, set := setup(t)
	ctx := context.Background()

	locales, err := set.Locales.ListActive(ctx)
	if err != nil || len(locales) != 2 {
		t.Fatalf("%v %v", locales, err)
	}
	la, en := locales[0], locales[1]
	if la.Code != "la" || !la.IsDefault || la.FallbackCode != nil || en.Code != "en" || en.IsDefault || str(en.FallbackCode) != "la" ||
		en.ShortLabel != "EN" || en.TextDirection != "ltr" || str(en.Flag) == "<nil>" || len(en.DateFormats) == 0 {
		t.Errorf("%+v %+v", la, en)
	}
	var df struct {
		Short  string   `json:"short"`
		Months []string `json:"months"`
	}
	if err = json.Unmarshal(en.DateFormats.Bytes(), &df); err != nil || len(df.Months) != 12 {
		t.Errorf("date_formats: %v %+v", err, df)
	}
	chainRows := make([]localize.Locale, 0, len(locales))
	for _, l := range locales {
		fb := ""
		if l.FallbackCode != nil {
			fb = *l.FallbackCode
		}
		chainRows = append(chainRows, localize.Locale{Code: l.Code, FallbackCode: fb, IsDefault: l.IsDefault})
	}
	chain, err := localize.NewChain(chainRows)
	if err != nil || strings.Join(chain.For("en"), ",") != "en,la" {
		t.Errorf("chain from v_public_locales: %v %v", err, chain)
	}

	ui, err := set.UIStrings.List(ctx, "en", nil)
	if err != nil || len(ui) != 39 {
		t.Fatalf("ui strings: %d %v", len(ui), err)
	}
	if ui[0].RequestedLocale != "en" || ui[0].ResolvedLocale == "" || ui[0].Key == "" {
		t.Errorf("%+v", ui[0])
	}
	blog, err := set.UIStrings.List(ctx, "en", []string{"blog"})
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range blog {
		if str(u.Namespace) != "blog" {
			t.Errorf("namespace filter: %+v", u)
		}
	}
	if len(blog) == 0 || len(blog) >= len(ui) {
		t.Errorf("blog namespace: %d of %d", len(blog), len(ui))
	}

	settings, err := set.Settings.List(ctx, "en", []string{"brand", "contact"})
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]entities.PublicSiteSetting{}
	for _, st := range settings {
		if st.GroupKey != "brand" && st.GroupKey != "contact" {
			t.Errorf("group filter: %+v", st)
		}
		byKey[st.GroupKey+"."+st.Key] = st
	}
	if string(byKey["brand.name"].Value) != `"MATERM"` || byKey["brand.statement"].ValueType != "text" ||
		!strings.HasPrefix(string(byKey["brand.statement"].Value), `"Forging`) {
		t.Errorf("settings: %+v", byKey["brand.name"])
	}
	if v := byKey["brand.logo_media_id"]; v.Value != nil || v.MediaAssetID != nil || v.ValueType != "media" {
		t.Errorf("media setting without a value: %+v", v)
	}
	if _, private := byKey["analytics.ga4_id"]; private {
		t.Error("settings of another group")
	}

	items, err := set.Menus.Items(ctx, "en", "main")
	if err != nil || len(items) != 5 {
		t.Fatalf("menu: %d %v", len(items), err)
	}
	if items[0].LinkType != "page" || str(items[0].Href) != "/" || items[0].Label != "Home" || items[0].ActiveMatch != "exact" || items[0].PageID == nil || items[0].ParentID != nil {
		t.Errorf("%+v", items[0])
	}
	social, err := set.Menus.Items(ctx, "en", "social")
	if err != nil || len(social) != 4 || social[0].LinkType != "url" || !social[0].OpenInNewTab || str(social[0].Icon) != "instagram" || social[0].PageID != nil {
		t.Errorf("social: %v %+v", err, social)
	}
	if none, err := set.Menus.Items(ctx, "en", "nope"); err != nil || len(none) != 0 || none == nil {
		t.Errorf("unknown menu: %v %v", none, err)
	}
}

func TestPageRepository(t *testing.T) {
	_, set := setup(t)
	ctx := context.Background()

	home, err := set.Pages.FindByPath(ctx, "en", "/")
	if err != nil {
		t.Fatal(err)
	}
	if home.Key != "home" || home.Path != "/" || !home.IsHome || home.Title != "Home" || home.ResolvedLocale != "en" || home.IsFallback ||
		str(home.SEOTitle) != "Materm" || !home.SEOTitleAbsolute || home.PublishedAt == nil || home.PublicID == "" || len(home.PublicID) != 36 || home.Template != "home" {
		t.Errorf("%+v", home)
	}
	if _, err = set.Pages.FindByPath(ctx, "en", "/nope"); !errors.Is(err, repositories.ErrNotFound) {
		t.Errorf("unknown path: %v", err)
	}
	byKey, err := set.Pages.FindByKey(ctx, "la", "our-path")
	if err != nil || byKey.Path != "/our-path" || byKey.ResolvedLocale != "la" {
		t.Errorf("%v %+v", err, byKey)
	}
	byIDs, err := set.Pages.ByIDs(ctx, "en", []int64{home.PageID, byKey.PageID, 99999})
	if err != nil || len(byIDs) != 2 {
		t.Errorf("ByIDs: %v %d", err, len(byIDs))
	}

	secs, err := set.Pages.Sections(ctx, home.PageID)
	if err != nil {
		t.Fatal(err)
	}
	if len(secs) != 1 {
		t.Fatalf("only the visible section: %d", len(secs))
	}
	sec := secs[0]
	if sec.SectionKey != "hero_brand" || sec.SectionVersion != 1 || str(sec.Variant) != "normal" || str(sec.AnchorID) != "hero" || sec.SortOrder != 1 ||
		sec.SharedSectionID != nil || sec.PublicID == "" {
		t.Errorf("%+v", sec)
	}
	props, err := localize.DecodeObject(sec.Props.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	out, fb := localize.Collapse(props, localize.MustHints("title", "background.alt"), chainFor("en"))
	if out["title"] != "Hello en" || fb {
		t.Errorf("collapsed props: %v %v", out["title"], fb)
	}
	if ids := media.CollectIDs(out); len(ids) != 1 || ids[0] != 1 {
		t.Errorf("media ids in props: %v", ids)
	}
	if none, err := set.Pages.Sections(ctx, 99999); err != nil || len(none) != 0 {
		t.Errorf("unknown page: %v %v", none, err)
	}
}

func chainFor(code string) []string {
	c, _ := localize.NewChain([]localize.Locale{{Code: "la", IsDefault: true}, {Code: "en", FallbackCode: "la"}})
	return c.For(code)
}

func TestTaxonomyRepository(t *testing.T) {
	_, set := setup(t)
	ctx := context.Background()
	cats, err := set.Taxonomy.Categories(ctx, "en")
	if err != nil || len(cats) != 5 {
		t.Fatalf("%v %d", err, len(cats))
	}
	if cats[0].Key != "music" || cats[0].Name != "Music" || cats[0].Slug != "music" || cats[0].SortOrder != 1 || cats[0].ResolvedLocale != "en" || cats[0].CategoryID == 0 {
		t.Errorf("%+v", cats[0])
	}
	c, err := set.Taxonomy.CategoryByKey(ctx, "la", "camping")
	if err != nil || c.Key != "camping" {
		t.Errorf("%v %+v", err, c)
	}
	if _, err = set.Taxonomy.CategoryByKey(ctx, "en", "nope"); !errors.Is(err, repositories.ErrNotFound) {
		t.Errorf("%v", err)
	}
	tags, err := set.Taxonomy.Tags(ctx, "en")
	if err != nil || len(tags) != 2 || tags[0].Name != "Night" || tags[0].Key != "night" || tags[0].Slug != "night" {
		t.Errorf("%v %+v", err, tags)
	}
}

func TestPostRepository(t *testing.T) {
	_, set := setup(t)
	ctx := context.Background()

	cards, total, err := set.Posts.List(ctx, repositories.PostFilter{Locale: "en"}, 12, 0)
	if err != nil {
		t.Fatal(err)
	}
	// draft, future and time-boxed-out posts are not there
	if total != 2 || len(cards) != 2 {
		t.Fatalf("total %d, cards %d", total, len(cards))
	}
	newest, older := cards[0], cards[1]
	if newest.Slug != "camp-weekend" || !newest.IsFallback || newest.ResolvedLocale != "la" || newest.RequestedLocale != "en" || str(newest.Excerpt) != "Camping excerpt" {
		t.Errorf("newest (la only, shown to en as fallback): %+v", newest)
	}
	if older.Slug != "music-night" || older.IsFallback || older.Title != "Music night" || str(older.Label) != "Music Night" || str(older.LocationLabel) != "Phou Khao Khouay" ||
		!older.IsFeatured || str(older.CategoryKey) != "music" || str(older.CategoryName) != "Music" || str(older.CategorySlug) != "music" ||
		str(older.AuthorName) != "Materm Team" || older.AuthorID == nil || older.PublishedAt == nil || older.CardMediaID == nil || older.TeaserMediaID == nil || older.HeroMediaID == nil ||
		*older.CardMediaID != *older.TeaserMediaID || older.PublicID == "" {
		t.Errorf("older: %+v", older)
	}
	if newest.CardMediaID == nil || newest.TeaserMediaID != nil || *newest.CardMediaID != *newest.HeroMediaID {
		t.Errorf("card media falls back to the hero: %+v", newest)
	}

	// paging and filters
	page2, total, err := set.Posts.List(ctx, repositories.PostFilter{Locale: "en"}, 1, 1)
	if err != nil || total != 2 || len(page2) != 1 || page2[0].Slug != "music-night" {
		t.Errorf("page 2: %v %d %+v", err, total, page2)
	}
	musicCat, _ := set.Taxonomy.CategoryByKey(ctx, "en", "music")
	byCat, total, err := set.Posts.List(ctx, repositories.PostFilter{Locale: "en", CategoryID: &musicCat.CategoryID}, 12, 0)
	if err != nil || total != 1 || len(byCat) != 1 || byCat[0].Slug != "music-night" {
		t.Errorf("category filter: %v %d %+v", err, total, byCat)
	}
	byTag, total, err := set.Posts.List(ctx, repositories.PostFilter{Locale: "en", TagKey: "night"}, 12, 0)
	if err != nil || total != 1 || byTag[0].Slug != "music-night" {
		t.Errorf("tag filter: %v %d", err, total)
	}
	none, total, err := set.Posts.List(ctx, repositories.PostFilter{Locale: "en", TagKey: "trek"}, 12, 0)
	if err != nil || total != 0 || len(none) != 0 || none == nil {
		t.Errorf("empty tag: %v %d %v", err, total, none)
	}
	la, _, err := set.Posts.List(ctx, repositories.PostFilter{Locale: "la"}, 12, 0)
	if err != nil || len(la) != 2 || la[1].Slug != "music-night-la" || la[1].IsFallback {
		t.Errorf("la list: %v %+v", err, la)
	}

	// detail: step (1) of doc/08 2.3.3
	post, err := set.Posts.FindBySlug(ctx, "en", "music-night")
	if err != nil {
		t.Fatal(err)
	}
	if post.PostID != older.PostID || post.Body == nil || str(post.GalleryHeading) != "Gallery" || str(post.GalleryDescription) != "Photos of the night" ||
		str(post.HeroAlt) != "Hero alt" || str(post.SEOTitle) != "SEO title" || str(post.SEODescription) != "SEO description" || post.OGImageID == nil {
		t.Errorf("%+v", post)
	}
	fb, err := set.Posts.FindBySlug(ctx, "en", "camp-weekend")
	if err != nil || !fb.IsFallback || fb.ResolvedLocale != "la" {
		t.Errorf("fallback detail: %v %+v", err, fb)
	}
	for _, slug := range []string{"music-night-la", "nope", "draft-post", "future-post", "expired-post"} {
		if _, err = set.Posts.FindBySlug(ctx, "en", slug); !errors.Is(err, repositories.ErrNotFound) {
			t.Errorf("FindBySlug(en, %s): %v", slug, err)
		}
	}
	// the body renders through the richtext pipeline: the page link resolves, the image id is collected
	doc, _ := localize.DecodeObject(post.Body.Bytes())
	pages, _ := richtext.CollectLinks(doc)
	res := richtext.Resolve(doc, richtext.Full, richtext.MapResolver{Pages: map[int64]string{pages[0]: "/our-path"}})
	if len(res.MediaIDs) != 1 || !strings.Contains(fmt.Sprint(res.Doc), "/our-path") {
		t.Errorf("richtext of the fixture body: %v %v", res.MediaIDs, res.Doc)
	}

	// step (2): the slug of another locale
	owner, err := set.Posts.FindSlugOwner(ctx, "music-night-la", "la")
	if err != nil || owner.PostID != post.PostID || owner.LocaleCode != "la" {
		t.Errorf("slug owner: %v %+v", err, owner)
	}
	if _, err = set.Posts.FindSlugOwner(ctx, "nope", "la"); !errors.Is(err, repositories.ErrNotFound) {
		t.Errorf("unknown slug owner: %v", err)
	}
	if _, err = set.Posts.FindSlugOwner(ctx, "draft-post", "la"); !errors.Is(err, repositories.ErrNotFound) {
		t.Errorf("a draft has no public slug: %v", err)
	}
	// step (3): that post, in the viewer's locale
	byID, err := set.Posts.FindByID(ctx, "en", owner.PostID)
	if err != nil || byID.Slug != "music-night" {
		t.Errorf("step 3: %v %+v", err, byID)
	}
	if _, err = set.Posts.FindByID(ctx, "en", 999999); !errors.Is(err, repositories.ErrNotFound) {
		t.Errorf("unknown id: %v", err)
	}

	cardsByID, err := set.Posts.ByIDs(ctx, "en", []int64{post.PostID, 999999})
	if err != nil || len(cardsByID) != 1 {
		t.Errorf("ByIDs: %v %d", err, len(cardsByID))
	}
	pm, err := set.Posts.Media(ctx, "en", post.PostID)
	if err != nil || len(pm) != 1 || str(pm[0].Alt) != "Gallery alt en" || str(pm[0].Caption) != "Gallery caption en" || pm[0].SortOrder != 1 || pm[0].MediaID == 0 || pm[0].PostMediaID == 0 {
		t.Errorf("post media: %v %+v", err, pm)
	}
	pmLa, err := set.Posts.Media(ctx, "la", post.PostID)
	if err != nil || len(pmLa) != 1 || pmLa[0].Alt != nil {
		t.Errorf("no la alt override and no asset alt: %v %+v", err, pmLa)
	}
	tags, err := set.Posts.Tags(ctx, "en", []int64{post.PostID, newest.PostID})
	if err != nil || len(tags) != 1 || tags[0].Key != "night" || tags[0].Name != "Night" || tags[0].PostID != post.PostID {
		t.Errorf("tags: %v %+v", err, tags)
	}
}

func TestAlbumFAQFormCollectionRepositories(t *testing.T) {
	_, set := setup(t)
	ctx := context.Background()

	albums, err := set.Albums.List(ctx, "en")
	if err != nil || len(albums) != 1 {
		t.Fatalf("%v %d", err, len(albums))
	}
	a := albums[0]
	if a.Key != "forest" || a.Title != "Forest walk (en)" || str(a.Description) != "Album description" || a.CoverMediaID == nil || a.PublishedAt == nil || a.ResolvedLocale != "en" || a.PublicID == "" {
		t.Errorf("%+v", a)
	}
	byKey, err := set.Albums.FindByKey(ctx, "en", "forest")
	if err != nil || byKey.AlbumID != a.AlbumID {
		t.Errorf("%v %+v", err, byKey)
	}
	if _, err = set.Albums.FindByKey(ctx, "en", "nope"); !errors.Is(err, repositories.ErrNotFound) {
		t.Errorf("%v", err)
	}
	items, err := set.Albums.Items(ctx, "en", a.AlbumID, 0)
	if err != nil || len(items) != 2 {
		t.Fatalf("%v %d", err, len(items))
	}
	if items[0].SortOrder != 1 || str(items[0].CaptionTitle) != "Caption title" || str(items[0].CaptionDescription) != "Caption description" || str(items[0].Alt) != "Item alt" ||
		str(items[0].LayoutHint) != "wide" || items[0].MediaID == 0 || items[0].PublicID == "" || items[0].AlbumItemID == 0 {
		t.Errorf("%+v", items[0])
	}
	if items[1].CaptionTitle != nil || items[1].Alt != nil {
		t.Errorf("an item without translations: %+v", items[1])
	}
	if one, lerr := set.Albums.Items(ctx, "en", a.AlbumID, 1); lerr != nil || len(one) != 1 {
		t.Errorf("limit: %v %d", lerr, len(one))
	}

	faq, err := set.FAQ.Items(ctx, "en", "contact")
	if err != nil || len(faq) != 4 {
		t.Fatalf("%v %d", err, len(faq))
	}
	if faq[0].Question != "How do I get to Materm?" || faq[0].SortOrder != 1 || faq[0].GroupKey != "contact" || faq[0].ResolvedLocale != "en" {
		t.Errorf("%+v", faq[0])
	}
	var ans map[string]any
	if err = json.Unmarshal(faq[0].Answer.Bytes(), &ans); err != nil || ans["type"] != "doc" {
		t.Errorf("answer is a rich text doc: %v %v", err, ans)
	}
	if none, ferr := set.FAQ.Items(ctx, "en", "nope"); ferr != nil || len(none) != 0 {
		t.Errorf("%v %v", ferr, none)
	}

	form, err := set.Forms.Find(ctx, "en", "contact")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(form.FieldOrder, ",") != "name,email,phone,topic,message" || str(form.HoneypotField) != "company" || form.Title != "Send a message" || form.SubmitLabel != "Send message" ||
		str(form.SendingLabel) != "Sending…" || str(form.SuccessTitle) != "Thank you!" || form.RateLimitMax == nil || *form.RateLimitMax != 5 || *form.RateLimitWindowS != 600 ||
		form.MinFillSeconds == nil || form.RateLimitDailyMax == nil || *form.MaxPayloadBytes != 16384 || form.ResolvedLocale != "en" {
		t.Errorf("%+v", form)
	}
	var schema map[string]any
	var msgs map[string]string
	if json.Unmarshal(form.JSONSchema.Bytes(), &schema) != nil || schema["type"] != "object" || json.Unmarshal(form.Messages.Bytes(), &msgs) != nil || msgs["required"] == "" {
		t.Errorf("schema/messages: %s / %s", form.JSONSchema, form.Messages)
	}
	if _, err = set.Forms.Find(ctx, "en", "nope"); !errors.Is(err, repositories.ErrNotFound) {
		t.Errorf("%v", err)
	}
	fields, err := set.Forms.Fields(ctx, "en", "contact")
	if err != nil || len(fields) != 5 {
		t.Fatalf("%v %d", err, len(fields))
	}
	byField := map[string]entities.PublishedFormField{}
	for _, f := range fields {
		byField[f.FieldKey] = f
	}
	var opts map[string]string
	if byField["name"].Label != "Full name" || str(byField["name"].Placeholder) != "Your name" || byField["topic"].Placeholder != nil ||
		json.Unmarshal(byField["topic"].OptionLabels.Bytes(), &opts) != nil || opts["booking"] != "Booking & stays" || byField["name"].FormSlug != "contact" {
		t.Errorf("fields: %+v", byField)
	}

	entries, total, err := set.Collections.Entries(ctx, "en", "events", 10, 0)
	if err != nil || total != 1 || len(entries) != 1 {
		t.Fatalf("%v %d", err, total)
	}
	e := entries[0]
	if e.CollectionKey != "events" || e.Title != "Opening day" || str(e.Slug) != "opening-day" || e.ResolvedLocale != "la" || e.PublishedAt == nil || e.SchemaVersion < 1 ||
		!strings.Contains(string(e.Data), "2026-05-01") || !strings.Contains(string(e.LocalizedData), "camp") || e.PublicID == "" {
		t.Errorf("%+v", e)
	}
	if none, total, err := set.Collections.Entries(ctx, "en", "nope", 10, 0); err != nil || total != 0 || len(none) != 0 {
		t.Errorf("%v %d", err, total)
	}
}

func TestSEORepository(t *testing.T) {
	_, set := setup(t)
	ctx := context.Background()
	sm, err := set.SEO.Sitemap(ctx, "la")
	if err != nil {
		t.Fatal(err)
	}
	var pages, posts int
	paths := map[string]bool{}
	for _, e := range sm {
		paths[e.Path] = true
		switch e.Kind {
		case "page":
			pages++
		case "post":
			posts++
			if !strings.HasPrefix(e.Path, "/blog/") {
				t.Errorf("post path %q", e.Path)
			}
		default:
			t.Errorf("kind %q", e.Kind)
		}
		if e.Lastmod == nil || e.EntityID == 0 || e.RequestedLocale != "la" {
			t.Errorf("%+v", e)
		}
	}
	if pages != 5 || posts != 2 || !paths["/blog/music-night-la"] || !paths["/blog/camp-weekend"] || !paths["/"] {
		t.Errorf("pages %d posts %d %v", pages, posts, paths)
	}

	r, err := set.SEO.Redirect(ctx, "/old-blog", "en")
	if err != nil || r.Target != "/blog?en=1" || r.StatusCode != 302 || str(r.LocaleCode) != "en" {
		t.Errorf("locale specific redirect wins: %v %+v", err, r)
	}
	r, err = set.SEO.Redirect(ctx, "/old-blog", "la")
	if err != nil || r.Target != "/blog" || r.StatusCode != 301 || r.LocaleCode != nil {
		t.Errorf("generic redirect: %v %+v", err, r)
	}
	if _, err = set.SEO.Redirect(ctx, "/nothing", "en"); !errors.Is(err, repositories.ErrNotFound) {
		t.Errorf("%v", err)
	}
}

func TestAssetRepositoryAndMediaBuilder(t *testing.T) {
	_, set := setup(t)
	ctx := context.Background()

	ids := assetIDs(t, set)
	rows, err := set.Assets.ByIDs(ctx, "en", append(ids, 999999))
	if err != nil || len(rows) != 3 {
		t.Fatalf("pending and deleted assets are not in the view: %v %d", err, len(rows))
	}
	hero := rows[0]
	if hero.StorageKey != "static/images/site/home/hero-bg.png" || hero.MimeType != "image/png" || hero.Kind != "image" || hero.Bytes != 1000 ||
		*hero.Width != 3456 || *hero.Height != 2048 || *hero.FocalX != 0.5 || *hero.FocalY != 0.4 || str(hero.Blurhash) == "<nil>" || str(hero.DominantColor) != "#112233" ||
		str(hero.Alt) != "Hero background" || str(hero.Caption) != "A caption" || str(hero.Credit) != "Photo credit" || hero.PublicID == "" {
		t.Errorf("%+v", hero)
	}
	if !strings.Contains(string(hero.Variants), `"storage_key": "variants/hero-bg-640.webp"`) && !strings.Contains(string(hero.Variants), `"storage_key":"variants/hero-bg-640.webp"`) {
		t.Errorf("variants: %s", hero.Variants)
	}
	if rows[1].Alt != nil || rows[1].Blurhash != nil || rows[1].FocalX != nil || string(rows[1].Variants) != "[]" {
		t.Errorf("an asset without translations/variants: %+v", rows[1])
	}
	la, err := set.Assets.ByIDs(ctx, "la", ids[:1])
	if err != nil || str(la[0].Alt) != "Hero background (la)" {
		t.Errorf("la: %v %+v", err, la)
	}

	// end to end with pkg/media
	b := media.NewBuilder(media.Config{PathIP: "https://files.example.test/base", StaticPrefix: "static/"})
	m, err := b.Resolve(ctx, repositories.NewMediaLookup(set.Assets), "en", ids)
	if err != nil || len(m) != 3 {
		t.Fatalf("%v %d", err, len(m))
	}
	h := m[fmt.Sprint(ids[0])]
	if h.URL != "/images/site/home/hero-bg.png" || *h.Alt != "Hero background" || h.Focal == nil || h.Focal.Y != 0.4 || len(h.Variants) != 1 ||
		h.Variants[0].URL != "https://files.example.test/base/variants/hero-bg-640.webp" || *h.Mime != "image/png" {
		t.Errorf("%+v", h)
	}
	if m[fmt.Sprint(ids[1])].URL != "https://files.example.test/base/uploads/2026/photo%20a.webp" {
		t.Errorf("file service URL: %q", m[fmt.Sprint(ids[1])].URL)
	}
	// without file.path_ip the file-service images drop out, the static one stays
	m2, err := media.NewBuilder(media.Config{}).Resolve(ctx, repositories.NewMediaLookup(set.Assets), "en", ids)
	if err != nil || len(m2) != 1 {
		t.Errorf("%v %d", err, len(m2))
	}
}

// assetIDs returns the ids of the three ready fixture assets in id order: hero (static), "photo a", "photo-b".
func assetIDs(t *testing.T, set *repositories.Set) []int64 {
	t.Helper()
	cards, _, err := set.Posts.List(context.Background(), repositories.PostFilter{Locale: "en"}, 12, 0)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	var ids []int64
	add := func(p *int64) {
		if p != nil && !seen[*p] {
			seen[*p] = true
			ids = append(ids, *p)
		}
	}
	for _, c := range cards {
		add(c.HeroMediaID)
		add(c.TeaserMediaID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if len(ids) != 3 {
		t.Fatalf("fixture assets: %v", ids)
	}
	return ids
}
