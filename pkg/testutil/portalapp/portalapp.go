// Package portalapp builds, for tests, the same HTTP pipeline the server runs - bootstrap.NewFiber (request
// id, access log, recover, security headers, 3 s request deadline, apperror.Handler) with the portal key guard
// mounted on /api/v1 through a routepolicy group - so that a module's routes are exercised exactly as the
// integrator wires them (modules register on App.API with routes.Register). It only builds an in-memory
// *fiber.App; it opens no socket, no database and no Redis.
package portalapp

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/materm-organization/materm-web-portal-backend/bootstrap"
	"github.com/materm-organization/materm-web-portal-backend/pkg/constants"
	"github.com/materm-organization/materm-web-portal-backend/pkg/portalkey"
	"github.com/materm-organization/materm-web-portal-backend/pkg/routepolicy"
)

// Key is the portal key the helper accepts (>= 32 printable bytes, like a real one).
const Key = "portalapp-test-key-0123456789abcdef"

// App is a test server.
type App struct {
	Fiber *fiber.App
	// API is the /api/v1 group behind the portal key guard: pass it to a module's routes.Register.
	API      *routepolicy.Group
	Registry *routepolicy.Registry
}

// New builds the pipeline with the production Fiber configuration and an empty API group.
func New() *App {
	app := bootstrap.NewFiber(bootstrap.ServerConfig{})
	reg := routepolicy.NewRegistry()
	api := routepolicy.NewAPI(app, constants.MainRoute, portalkey.New([]string{Key}).Middleware(), reg)
	return &App{Fiber: app, API: api, Registry: reg}
}

// Request describes one call; the zero Header sends the portal key.
type Request struct {
	Method string
	Path   string
	Header map[string]string
	Body   []byte
	// NoKey omits X-Portal-Key.
	NoKey bool
}

// Response is what came back.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Do sends r through the app (no network).
func (a *App) Do(t testing.TB, r Request) Response {
	t.Helper()
	method := r.Method
	if method == "" {
		method = http.MethodGet
	}
	req := httptest.NewRequest(method, r.Path, bytes.NewReader(r.Body))
	if !r.NoKey {
		req.Header.Set(portalkey.Header, Key)
	}
	for k, v := range r.Header {
		req.Header.Set(k, v)
	}
	resp, err := a.Fiber.Test(req, -1)
	if err != nil {
		t.Fatalf("%s %s: %v", method, r.Path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return Response{Status: resp.StatusCode, Header: resp.Header, Body: b}
}

// Get is Do for a GET with extra headers.
func (a *App) Get(t testing.TB, path string, header map[string]string) Response {
	t.Helper()
	return a.Do(t, Request{Method: http.MethodGet, Path: path, Header: header})
}
