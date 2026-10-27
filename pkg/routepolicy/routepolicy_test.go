package routepolicy

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/materm-organization/materm-web-portal-backend/pkg/apperror"
	"github.com/materm-organization/materm-web-portal-backend/pkg/portalkey"
)

const testKey = "routepolicy-test-key-0123456789abcdef"

func build(t *testing.T) (*fiber.App, *Registry, *Group, *Group) {
	t.Helper()
	app := fiber.New(fiber.Config{ErrorHandler: apperror.Handler})
	reg := NewRegistry()
	root := NewRoot(app, reg)
	api := NewAPI(app, "/api/v1", portalkey.New([]string{testKey}).Middleware(), reg)
	ok := func(c *fiber.Ctx) error { return c.SendString("ok") }
	root.Get("/health", Public(), ok)
	api.Group("/public").Get("/site", PortalKey(), ok)
	api.Post("/forms", PortalKey(), ok)
	return app, reg, root, api
}

func status(app *fiber.App, method, path string, key bool) int {
	req := httptest.NewRequest(method, path, nil)
	if key {
		req.Header.Set(portalkey.Header, testKey)
	}
	resp, err := app.Test(req, -1)
	if err != nil {
		panic(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestRegistry(t *testing.T) {
	_, reg, _, _ := build(t)
	e, ok := reg.Lookup("GET", "/api/v1/public/site")
	if !ok || e.Policy.Kind != KindPortalKey {
		t.Fatalf("lookup: %+v %v", e, ok)
	}
	if e, ok = reg.Lookup("GET", "/health"); !ok || e.Policy.Kind != KindPublic {
		t.Fatalf("health: %+v", e)
	}
	if len(reg.Entries()) != 3 {
		t.Errorf("entries: %d\n%s", len(reg.Entries()), reg.Describe())
	}
	if Public().String() != "-" || PortalKey().String() != "portal-key" {
		t.Error("policy notation")
	}
}

// The coverage test: a route added with a raw app.Get is reported; HEAD derived from a GET is covered.
func TestUncoveredRoutes(t *testing.T) {
	app, reg, _, _ := build(t)
	if got := UncoveredRoutes(app, reg); len(got) != 0 {
		t.Fatalf("all routes were registered through a Group, got uncovered: %v", got)
	}
	app.Get("/sneaky", func(c *fiber.Ctx) error { return c.SendString("x") })
	got := UncoveredRoutes(app, reg)
	if len(got) == 0 || !strings.Contains(strings.Join(got, ","), "GET /sneaky") {
		t.Errorf("a raw route must be reported, got %v", got)
	}
}

// What a route declares is what is enforced: every PortalKey route is 401 without the key and reachable with
// it; the Public routes need nothing. Unknown paths below the API prefix are 401 without the key (no route
// enumeration) and 404 with it.
func TestPolicyIsEnforced(t *testing.T) {
	app, reg, _, _ := build(t)
	for _, e := range reg.Entries() {
		path := strings.NewReplacer(":id", "1").Replace(e.Path)
		without, with := status(app, e.Method, path, false), status(app, e.Method, path, true)
		switch e.Policy.Kind {
		case KindPortalKey:
			if without != 401 || with != 200 {
				t.Errorf("%s %s: without key %d, with key %d (want 401 / 200)", e.Method, e.Path, without, with)
			}
		case KindPublic:
			if without != 200 {
				t.Errorf("%s %s: public route answered %d", e.Method, e.Path, without)
			}
		}
	}
	if got := status(app, "GET", "/api/v1/no/such/route", false); got != 401 {
		t.Errorf("unknown API path without key: %d, want 401", got)
	}
	if got := status(app, "GET", "/api/v1/no/such/route", true); got != 404 {
		t.Errorf("unknown API path with key: %d, want 404", got)
	}
	if got := status(app, "GET", "/no/such/route", false); got != 404 {
		t.Errorf("unknown root path: %d, want 404", got)
	}
}

func TestMismatchedPolicyPanics(t *testing.T) {
	_, _, root, api := build(t)
	ok := func(c *fiber.Ctx) error { return nil }
	mustPanic := func(name string, f func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s: expected a panic", name)
			}
		}()
		f()
	}
	mustPanic("public inside the API group", func() { api.Get("/open", Public(), ok) })
	mustPanic("portal key at the root", func() { root.Get("/keyed", PortalKey(), ok) })
	mustPanic("no handler", func() { api.Get("/nohandler", PortalKey()) })
	mustPanic("API group without a guard", func() { NewAPI(fiber.New(), "/api/v1", nil, NewRegistry()) })
}
