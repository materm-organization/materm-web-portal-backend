//go:build dbtest

package application

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/materm-organization/materm-web-portal-backend/bootstrap"
	"github.com/materm-organization/materm-web-portal-backend/pkg/portalkey"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/dbtest"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/redistest"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/schemadb"
)

func scratchEnv(t *testing.T, d *dbtest.Database, envName string) *bootstrap.Env {
	t.Helper()
	u, err := url.Parse(d.DSN)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(u.Port())
	pw, _ := u.User.Password()
	e := &bootstrap.Env{
		Server:   bootstrap.ServerConfig{Port: 3000, Env: envName},
		Database: bootstrap.DatabaseConfig{Admin: bootstrap.DBConnection{Host: u.Hostname(), Port: port, User: u.User.Username(), Password: pw, DBName: d.Name, SSLMode: "disable"}},
		Portal:   bootstrap.PortalConfig{FrontendKeys: []string{testKey}},
	}
	e.Defaults()
	return e
}

// roEnv is the configuration of an application that connects with dsn (the read-only role of schemadb).
func roEnv(t *testing.T, dsn, envName string) *bootstrap.Env {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(u.Port())
	pw, _ := u.User.Password()
	e := &bootstrap.Env{
		Server:   bootstrap.ServerConfig{Port: 3000, Env: envName},
		Database: bootstrap.DatabaseConfig{Admin: bootstrap.DBConnection{Host: u.Hostname(), Port: port, User: u.User.Username(), Password: pw, DBName: strings.TrimPrefix(u.Path, "/"), SSLMode: "disable"}},
		Portal:   bootstrap.PortalConfig{FrontendKeys: []string{testKey}},
		Site:     bootstrap.SiteConfig{DefaultBaseURL: "https://materm.example.test"},
	}
	e.Defaults()
	return e
}

// Boot the REAL wiring (Setup) on the scratch cluster with the full schema and the read-only role: /ready is
// ok with Redis and 200 degraded without it, and the key still guards /api/v1. (/ready reads v_public_locales,
// so an empty database - TestBootCreatesNothing - is "down".)
func TestBootOnScratch(t *testing.T) {
	s := schemadb.Shared(t)
	r := redistest.New(t)
	ctx := context.Background()

	for name, tc := range map[string]struct {
		redis bool
		want  string
	}{"redis up": {true, "ok"}, "redis not connected": {false, "degraded"}} {
		t.Run(name, func(t *testing.T) {
			env := roEnv(t, s.RODSN, bootstrap.EnvDev)
			app := &bootstrap.Application{Env: env, Fiber: bootstrap.NewFiber(env.Server)}
			if tc.redis {
				app.Redis = r.Client
			}
			rt, err := Setup(ctx, app)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(rt.Close)

			status, body := req(t, rt.Fiber, "GET", "/ready", nil)
			data, _ := body["data"].(map[string]any)
			if status != 200 || data["status"] != tc.want {
				t.Errorf("/ready: %d %v, want %s", status, body, tc.want)
			}
			if status, _ = req(t, rt.Fiber, "GET", "/api/v1/x", nil); status != 401 {
				t.Errorf("/api/v1 without key: %d", status)
			}
			if status, _ = req(t, rt.Fiber, "GET", "/api/v1/x", map[string]string{portalkey.Header: testKey}); status != 404 {
				t.Errorf("/api/v1 with key: %d", status)
			}
		})
	}
}

// An empty database has no v_public_locales: the database check of /ready fails, 503 {status: down}, and
// liveness is untouched.
func TestReadyIsDownWithoutThePublicViews(t *testing.T) {
	d := dbtest.New(t)
	env := scratchEnv(t, d, bootstrap.EnvDev)
	app := &bootstrap.Application{Env: env, Fiber: bootstrap.NewFiber(env.Server)}
	rt, err := Setup(context.Background(), app)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Close)
	status, body := req(t, rt.Fiber, "GET", "/ready", nil)
	if data, _ := body["data"].(map[string]any); status != 503 || data["status"] != "down" {
		t.Errorf("/ready on an empty database: %d %v, want 503 down", status, body)
	}
	if status, _ = req(t, rt.Fiber, "GET", "/health", nil); status != 200 {
		t.Errorf("/health: %d", status)
	}
}

// The 16 endpoints through the real Setup (real repositories on the read-only role, real services, the scratch
// Redis for cache, rate limits and the IP salt): each answers what the fixture says, with and without the key.
func TestBootServesTheSixteenEndpoints(t *testing.T) {
	s := schemadb.Shared(t)
	r := redistest.New(t)
	// Setup names its Redis keys under the production prefix (materm-portal:), not under redistest's random one:
	// the response cache is OFF here so that no answer of another run's database is served from the scratch
	// Redis, and the submission below uses an address and a message nobody used before (the rate limit and the
	// duplicate marker live under that prefix for minutes). tests/readapi covers the cache on a private prefix.
	env := roEnv(t, s.RODSN, bootstrap.EnvDev)
	env.Cache.Enabled = false
	app := &bootstrap.Application{Env: env, Fiber: bootstrap.NewFiber(env.Server), Redis: r.Client}
	rt, err := Setup(context.Background(), app)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Close)
	nonce := time.Now().UnixNano()

	key := map[string]string{portalkey.Header: testKey}
	for _, c := range []struct {
		path string
		want int
	}{
		{"/api/v1/public/locales", 200},
		{"/api/v1/public/settings?locale=en", 200},
		{"/api/v1/public/ui-strings?locale=en&ns=site", 200},
		{"/api/v1/public/menus/main?locale=en", 200},
		{"/api/v1/public/site?locale=en", 200},
		{"/api/v1/public/pages/resolve?path=/&locale=en", 200},
		{"/api/v1/public/posts?locale=en", 200},
		{"/api/v1/public/posts/music-night?locale=en", 200},
		{"/api/v1/public/categories?locale=en", 200},
		{"/api/v1/public/tags?locale=en", 200},
		{"/api/v1/public/albums?locale=en", 200},
		{"/api/v1/public/albums/forest?locale=en", 200},
		{"/api/v1/public/faq/contact?locale=en", 200},
		{"/api/v1/public/forms/contact?locale=en", 200},
		{"/api/v1/public/sitemap", 200},
	} {
		if status, _ := req(t, rt.Fiber, "GET", c.path, nil); status != 401 {
			t.Errorf("%s without a key: %d, want 401", c.path, status)
		}
		if status, body := req(t, rt.Fiber, "GET", c.path, key); status != c.want {
			t.Errorf("%s: %d %v, want %d", c.path, status, body, c.want)
		}
	}
	// P-15: a valid submission is stored through forms_submit_form (the one write the read-only role may do)
	body := fmt.Sprintf(`{"locale":"en","source_path":"/contact","data":{"name":"Noy","phone":"","email":"noy@example.test","topic":"other","message":"Hello from the boot test %d."},"honeypot":"","elapsed_ms":9000}`, nonce)
	r2 := httptest.NewRequest("POST", "/api/v1/public/forms/contact/submissions", strings.NewReader(body))
	r2.Header.Set(portalkey.Header, testKey)
	r2.Header.Set("Content-Type", "application/json")
	r2.Header.Set("X-Client-IP", fmt.Sprintf("198.51.100.%d", nonce%250+1))
	resp, err := rt.Fiber.Test(r2, -1)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), "received") {
		t.Errorf("POST submission: %d %s", resp.StatusCode, b)
	}
	var stored int64
	if err := s.Owner.QueryRowContext(context.Background(), `select count(*) from forms_form_submissions where data::text like '%' || $1 || '%'`, strconv.FormatInt(nonce, 10)).Scan(&stored); err != nil || stored < 1 {
		t.Errorf("the submission was not stored: %d %v", stored, err)
	}
	if resp := mustDo(t, rt.Fiber, httptest.NewRequest("POST", "/api/v1/public/forms/contact/submissions", strings.NewReader(body))); resp != 401 {
		t.Errorf("POST without a key: %d, want 401", resp)
	}
}

func mustDo(t *testing.T, app *fiber.App, r *http.Request) int {
	t.Helper()
	resp, err := app.Test(r, -1)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// Outside dev the scratch superuser is not an acceptable runtime role: Setup refuses and leaves nothing open.
func TestBootRefusesSuperuserOutsideDev(t *testing.T) {
	d := dbtest.New(t)
	env := scratchEnv(t, d, bootstrap.EnvProd)
	app := &bootstrap.Application{Env: env, Fiber: bootstrap.NewFiber(env.Server)}
	rt, err := Setup(context.Background(), app)
	if err == nil {
		rt.Close()
		t.Fatal("Setup must refuse a superuser role in prod")
	}
	if !strings.Contains(err.Error(), "database role") {
		t.Errorf("unexpected error: %v", err)
	}
}

// The portal does not own the schema: booting against an empty database creates nothing.
func TestBootCreatesNothing(t *testing.T) {
	d := dbtest.New(t)
	env := scratchEnv(t, d, bootstrap.EnvDev)
	app := &bootstrap.Application{Env: env, Fiber: bootstrap.NewFiber(env.Server)}
	rt, err := Setup(context.Background(), app)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	var n int
	if err = d.SQL.QueryRowContext(context.Background(),
		`select count(*) from pg_class c join pg_namespace n on n.oid = c.relnamespace where n.nspname = 'public'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("the portal created %d relations in public; it must never migrate", n)
	}
}
