package application

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/materm-organization/materm-web-portal-backend/bootstrap"
	systemservices "github.com/materm-organization/materm-web-portal-backend/modules/system/services"
	"github.com/materm-organization/materm-web-portal-backend/pkg/localize"
	"github.com/materm-organization/materm-web-portal-backend/pkg/metrics"
	"github.com/materm-organization/materm-web-portal-backend/pkg/portalkey"
	"github.com/materm-organization/materm-web-portal-backend/pkg/routepolicy"
)

const testKey = "application-test-key-0123456789abcdef"

func up(context.Context) error { return nil }

// wire builds the real pipeline (bootstrap.NewFiber) and the real router over fake dependencies.
func wire(t *testing.T, keys []string, redisUp bool) (*fiber.App, *routepolicy.Registry, *routepolicy.Group) {
	t.Helper()
	app := bootstrap.NewFiber(bootstrap.ServerConfig{})
	var rp func(context.Context) error
	if redisUp {
		rp = up
	}
	svc := stubServices()
	svc.System = systemservices.NewSystemService(systemservices.Deps{PingDB: up, PingRedis: rp, Strict: true})
	svc.Locales = localize.NewProvider(func(context.Context) ([]localize.Locale, error) {
		return []localize.Locale{{Code: "la", IsDefault: true}, {Code: "en", FallbackCode: "la"}}, nil
	}, localize.DefaultProviderTTL)
	reg, api := SetupRouter(app, SetupControllers(svc), portalkey.New(keys))
	return app, reg, api
}

func req(t *testing.T, app *fiber.App, method, path string, headers map[string]string) (int, map[string]any) {
	t.Helper()
	r := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	resp, err := app.Test(r, -1)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	var body map[string]any
	_ = json.Unmarshal(b, &body)
	return resp.StatusCode, body
}

// The route-policy coverage test (NFR-12): every route of the Fiber table is in the registry, i.e. went
// through a routepolicy.Group and so declared its policy.
func TestEveryRouteDeclaresAPolicy(t *testing.T) {
	app, reg, _ := wire(t, []string{testKey}, true)
	if got := routepolicy.UncoveredRoutes(app, reg); len(got) != 0 {
		t.Fatalf("routes without a declared policy: %v\nregistry:\n%s", got, reg.Describe())
	}
	if _, ok := reg.Lookup("GET", "/health"); !ok {
		t.Fatalf("the probes must be registered:\n%s", reg.Describe())
	}
}

// Whatever the registry says is what the server does, for every route the application registers (it grows
// with each module, so this test needs no edit when a module is added).
func TestRegisteredPoliciesAreEnforced(t *testing.T) {
	app, reg, _ := wire(t, []string{testKey}, true)
	for _, e := range reg.Entries() {
		path := strings.NewReplacer(":id", "1", ":slug", "x", ":key", "x", ":group_key", "x", ":code", "en").Replace(e.Path)
		without, _ := req(t, app, e.Method, path, nil)
		wrong, _ := req(t, app, e.Method, path, map[string]string{portalkey.Header: "wrong"})
		switch e.Policy.Kind {
		case routepolicy.KindPortalKey:
			if without != 401 || wrong != 401 {
				t.Errorf("%s %s: without key %d, wrong key %d, want 401", e.Method, e.Path, without, wrong)
			}
			if with, _ := req(t, app, e.Method, path, map[string]string{portalkey.Header: testKey}); with == 401 {
				t.Errorf("%s %s: refused a valid key", e.Method, e.Path)
			}
		case routepolicy.KindPublic:
			if without == 401 {
				t.Errorf("%s %s: public route demanded a key", e.Method, e.Path)
			}
		}
		if strings.HasPrefix(e.Path, "/api/v1") && e.Policy.Kind != routepolicy.KindPortalKey {
			t.Errorf("%s %s: a route below /api/v1 must be PortalKey", e.Method, e.Path)
		}
	}
}

func TestProbesNeedNoKey(t *testing.T) {
	app, _, _ := wire(t, []string{testKey}, true)
	for _, p := range []string{"/health", "/healthz", "/ready", "/readyz"} {
		if status, body := req(t, app, "GET", p, nil); status != 200 || body["success"] != true {
			t.Errorf("%s: %d %v", p, status, body)
		}
	}
	// Redis down in a strict deployment: /ready is 200 degraded (the cache is optional; doc/08 section 2.7 item 5),
	// liveness is untouched
	app, _, _ = wire(t, []string{testKey}, false)
	if status, body := req(t, app, "GET", "/ready", nil); status != 200 || body["data"].(map[string]any)["status"] != "degraded" {
		t.Errorf("/ready without Redis in strict mode: %d %v, want 200 degraded", status, body)
	}
	if status, _ := req(t, app, "GET", "/health", nil); status != 200 {
		t.Errorf("/health: %d", status)
	}
}

// Everything under /api/v1 is behind the key, including paths that match no route: 401 without it, a
// standard 404 with it.
func TestAPIPrefixIsGuarded(t *testing.T) {
	app, _, _ := wire(t, []string{testKey}, true)
	status, body := req(t, app, "GET", "/api/v1/anything/at/all", nil)
	if status != 401 || body["code"] != "UNAUTHENTICATED" || body["request_id"] == "" {
		t.Errorf("no key: %d %v", status, body)
	}
	status, body = req(t, app, "GET", "/api/v1/anything/at/all", map[string]string{portalkey.Header: testKey})
	if status != 404 || body["code"] != "NOT_FOUND" {
		t.Errorf("with key: %d %v", status, body)
	}
	// outside the prefix no key is needed to learn that a path does not exist
	if status, _ = req(t, app, "GET", "/elsewhere", nil); status != 404 {
		t.Errorf("/elsewhere: %d", status)
	}
}

// Fail closed: without any configured key (dev with PORTAL_FRONTENDKEYS blank) the API refuses everyone.
func TestNoConfiguredKeyRefusesEveryone(t *testing.T) {
	app, _, api := wire(t, nil, true)
	api.Get("/ping", routepolicy.PortalKey(), func(c *fiber.Ctx) error { return c.SendString("pong") })
	for _, h := range []map[string]string{nil, {portalkey.Header: ""}, {portalkey.Header: testKey}} {
		if status, _ := req(t, app, "GET", "/api/v1/ping", h); status != 401 {
			t.Errorf("headers %v: %d, want 401", h, status)
		}
	}
}

// A module route registered on the API group is protected without the module doing anything.
func TestModuleRouteOnAPIGroup(t *testing.T) {
	app, reg, api := wire(t, []string{testKey}, true)
	api.Group("/public").Get("/ping", routepolicy.PortalKey(), func(c *fiber.Ctx) error { return c.SendString("pong") })
	if status, _ := req(t, app, "GET", "/api/v1/public/ping", nil); status != 401 {
		t.Errorf("no key: %d", status)
	}
	if status, _ := req(t, app, "GET", "/api/v1/public/ping", map[string]string{portalkey.Header: testKey}); status != 200 {
		t.Errorf("with key: %d", status)
	}
	if _, ok := reg.Lookup("GET", "/api/v1/public/ping"); !ok {
		t.Error("the route is not in the registry")
	}
}

// The 17 endpoints (P-01 ... P-16 of milestone M2 and P-19 preview of M4) are all mounted, each with the PortalKey
// policy. The list is the contract: a route that disappears (or one that appears without being added here and
// to the README table) fails this test.
func TestAllPublicEndpointsAreMounted(t *testing.T) {
	_, reg, _ := wire(t, []string{testKey}, true)
	want := []string{
		"GET /api/v1/public/locales", "GET /api/v1/public/settings", "GET /api/v1/public/ui-strings",
		"GET /api/v1/public/menus/:key", "GET /api/v1/public/site", "GET /api/v1/public/pages/resolve",
		"GET /api/v1/public/posts", "GET /api/v1/public/posts/:slug", "GET /api/v1/public/categories", "GET /api/v1/public/tags",
		"GET /api/v1/public/albums", "GET /api/v1/public/albums/:key", "GET /api/v1/public/faq/:group_key",
		"GET /api/v1/public/forms/:slug", "POST /api/v1/public/forms/:slug/submissions", "GET /api/v1/public/sitemap",
		"GET /api/v1/preview/resolve",
	}
	got := map[string]bool{}
	for _, e := range reg.Entries() {
		if strings.HasPrefix(e.Path, "/api/v1") {
			got[e.Method+" "+e.Path] = true
			if e.Policy.Kind != routepolicy.KindPortalKey {
				t.Errorf("%s %s: %v, want PortalKey", e.Method, e.Path, e.Policy.Kind)
			}
		}
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("route %s is not mounted", w)
		}
		delete(got, w)
	}
	for extra := range got {
		t.Errorf("route %s is mounted but not in the expected list", extra)
	}
}

// /metrics is never part of the public application (Q-B10): not without the key, not with it. The metrics
// listener is a separate app on a separate port (pkg/metrics.NewApp), and what the public app served shows up
// there by route template.
func TestMetricsAreNotOnThePublicListener(t *testing.T) {
	app, _, _ := wire(t, []string{testKey}, true)
	for _, h := range []map[string]string{nil, {portalkey.Header: testKey}} {
		if code, _ := req(t, app, "GET", "/metrics", h); code != 404 {
			t.Errorf("public GET /metrics (key=%v) = %d, want 404", h != nil, code)
		}
		if code, _ := req(t, app, "GET", "/api/v1/metrics", h); code == 200 {
			t.Errorf("public GET /api/v1/metrics (key=%v) must not exist", h != nil)
		}
	}
	if code, _ := req(t, app, "GET", "/health", nil); code != 200 {
		t.Fatalf("health %d", code)
	}
	if code, _ := req(t, app, "GET", "/api/v1/public/site", nil); code != 401 {
		t.Fatalf("site without key %d", code)
	}

	mr, err := metrics.NewApp(metrics.Default).Test(httptest.NewRequest("GET", "/metrics", nil), -1)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(mr.Body)
	_ = mr.Body.Close()
	out := string(b)
	for _, want := range []string{
		`materm_portal_http_requests_total{method="GET",route="/health",status_class="2xx"}`,
		`materm_portal_http_requests_total{method="GET",route="/api/v1/public/site",status_class="4xx"}`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("metrics output lacks %s", want)
		}
	}
	if strings.Contains(out, testKey) {
		t.Error("the portal key must never appear in the metrics")
	}
}
