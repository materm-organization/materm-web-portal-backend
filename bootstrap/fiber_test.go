package bootstrap

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

func testApp(t *testing.T, cfg ServerConfig) *fiber.App {
	t.Helper()
	app := NewFiber(cfg)
	app.Get("/ok", func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"success": true, "data": "x"}) })
	app.Get("/boom", func(*fiber.Ctx) error { panic("kaboom with internal detail") })
	app.Get("/fail", func(*fiber.Ctx) error { return io.ErrUnexpectedEOF })
	app.Post("/echo", func(c *fiber.Ctx) error { return c.SendString("ok") })
	return app
}

func withCORS() ServerConfig { return ServerConfig{CORSOrigins: []string{"http://localhost:3000"}} }

func do(t *testing.T, app *fiber.App, req *http.Request) (*http.Response, map[string]any, string) {
	t.Helper()
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	var body map[string]any
	_ = json.Unmarshal(b, &body)
	return resp, body, string(b)
}

func TestUnknownRouteIsAnEnvelope(t *testing.T) {
	resp, body, _ := do(t, testApp(t, withCORS()), httptest.NewRequest("GET", "/nope", nil))
	if resp.StatusCode != 404 || body["success"] != false || body["code"] != "NOT_FOUND" || body["message"] == "" || body["request_id"] == "" {
		t.Fatalf("404 envelope: %d %v", resp.StatusCode, body)
	}
	if resp.Header.Get("X-Request-Id") != body["request_id"] {
		t.Error("response header and body must carry the same request id")
	}
}

func TestPanicBecomesInternalErrorWithoutLeaking(t *testing.T) {
	app := testApp(t, withCORS())
	resp, body, raw := do(t, app, httptest.NewRequest("GET", "/boom", nil))
	if resp.StatusCode != 500 || body["code"] != "INTERNAL_ERROR" {
		t.Fatalf("panic: %d %v", resp.StatusCode, body)
	}
	if strings.Contains(raw, "kaboom") || strings.Contains(raw, "internal detail") {
		t.Errorf("panic text leaked to the client: %s", raw)
	}
	_, body, raw = do(t, app, httptest.NewRequest("GET", "/fail", nil))
	if body["code"] != "INTERNAL_ERROR" || strings.Contains(raw, "EOF") {
		t.Errorf("unknown error must be generic: %s", raw)
	}
}

func TestRequestID(t *testing.T) {
	app := testApp(t, withCORS())
	req := httptest.NewRequest("GET", "/ok", nil)
	req.Header.Set("X-Request-Id", "client-supplied-1234")
	resp, _, _ := do(t, app, req)
	if resp.Header.Get("X-Request-Id") != "client-supplied-1234" {
		t.Errorf("well-formed id must be kept, got %q", resp.Header.Get("X-Request-Id"))
	}
	for _, bad := range []string{"short", "has space in it 1234", "inject\nline-1234", strings.Repeat("a", 65), "<script>alert(1)</script>"} {
		req := httptest.NewRequest("GET", "/ok", nil)
		req.Header.Set("X-Request-Id", bad)
		r2, _, _ := do(t, app, req)
		got := r2.Header.Get("X-Request-Id")
		if got == bad || len(got) < 8 {
			t.Errorf("malformed id %q must be replaced, got %q", bad, got)
		}
	}
	resp, _, _ = do(t, app, httptest.NewRequest("GET", "/ok", nil))
	if len(resp.Header.Get("X-Request-Id")) < 32 {
		t.Error("an id must be generated when none is sent")
	}
}

func TestBodyLimit(t *testing.T) {
	// fasthttp rejects an oversized body while reading the request, which app.Test cannot show: use a
	// real loopback listener
	app := testApp(t, withCORS())
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = app.Listener(ln) }()
	t.Cleanup(func() { _ = app.Shutdown() })
	raw := func(contentLength int) string {
		conn, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		_, _ = fmt.Fprintf(conn, "POST /echo HTTP/1.1\r\nHost: x\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", contentLength)
		b, _ := io.ReadAll(conn)
		return string(b)
	}
	got := raw(MaxBodyBytes + 1)
	if !strings.HasPrefix(got, "HTTP/1.1 413") || !strings.Contains(got, `"code":"PAYLOAD_TOO_LARGE"`) || !strings.Contains(got, `"success":false`) {
		t.Fatalf("over the limit: %s", got)
	}
}

func TestCORSAllowList(t *testing.T) {
	app := testApp(t, withCORS())
	req := httptest.NewRequest("GET", "/ok", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	resp, _, _ := do(t, app, req)
	if resp.Header.Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
		t.Errorf("allowed origin not echoed: %q", resp.Header.Get("Access-Control-Allow-Origin"))
	}
	if resp.Header.Get("Access-Control-Allow-Credentials") != "" {
		t.Error("credentials must not be allowed")
	}
	if !strings.Contains(resp.Header.Get("Access-Control-Expose-Headers"), "X-Request-Id") {
		t.Error("X-Request-Id must be exposed")
	}
	req = httptest.NewRequest("GET", "/ok", nil)
	req.Header.Set("Origin", "https://evil.example")
	resp, _, _ = do(t, app, req)
	if v := resp.Header.Get("Access-Control-Allow-Origin"); v != "" {
		t.Errorf("foreign origin must not be allowed, got %q", v)
	}
	// preflight: GET/POST/OPTIONS only, the portal key is not an allowed header
	req = httptest.NewRequest("OPTIONS", "/api/v1/public/site", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", "GET")
	req.Header.Set("Access-Control-Request-Headers", "Content-Type,Accept-Language")
	resp, _, _ = do(t, app, req)
	if resp.StatusCode != 204 && resp.StatusCode != 200 {
		t.Errorf("preflight: %d", resp.StatusCode)
	}
	m := resp.Header.Get("Access-Control-Allow-Methods")
	if m != "GET,POST,OPTIONS" {
		t.Errorf("methods: %q", m)
	}
	h := resp.Header.Get("Access-Control-Allow-Headers")
	if strings.Contains(strings.ToLower(h), "x-portal-key") || strings.Contains(h, "*") {
		t.Errorf("allow headers must not include the portal key or a wildcard: %q", h)
	}
	if resp.Header.Get("Access-Control-Max-Age") != "600" {
		t.Errorf("max-age: %q", resp.Header.Get("Access-Control-Max-Age"))
	}
}

// Fiber's cors middleware turns an empty origin list into "*": with no configured origin it must not be
// mounted, so no response carries Access-Control-Allow-Origin, not even a preflight answer.
func TestEmptyCORSListMeansNoCORSAtAll(t *testing.T) {
	app := testApp(t, ServerConfig{})
	for _, method := range []string{"GET", "OPTIONS"} {
		req := httptest.NewRequest(method, "/ok", nil)
		req.Header.Set("Origin", "https://evil.example")
		req.Header.Set("Access-Control-Request-Method", "GET")
		resp, _, _ := do(t, app, req)
		for h := range resp.Header {
			if strings.HasPrefix(h, "Access-Control-") {
				t.Errorf("%s: unexpected CORS header %s=%q", method, h, resp.Header.Get(h))
			}
		}
	}
}

func TestSecurityHeaders(t *testing.T) {
	resp, _, _ := do(t, testApp(t, withCORS()), httptest.NewRequest("GET", "/ok", nil))
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("helmet headers are missing")
	}
}

func TestForwardingHeadersIgnoredWithoutTrustedProxies(t *testing.T) {
	app := NewFiber(ServerConfig{ProxyHeader: "X-Forwarded-For"})
	var ip string
	app.Get("/ip", func(c *fiber.Ctx) error { ip = c.IP(); return c.SendString(ip) })
	req := httptest.NewRequest("GET", "/ip", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.99")
	do(t, app, req)
	if ip == "203.0.113.99" {
		t.Error("X-Forwarded-For must be ignored when no trusted proxy is configured")
	}
}

func TestMethodNotAllowedUsesDocumentedStatus(t *testing.T) {
	resp, body, _ := do(t, testApp(t, withCORS()), httptest.NewRequest("DELETE", "/ok", nil))
	if resp.StatusCode != 405 || body["code"] != "METHOD_NOT_ALLOWED" {
		t.Errorf("405: %d %v", resp.StatusCode, body)
	}
}

func TestAccessLogRouteTemplate(t *testing.T) {
	app := NewFiber(ServerConfig{})
	var got string
	// evaluated after the chain ran, exactly like accessLog does
	app.Use(func(c *fiber.Ctx) error {
		err := c.Next()
		got = routeTemplate(c)
		return err
	})
	app.Get("/api/v1/public/pages/:id", func(c *fiber.Ctx) error { return c.SendString("x") })
	app.Get("/", func(c *fiber.Ctx) error { return c.SendString("root") })

	cases := map[string]string{
		"/api/v1/public/pages/7": "/api/v1/public/pages/:id",
		"/":                      "/",
		"/no/such/route/123":     "unmatched",
	}
	for path, want := range cases {
		got = ""
		do(t, app, httptest.NewRequest("GET", path, nil))
		if got != want {
			t.Errorf("%s: routeTemplate = %q, want %q", path, got, want)
		}
	}
}

// The request context carries the DB deadline (3s) for repositories.
func TestRequestContextHasDeadline(t *testing.T) {
	app := NewFiber(ServerConfig{})
	var d time.Duration
	app.Get("/d", func(c *fiber.Ctx) error {
		dl, ok := c.UserContext().Deadline()
		if ok {
			d = time.Until(dl)
		}
		return c.SendString("x")
	})
	do(t, app, httptest.NewRequest("GET", "/d", nil))
	if d <= 0 || d > 3*time.Second {
		t.Errorf("deadline = %v, want within (0, 3s]", d)
	}
}
