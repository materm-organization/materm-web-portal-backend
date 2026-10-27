package repositories

import (
	"context"
	"strings"
	"sync"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/entities"
)

// recorder captures the SQL the repositories build, without a database: the DryRun session never executes.
type recorder struct {
	mu   sync.Mutex
	sqls []string
	vars [][]any
}

func (r *recorder) last() (string, []any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.sqls) == 0 {
		return "", nil
	}
	return r.sqls[len(r.sqls)-1], r.vars[len(r.vars)-1]
}

func (r *recorder) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.sqls...)
}

func newRecorded(t *testing.T) (*Set, *recorder) {
	t.Helper()
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: "host=127.0.0.1 port=1 user=x dbname=x sslmode=disable"}),
		&gorm.Config{DryRun: true, DisableAutomaticPing: true, Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	save := func(tx *gorm.DB) {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		rec.sqls = append(rec.sqls, tx.Statement.SQL.String())
		rec.vars = append(rec.vars, append([]any(nil), tx.Statement.Vars...))
	}
	if err = db.Callback().Query().After("gorm:query").Register("test:record", save); err != nil {
		t.Fatal(err)
	}
	return New(db), rec
}

func TestPostListSelectsNoBody(t *testing.T) {
	set, rec := newRecorded(t)
	cat := int64(3)
	_, _, _ = set.Posts.List(context.Background(), PostFilter{Locale: "en", CategoryID: &cat, TagKey: "night"}, 12, 24)
	sqls := rec.all()
	if len(sqls) != 2 {
		t.Fatalf("count + select expected, got %v", sqls)
	}
	list := sqls[1]
	if strings.Contains(list, `"body"`) || strings.Contains(list, "*") {
		t.Errorf("a list must not select the body: %s", list)
	}
	for _, want := range []string{`FROM "v_published_posts"`, `requested_locale = $1`, `category_id = $2`, `v_published_post_tags`,
		`ORDER BY published_at DESC, post_id DESC`, `LIMIT $`, `OFFSET $`, `"card_media_id"`, `"is_fallback"`} {
		if !strings.Contains(list, want) {
			t.Errorf("list SQL lacks %q:\n%s", want, list)
		}
	}
	if !strings.Contains(sqls[0], "count(*)") {
		t.Errorf("count: %s", sqls[0])
	}
}

func TestPostDetailSelectsBodyAndOrdersRealTranslationFirst(t *testing.T) {
	set, rec := newRecorded(t)
	_, _ = set.Posts.FindBySlug(context.Background(), "en", "music-night")
	q, vars := rec.last()
	for _, want := range []string{`"body"`, `"gallery_heading"`, `"og_image_id"`, `requested_locale = $1 AND slug = $2`, `ORDER BY is_fallback, post_id`, `LIMIT`} {
		if !strings.Contains(q, want) {
			t.Errorf("lacks %q:\n%s", want, q)
		}
	}
	if len(vars) < 2 || vars[0] != "en" || vars[1] != "music-night" {
		t.Errorf("vars %v", vars)
	}
	_, _ = set.Posts.FindSlugOwner(context.Background(), "music-night", "la")
	q, _ = rec.last()
	if !strings.Contains(q, `FROM "v_published_post_slugs"`) || !strings.Contains(q, "(locale_code = $2) DESC, locale_code, post_id") {
		t.Errorf("slug owner order: %s", q)
	}
}

func TestEveryQueryFiltersOnLocaleAndNeverSelectsStar(t *testing.T) {
	set, rec := newRecorded(t)
	ctx := context.Background()
	_, _ = set.Locales.ListActive(ctx)
	_, _ = set.UIStrings.List(ctx, "en", []string{"site", "blog"})
	_, _ = set.Settings.List(ctx, "en", []string{"brand"})
	_, _ = set.Menus.Items(ctx, "en", "main")
	_, _ = set.Pages.FindByPath(ctx, "en", "/")
	_, _ = set.Pages.FindByKey(ctx, "en", "home")
	_, _ = set.Pages.ByIDs(ctx, "en", []int64{1, 2})
	_, _ = set.Pages.Sections(ctx, 1)
	_, _ = set.Taxonomy.Categories(ctx, "en")
	_, _ = set.Taxonomy.CategoryByKey(ctx, "en", "music")
	_, _ = set.Taxonomy.Tags(ctx, "en")
	_, _ = set.Posts.ByIDs(ctx, "en", []int64{1})
	_, _ = set.Posts.FindByID(ctx, "en", 1)
	_, _ = set.Posts.Media(ctx, "en", 1)
	_, _ = set.Posts.Tags(ctx, "en", []int64{1})
	_, _ = set.Albums.List(ctx, "en")
	_, _ = set.Albums.FindByKey(ctx, "en", "k")
	_, _ = set.Albums.Items(ctx, "en", 1, 5)
	_, _ = set.FAQ.Items(ctx, "en", "contact")
	_, _ = set.Forms.Find(ctx, "en", "contact")
	_, _ = set.Forms.Fields(ctx, "en", "contact")
	_, _, _ = set.Collections.Entries(ctx, "en", "events", 10, 0)
	_, _ = set.SEO.Sitemap(ctx, "la")
	_, _ = set.SEO.Redirect(ctx, "/old", "en")
	_, _ = set.Assets.ByIDs(ctx, "en", []int64{1})

	noLocale := map[string]bool{"v_public_locales": true, "v_published_page_sections": true, "v_published_post_slugs": true, "v_active_redirects": true}
	for _, q := range rec.all() {
		if strings.Contains(q, "SELECT *") || strings.Contains(q, ".*") {
			t.Errorf("select list must be explicit: %s", q)
		}
		view := strings.TrimSuffix(strings.SplitN(strings.SplitN(q, `FROM "`, 2)[1], `"`, 2)[0], `"`)
		if !noLocale[view] && !strings.Contains(q, "requested_locale") {
			t.Errorf("%s is not filtered by requested_locale: %s", view, q)
		}
		if strings.Contains(q, "FROM cms_") || strings.Contains(q, `FROM "cms_`) || strings.Contains(q, `FROM "v_locale_resolution"`) || strings.Contains(q, `"v_user_permissions"`) {
			t.Errorf("a base table or an ungranted view is read: %s", q)
		}
	}
}

func TestEmptyIDListsSkipTheQuery(t *testing.T) {
	set, rec := newRecorded(t)
	ctx := context.Background()
	if r, err := set.Assets.ByIDs(ctx, "en", nil); err != nil || r == nil || len(r) != 0 {
		t.Error("assets")
	}
	if r, err := set.Pages.ByIDs(ctx, "en", nil); err != nil || r == nil {
		t.Error("pages")
	}
	if r, err := set.Posts.ByIDs(ctx, "en", nil); err != nil || r == nil {
		t.Error("posts")
	}
	if r, err := set.Posts.Tags(ctx, "en", nil); err != nil || r == nil {
		t.Error("tags")
	}
	if len(rec.all()) != 0 {
		t.Errorf("no SQL expected: %v", rec.all())
	}
	big := make([]int64, maxIDs+1)
	if _, err := set.Assets.ByIDs(ctx, "en", big); err == nil {
		t.Error("oversized IN list must fail")
	}
}

func TestColumnsAndTableOf(t *testing.T) {
	cols, err := Columns(&entities.PublishedPostCard{})
	if err != nil {
		t.Fatal(err)
	}
	has := func(c string) bool {
		for _, x := range cols {
			if x == c {
				return true
			}
		}
		return false
	}
	if has("body") || !has("post_id") || !has("title") {
		t.Errorf("%v", cols)
	}
	full, _ := Columns(&entities.PublishedPost{})
	if len(full) != len(cols)+6 {
		t.Errorf("PublishedPost must add body + 5 detail columns (hero_alt is on the card): %d vs %d", len(full), len(cols))
	}
	if tn, _ := TableOf(&entities.PublishedPost{}); tn != "v_published_posts" {
		t.Errorf("table %q", tn)
	}
	if _, err := Columns(42); err == nil {
		t.Error("non-struct must fail")
	}
}
