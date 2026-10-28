//go:build dbtest

package previewapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/repositories"
	previewrepos "github.com/materm-organization/materm-web-portal-backend/modules/preview/repositories"
	previewservices "github.com/materm-organization/materm-web-portal-backend/modules/preview/services"
	"github.com/materm-organization/materm-web-portal-backend/pkg/assemble"
	"github.com/materm-organization/materm-web-portal-backend/pkg/ratelimit"
	"github.com/materm-organization/materm-web-portal-backend/pkg/rediskeys"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/jsondiff"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/redistest"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/schemadb"
)

func TestMain(m *testing.M) {
	code := m.Run()
	schemadb.Teardown()
	os.Exit(code)
}

// baselinePage is the snapshot query of the ADMIN backend (bootstrap/seed_site_baseline.go, the only producer of
// the image of doc/04 section 5.2 that exists today), copied verbatim: the tests below preview what the real
// producer writes.
const baselinePage = `
select jsonb_build_object(
  'page', jsonb_build_object('key', p.key, 'name', p.name, 'template', p.template, 'path', p.path, 'parent_id', p.parent_id,
                             'is_home', p.is_home, 'noindex', p.noindex, 'in_sitemap', p.in_sitemap, 'sort_order', p.sort_order),
  'translations', coalesce((select jsonb_agg(jsonb_build_object('locale_code', t.locale_code, 'title', t.title, 'seo_title', t.seo_title,
        'seo_title_absolute', t.seo_title_absolute, 'seo_description', t.seo_description, 'og_image_id', t.og_image_id,
        'canonical_path', t.canonical_path, 'is_enabled', t.is_enabled) order by t.locale_code)
      from cms_page_translations t where t.page_id = p.id), '[]'::jsonb),
  'sections', coalesce((select jsonb_agg(jsonb_build_object('id', s.id, 'public_id', s.public_id, 'section_type', st.key,
        'section_version', st.version, 'variant', s.variant, 'sort_order', s.sort_order, 'is_visible', s.is_visible,
        'anchor_id', s.anchor_id, 'props', s.props, 'shared_section_id', s.shared_section_id) order by s.sort_order)
      from cms_page_sections s join cms_section_types st on st.id = s.section_type_id where s.page_id = p.id), '[]'::jsonb))::text
from cms_pages p where p.id = $1`

type dbEnv struct {
	s    *schemadb.Schema
	set  *repositories.Set
	home int64
}

func dbSetup(t *testing.T) *dbEnv {
	t.Helper()
	s := schemadb.Shared(t)
	e := &dbEnv{s: s, set: repositories.New(s.GormRO(t))}
	if err := s.Owner.QueryRow(`select id from cms_pages where key = 'home'`).Scan(&e.home); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *dbEnv) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	ctx := context.Background()
	conn, err := e.s.Owner.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `set cms.live_write = 'on'`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// issue does what the admin's POST /pages/{id}/preview-links does: a history revision of the page as it is now
// ('preview' note) and a token bound to it (only sha256 stored), then returns the raw token.
func (e *dbEnv) issue(t *testing.T, seed string, pageID int64, locale string, ttl time.Duration) (token string, tokenID int64) {
	t.Helper()
	var snap string
	if err := e.s.Owner.QueryRow(baselinePage, pageID).Scan(&snap); err != nil {
		t.Fatal(err)
	}
	return e.issueSnapshot(t, seed, "cms.pages", pageID, locale, ttl, snap)
}

func (e *dbEnv) issueSnapshot(t *testing.T, seed, entityType string, entityID int64, locale string, ttl time.Duration, snap string) (string, int64) {
	t.Helper()
	token := makeToken(seed)
	hash := sha256.Sum256([]byte(token))
	var rev int64
	if err := e.s.Owner.QueryRow(`insert into cms_content_revisions (entity_type, entity_id, version, kind, snapshot, change_note)
		values ($1, $2, (select coalesce(max(version), 0) + 1 from cms_content_revisions where entity_type = $1 and entity_id = $2), 'history', $3::jsonb, 'preview')
		returning id`, entityType, entityID, snap).Scan(&rev); err != nil {
		t.Fatal(err)
	}
	var loc any
	if locale != "" {
		loc = locale
	}
	var id int64
	if err := e.s.Owner.QueryRow(`insert into cms_preview_tokens (token_hash, revision_id, locale_code, expires_at)
		values ($1, $2, $3, now() + $4 * interval '1 second') returning id`, hash[:], rev, loc, int(ttl.Seconds())).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return token, id
}

func (e *dbEnv) uses(t *testing.T, id int64) int {
	t.Helper()
	var n int
	if err := e.s.Owner.QueryRow(`select use_count from cms_preview_tokens where id = $1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *dbEnv) stack(t *testing.T, o ...func(*opts)) *stack {
	t.Helper()
	all := []func(*opts){func(c *opts) {
		c.set = e.set
		c.tokens = previewrepos.New(e.s.GormRO(t))
		c.limiter = nil
	}}
	return newStack(t, append(all, o...)...)
}

// ---- tests --------------------------------------------------------------------------------------------------------

// The snapshot the ADMIN writes, consumed through the real SQL function by the read-only role, renders to the
// same body as P-06 for the live page.
func TestDBPreviewEqualsP06(t *testing.T) {
	e := dbSetup(t)
	st := e.stack(t)
	ctx := context.Background()
	chain, err := st.Provider.Chain(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, loc := range []string{"la", "en"} {
		tok, _ := e.issue(t, "eq"+loc, e.home, "", time.Hour)
		page, err := e.set.Pages.FindByKey(ctx, loc, "home")
		if err != nil {
			t.Fatal(err)
		}
		secs, _ := e.set.Pages.Sections(ctx, page.PageID)
		res, err := st.Public.Page(ctx, assemble.PageInput{Locale: loc, Chain: chain.For(loc), Page: page, Sections: secs})
		if err != nil {
			t.Fatal(err)
		}
		want, _ := jsondiff.ToGeneric(res.Data)

		r := st.resolve(t, tok, "?locale="+loc)
		if r.Status != http.StatusOK {
			t.Fatalf("%s: %d %s", loc, r.Status, r.Body)
		}
		got, _ := jsondiff.ToGeneric(r.Data()["page"])
		// public_id and section ids ARE comparable here (the snapshot carries public ids)
		mask := func(p string) bool { return p == ".page.updated_at" || p == ".page.seo.noindex" }
		if diffs := jsondiff.Diff(want, got, jsondiff.Options{Mask: mask, Extras: map[string]bool{}}); len(diffs) > 0 {
			t.Fatalf("%s: differences from P-06:\n%s", loc, strings.Join(diffs, "\n"))
		}
		if n := len(r.Data()["page"].(map[string]any)["sections"].([]any)); n != 1 {
			t.Errorf("%s: %d sections, want 1 (the hidden FAQ is skipped)", loc, n)
		}
	}
}

// unknown / expired / revoked / used by another entity type: one answer, from the real function.
func TestDBUnusableTokensAreIndistinguishable(t *testing.T) {
	e := dbSetup(t)
	st := e.stack(t)
	good, _ := e.issue(t, "db-good", e.home, "", time.Hour)
	expired, expiredID := e.issue(t, "db-expired", e.home, "", time.Hour)
	if _, err := e.s.Owner.Exec(`update cms_preview_tokens set expires_at = now() - interval '1 second' where id = $1`, expiredID); err != nil {
		t.Fatal(err)
	}
	revoked, revokedID := e.issue(t, "db-revoked", e.home, "", time.Hour)
	if _, err := e.s.Owner.Exec(`update cms_preview_tokens set revoked_at = now() where id = $1`, revokedID); err != nil {
		t.Fatal(err)
	}
	post, _ := e.issueSnapshot(t, "db-post", "cms.posts", 1, "", time.Hour, `{"post":{}}`)

	if r := st.resolve(t, good, ""); r.Status != 200 {
		t.Fatalf("control: %d %s", r.Status, r.Body)
	}
	var first []byte
	for name, tok := range map[string]string{"unknown": makeToken("db-unknown"), "expired": expired, "revoked": revoked, "post": post} {
		r := st.resolve(t, tok, "")
		if r.Status != http.StatusUnauthorized || r.Code() != "INVALID_TOKEN" {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
			continue
		}
		body := requestIDRe.ReplaceAll(r.Body, nil)
		if first == nil {
			first = body
		} else if !bytes.Equal(first, body) {
			t.Errorf("%s differs: %s vs %s", name, body, first)
		}
	}
	if r := st.resolve(t, good, "?path=/somewhere-else"); r.Status != http.StatusUnauthorized {
		t.Errorf("wrong path: %d", r.Status)
	}
}

// The link is frozen at issue time: a later edit of the live page does not show in the preview (08 section 3.9).
func TestDBFrozenSnapshot(t *testing.T) {
	e := dbSetup(t)
	st := e.stack(t)
	tok, _ := e.issue(t, "frozen", e.home, "en", time.Hour)
	title := func() string {
		r := st.resolve(t, tok, "")
		if r.Status != 200 {
			t.Fatalf("%d %s", r.Status, r.Body)
		}
		return r.Data()["page"].(map[string]any)["page"].(map[string]any)["title"].(string)
	}
	before := title()
	e.exec(t, `update cms_page_translations set title = 'EDITED AFTER THE LINK' where page_id = $1 and locale_code = 'en'`, e.home)
	defer e.exec(t, `update cms_page_translations set title = $2 where page_id = $1 and locale_code = 'en'`, e.home, before)
	if after := title(); after != before {
		t.Errorf("the preview followed the live edit: %q -> %q", before, after)
	}
	if live, _ := e.set.Pages.FindByKey(context.Background(), "en", "home"); live.Title != "EDITED AFTER THE LINK" {
		t.Fatalf("the live page did not change (%q): the test proves nothing", live.Title)
	}
}

// Many calls, one token: no use limit. Without the snapshot cache the function counts every call; with it,
// only the first inside the TTL.
func TestDBMultiUseAndUseCount(t *testing.T) {
	e := dbSetup(t)
	st := e.stack(t)
	tok, id := e.issue(t, "multi", e.home, "", time.Hour)
	for i := 0; i < 6; i++ {
		if r := st.resolve(t, tok, ""); r.Status != 200 {
			t.Fatalf("call %d: %d", i+1, r.Status)
		}
	}
	if n := e.uses(t, id); n != 6 {
		t.Errorf("use_count %d, want 6", n)
	}
	var maxUses *int
	if err := e.s.Owner.QueryRow(`select max_uses from cms_preview_tokens where id = $1`, id).Scan(&maxUses); err != nil || maxUses != nil {
		t.Errorf("max_uses %v %v, want NULL", maxUses, err)
	}

	r := redistest.New(t)
	cached := e.stack(t, func(c *opts) {
		c.redis, c.keys = r.Client, rediskeys.New(r.Prefix)
		c.svc = func(d *previewservices.Deps) { d.Options.CacheTTL = 10 * time.Second }
	})
	tok2, id2 := e.issue(t, "multi2", e.home, "", time.Hour)
	for i := 0; i < 6; i++ {
		if res := cached.resolve(t, tok2, ""); res.Status != 200 {
			t.Fatalf("cached call %d: %d", i+1, res.Status)
		}
	}
	if n := e.uses(t, id2); n != 1 {
		t.Errorf("with the snapshot cache use_count %d, want 1", n)
	}
}

// Revocation: immediate without the snapshot cache, bounded by its TTL with it (doc/08 section 3.9: <= 10 s).
func TestDBRevokeAndCacheBound(t *testing.T) {
	e := dbSetup(t)
	st := e.stack(t)
	tok, id := e.issue(t, "rev1", e.home, "", time.Hour)
	if r := st.resolve(t, tok, ""); r.Status != 200 {
		t.Fatal(r.Status)
	}
	if _, err := e.s.Owner.Exec(`update cms_preview_tokens set revoked_at = now() where id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if r := st.resolve(t, tok, ""); r.Status != http.StatusUnauthorized {
		t.Errorf("revoked, no cache: %d", r.Status)
	}

	rd := redistest.New(t)
	cached := e.stack(t, func(c *opts) {
		c.redis, c.keys = rd.Client, rediskeys.New(rd.Prefix)
		c.svc = func(d *previewservices.Deps) { d.Options.CacheTTL = 400 * time.Millisecond }
	})
	tok2, id2 := e.issue(t, "rev2", e.home, "", time.Hour)
	if r := cached.resolve(t, tok2, ""); r.Status != 200 {
		t.Fatal(r.Status)
	}
	if _, err := e.s.Owner.Exec(`update cms_preview_tokens set revoked_at = now() where id = $1`, id2); err != nil {
		t.Fatal(err)
	}
	if r := cached.resolve(t, tok2, ""); r.Status != 200 {
		t.Errorf("inside the cache TTL the snapshot is still served: %d", r.Status)
	}
	time.Sleep(600 * time.Millisecond)
	if r := cached.resolve(t, tok2, ""); r.Status != http.StatusUnauthorized {
		t.Errorf("after the TTL the revocation applies: %d", r.Status)
	}
}

// A preview never reads or writes the shared response cache: the only keys it leaves in Redis are its own
// snapshot copy (addressed by the token's hash) and the rate-limit counters.
func TestDBNeverTouchesTheResponseCache(t *testing.T) {
	e := dbSetup(t)
	rd := redistest.New(t)
	keys := rediskeys.New(rd.Prefix)
	st := e.stack(t, func(c *opts) {
		c.redis, c.keys, c.limiter = rd.Client, keys, ratelimit.NewRedis(rd.Client)
	})
	tok, _ := e.issue(t, "nocache", e.home, "", time.Hour)
	for i := 0; i < 3; i++ {
		st.resolve(t, tok, "")
	}
	st.resolve(t, makeToken("db-nope"), "")

	ctx := context.Background()
	iter := rd.Client.Scan(ctx, 0, rd.Prefix+"*", 100).Iterator()
	n := 0
	for iter.Next(ctx) {
		k := strings.TrimPrefix(iter.Val(), rd.Prefix)
		n++
		if !strings.HasPrefix(k, "preview:") && !strings.HasPrefix(k, "rl:preview") {
			t.Errorf("unexpected Redis key %q", iter.Val())
		}
		if strings.Contains(iter.Val(), tok) {
			t.Errorf("the raw token is in a Redis key: %q", iter.Val())
		}
	}
	if n == 0 {
		t.Fatal("no key at all: the snapshot copy and the counters were expected")
	}
	// and a page that WAS cached for the public is not served to a preview: the cache has no say in the body
	if rd.Client.Exists(ctx, keys.Gen()).Val() != 0 {
		t.Error("the response-cache generation counter was created")
	}
}

// The per-token and per-key ceilings with the real Redis limiter.
func TestDBRateLimit(t *testing.T) {
	e := dbSetup(t)
	rd := redistest.New(t)
	st := e.stack(t, func(c *opts) {
		c.redis, c.keys, c.limiter = rd.Client, rediskeys.New(rd.Prefix), ratelimit.NewRedis(rd.Client)
		c.svc = func(d *previewservices.Deps) {
			d.Options = previewservices.Options{TokenPerMinute: 4, KeyPerMinute: 100}
		}
	})
	tok, _ := e.issue(t, "rl", e.home, "", time.Hour)
	var last int
	for i := 0; i < 6; i++ {
		last = st.resolve(t, tok, "").Status
	}
	if last != http.StatusTooManyRequests {
		t.Errorf("6th call: %d, want 429", last)
	}
}

// The portal role may execute the function and nothing else of the preview tables.
func TestDBRoleCannotReadTheTokenTables(t *testing.T) {
	e := dbSetup(t)
	for _, tbl := range []string{"cms_preview_tokens", "cms_content_revisions"} {
		rows, err := e.s.RO.Query(`select 1 from ` + tbl + ` limit 1`)
		if err == nil {
			rows.Close()
			t.Errorf("the portal role can read %s", tbl)
		}
	}
	repo := previewrepos.New(e.s.GormRO(t))
	if _, err := repo.Consume(context.Background(), make([]byte, 32)); err != previewrepos.ErrInvalid {
		t.Errorf("unknown hash: %v, want ErrInvalid", err)
	}
}
