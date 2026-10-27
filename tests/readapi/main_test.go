//go:build dbtest

// Package readapi tests the content modules blog, gallery, faq and forms (PB-7, PB-8, PB-9) end to end on the
// scratch PostgreSQL and Redis: the real schema built from ../doc/sql, the real read repositories on a role
// with exactly the privileges of cms_portal_ro, the real response cache, and the real HTTP pipeline
// (bootstrap.NewFiber + the portal key guard). The wiring below is the wiring the integrator writes in
// cmd/application (T1.2.12); each module is mounted with its constructor and routes.Register.
package readapi

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	blogcontroller "github.com/materm-organization/materm-web-portal-backend/modules/blog/controller"
	blogroutes "github.com/materm-organization/materm-web-portal-backend/modules/blog/routes"
	blogservices "github.com/materm-organization/materm-web-portal-backend/modules/blog/services"
	"github.com/materm-organization/materm-web-portal-backend/modules/content/repositories"
	faqcontroller "github.com/materm-organization/materm-web-portal-backend/modules/faq/controller"
	faqroutes "github.com/materm-organization/materm-web-portal-backend/modules/faq/routes"
	faqservices "github.com/materm-organization/materm-web-portal-backend/modules/faq/services"
	formscontroller "github.com/materm-organization/materm-web-portal-backend/modules/forms/controller"
	formsrepositories "github.com/materm-organization/materm-web-portal-backend/modules/forms/repositories"
	formsroutes "github.com/materm-organization/materm-web-portal-backend/modules/forms/routes"
	formsservices "github.com/materm-organization/materm-web-portal-backend/modules/forms/services"
	gallerycontroller "github.com/materm-organization/materm-web-portal-backend/modules/gallery/controller"
	galleryroutes "github.com/materm-organization/materm-web-portal-backend/modules/gallery/routes"
	galleryservices "github.com/materm-organization/materm-web-portal-backend/modules/gallery/services"
	"github.com/materm-organization/materm-web-portal-backend/pkg/iphash"
	"github.com/materm-organization/materm-web-portal-backend/pkg/localize"
	"github.com/materm-organization/materm-web-portal-backend/pkg/media"
	"github.com/materm-organization/materm-web-portal-backend/pkg/publiccache"
	"github.com/materm-organization/materm-web-portal-backend/pkg/ratelimit"
	"github.com/materm-organization/materm-web-portal-backend/pkg/rediskeys"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/portalapp"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/redistest"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/schemadb"
)

// The scratch schema is built once for the package (a few seconds) and dropped here.
func TestMain(m *testing.M) {
	code := m.Run()
	schemadb.Teardown()
	os.Exit(code)
}

// env is one wired application over the shared schema and a private Redis prefix.
type env struct {
	app    *portalapp.App
	s      *schemadb.Schema
	rds    *redistest.Redis
	cache  *publiccache.Cache
	keys   rediskeys.Keys
	redis  redis.Cmdable // what the modules use (the scratch Redis, or a dead one)
	repo   formsrepositories.SubmissionRepository
	chain  *localize.Provider
	opened time.Time
}

type wireOpts struct {
	// DeadRedis points every Redis use (cache, rate limit, salt) at an address nobody listens on: the production
	// failure mode, as opposed to a nil client.
	DeadRedis bool
	Forms     formsservices.Options
}

func wire(t *testing.T, o ...wireOpts) *env {
	t.Helper()
	var opt wireOpts
	if len(o) > 0 {
		opt = o[0]
	}
	s := schemadb.Shared(t)
	r := redistest.New(t)
	e := &env{s: s, rds: r, keys: rediskeys.New(r.Prefix), opened: time.Now()}

	rc := r.Client
	if opt.DeadRedis {
		rc = redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 150 * time.Millisecond, ReadTimeout: 150 * time.Millisecond, MaxRetries: -1})
		t.Cleanup(func() { _ = rc.Close() })
	}
	e.redis = rc
	e.cache = publiccache.New(publiccache.Options{Client: rc, Keys: e.keys, Enabled: true, TTL: time.Minute})

	db := s.GormRO(t)
	content := repositories.New(db)
	mediaBuilder := media.NewBuilder(media.Config{PathIP: "https://files.example.test", StaticPrefix: "static/"})
	lookup := repositories.NewMediaLookup(content.Assets)
	e.chain = localize.NewProvider(func(ctx context.Context) ([]localize.Locale, error) {
		ls, err := content.Locales.ListActive(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]localize.Locale, 0, len(ls))
		for _, l := range ls {
			fb := ""
			if l.FallbackCode != nil {
				fb = *l.FallbackCode
			}
			out = append(out, localize.Locale{Code: l.Code, FallbackCode: fb, IsDefault: l.IsDefault})
		}
		return out, nil
	}, 0)

	e.app = portalapp.New()
	// ---- the wiring lines of the integrator (T1.2.12) ------------------------------------------------------------
	blogroutes.Register(e.app.API, blogcontroller.NewBlogCtrl(blogservices.NewBlogService(blogservices.Deps{
		Content: content, Media: mediaBuilder, Lookup: lookup, Cache: e.cache, Locales: e.chain, DefaultBaseURL: "https://fallback.example.test",
	}), e.chain))
	galleryroutes.Register(e.app.API, gallerycontroller.NewGalleryCtrl(galleryservices.NewGalleryService(galleryservices.Deps{
		Content: content, Media: mediaBuilder, Lookup: lookup, Cache: e.cache,
	}), e.chain))
	faqroutes.Register(e.app.API, faqcontroller.NewFaqCtrl(faqservices.NewFaqService(faqservices.Deps{
		Content: content, Cache: e.cache,
	}), e.chain))
	e.repo = formsrepositories.New(db)
	formsroutes.Register(e.app.API, formscontroller.NewFormCtrl(formsservices.NewFormService(formsservices.Deps{
		Forms: content.Forms, Submitter: e.repo, Cache: e.cache, Hasher: iphash.New(rc, e.keys),
		Limiter: ratelimit.NewRedis(rc), Redis: rc, Keys: e.keys, Options: opt.Forms,
	}), e.chain))
	return e
}

// ---- JSON helpers ----------------------------------------------------------------------------------------------

type obj = map[string]any

func decode(t *testing.T, b []byte) obj {
	t.Helper()
	var m obj
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, b)
	}
	return m
}

// at reads a value by a dotted path ("data.items.0.id"); it returns nil when something is missing.
func at(v any, path string) any {
	for _, p := range strings.Split(path, ".") {
		switch t := v.(type) {
		case obj:
			v = t[p]
		case []any:
			var i int
			if _, err := fmt.Sscanf(p, "%d", &i); err != nil || i < 0 || i >= len(t) {
				return nil
			}
			v = t[i]
		default:
			return nil
		}
	}
	return v
}

func str(v any) string { s, _ := v.(string); return s }

func num(v any) int64 { f, _ := v.(float64); return int64(f) }

func length(v any) int { a, _ := v.([]any); return len(a) }

// ---- owner access (writes the fixtures the read tests need to hide or to add) -------------------------------------

// ownerExec runs statements as the schema owner on ONE session with cms.live_write on (the live-row guard
// otherwise refuses direct edits of published content), and registers nothing: pair it with undo statements.
func ownerExec(t *testing.T, s *schemadb.Schema, stmts ...string) {
	t.Helper()
	ctx := context.Background()
	conn, err := s.Owner.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `set cms.live_write = 'on'`); err != nil {
		t.Fatal(err)
	}
	for _, q := range stmts {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			t.Fatalf("owner statement failed: %v\n%s", err, q)
		}
	}
}

// ownerScalar runs a query as the owner and scans one value.
func ownerScalar(t *testing.T, s *schemadb.Schema, dest any, q string, args ...any) {
	t.Helper()
	if err := s.Owner.QueryRowContext(context.Background(), q, args...).Scan(dest); err != nil {
		t.Fatalf("owner query failed: %v\n%s", err, q)
	}
}

// members lists the cache keys registered under a public tag.
func members(t *testing.T, e *env, tag string) []string {
	t.Helper()
	m, err := e.cache.Members(context.Background(), tag)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
