// Package apitest builds a small Fiber app for the API tests of the content modules, wired exactly as
// cmd/application wires production: the real middleware pipeline (bootstrap.NewFiber: request id, access log,
// recover, helmet, DB deadline, apperror.Handler) and the /api/v1 group behind the portal key guard
// (routepolicy.NewAPI + portalkey). A module under test registers itself on the group through its own
// routes.Register, the same call the integrator makes in cmd/application/router.go.
package apitest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/materm-organization/materm-web-portal-backend/bootstrap"
	"github.com/materm-organization/materm-web-portal-backend/pkg/constants"
	"github.com/materm-organization/materm-web-portal-backend/pkg/portalkey"
	"github.com/materm-organization/materm-web-portal-backend/pkg/routepolicy"
)

// Key is the portal key the test app accepts.
const Key = "apitest-portal-key-0123456789abcdef0123"

// App is the app under test.
type App struct {
	Fiber *fiber.App
	Reg   *routepolicy.Registry
	API   *routepolicy.Group
}

// New builds the app and lets register mount the routes of the module(s) under test on the API group.
func New(register func(api *routepolicy.Group)) *App {
	app := bootstrap.NewFiber(bootstrap.ServerConfig{})
	reg := routepolicy.NewRegistry()
	api := routepolicy.NewAPI(app, constants.MainRoute, portalkey.New([]string{Key}).Middleware(), reg)
	register(api)
	return &App{Fiber: app, Reg: reg, API: api}
}

// Response is a recorded answer.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// JSON decodes the body into a generic map (nil when it is not a JSON object).
func (r *Response) JSON() map[string]any {
	var m map[string]any
	_ = json.Unmarshal(r.Body, &m)
	return m
}

// Data returns body.data as an object.
func (r *Response) Data() map[string]any {
	m, _ := r.JSON()["data"].(map[string]any)
	return m
}

// Code returns the machine code of an error body.
func (r *Response) Code() string {
	s, _ := r.JSON()["code"].(string)
	return s
}

// Do sends a request with the portal key. headers are "Name: value" pairs; "X-Portal-Key: -" omits the key.
func (a *App) Do(t testing.TB, method, target string, headers ...string) *Response {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	withKey := true
	for _, h := range headers {
		name, value, _ := strings.Cut(h, ":")
		value = strings.TrimSpace(value)
		if strings.EqualFold(name, "X-Portal-Key") && value == "-" {
			withKey = false
			continue
		}
		req.Header.Set(name, value)
	}
	if withKey && req.Header.Get("X-Portal-Key") == "" {
		req.Header.Set("X-Portal-Key", Key)
	}
	resp, err := a.Fiber.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return &Response{Status: resp.StatusCode, Header: resp.Header, Body: b}
}

// Get is Do("GET", ...).
func (a *App) Get(t testing.TB, target string, headers ...string) *Response {
	t.Helper()
	return a.Do(t, http.MethodGet, target, headers...)
}

// AssertEveryRouteDeclaresAPolicy is the coverage test of NFR-12 for the routes registered by the module under
// test: every Fiber route is in the registry, every registered route answers 401 without the portal key and
// something else with it.
func (a *App) AssertEveryRouteDeclaresAPolicy(t testing.TB) {
	t.Helper()
	if got := routepolicy.UncoveredRoutes(a.Fiber, a.Reg); len(got) != 0 {
		t.Fatalf("routes without a declared policy: %v\nregistry:\n%s", got, a.Reg.Describe())
	}
	entries := a.Reg.Entries()
	if len(entries) == 0 {
		t.Fatal("no route registered")
	}
	for _, e := range entries {
		if e.Policy.Kind != routepolicy.KindPortalKey {
			t.Errorf("%s %s: policy %s, want portal-key", e.Method, e.Path, e.Policy)
			continue
		}
		path := strings.NewReplacer(":key", "x", ":slug", "x", ":id", "1").Replace(e.Path)
		if r := a.Do(t, e.Method, path, "X-Portal-Key: -"); r.Status != http.StatusUnauthorized {
			t.Errorf("%s %s without a key: %d, want 401", e.Method, e.Path, r.Status)
		}
		if r := a.Do(t, e.Method, path); r.Status == http.StatusUnauthorized {
			t.Errorf("%s %s with the key: 401", e.Method, e.Path)
		}
	}
}
