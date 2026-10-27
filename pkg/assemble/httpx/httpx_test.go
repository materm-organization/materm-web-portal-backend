package httpx

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/materm-organization/materm-web-portal-backend/pkg/apperror"
	"github.com/materm-organization/materm-web-portal-backend/pkg/localize"
	"github.com/materm-organization/materm-web-portal-backend/pkg/response"
)

func TestNoneMatch(t *testing.T) {
	const tag = `W/"abc"`
	for header, want := range map[string]bool{
		"": false, "*": true, tag: true, `"abc"`: true, `W/"zzz"`: false, `W/"zzz", W/"abc"`: true, ` "x" , "abc" `: true, `abc`: false,
	} {
		if got := NoneMatch(header, tag); got != want {
			t.Errorf("NoneMatch(%q) = %v, want %v", header, got, want)
		}
	}
}

func TestETagIsWeakAndStable(t *testing.T) {
	a, b := ETag([]byte(`{"a":1}`)), ETag([]byte(`{"a":1}`))
	if a != b || len(a) != len(`W/"0123456789abcdef"`) || a[:3] != `W/"` {
		t.Errorf("etag %q", a)
	}
	if ETag([]byte(`{"a":2}`)) == a {
		t.Error("different bodies must differ")
	}
}

type provider struct{ c *localize.Chain }

func (p provider) Chain(context.Context) (*localize.Chain, error) { return p.c, nil }

func app(t *testing.T) *fiber.App {
	t.Helper()
	chain, err := localize.NewChain([]localize.Locale{{Code: "la", IsDefault: true}, {Code: "en", FallbackCode: "la"}})
	if err != nil {
		t.Fatal(err)
	}
	a := fiber.New(fiber.Config{ErrorHandler: apperror.Handler})
	a.Get("/x", func(c *fiber.Ctx) error {
		neg, err := Negotiate(c, provider{chain})
		if err != nil {
			return err
		}
		return Send(c, &neg, response.Envelope{Success: true, Data: map[string]string{"locale": neg.Code}})
	})
	a.Get("/plain", func(c *fiber.Ctx) error { return Send(c, nil, response.Envelope{Success: true, Data: 1}) })
	a.Get("/missing", func(c *fiber.Ctx) error { return NotFound(c) })
	return a
}

func get(t *testing.T, a *fiber.App, target string, hdr ...string) *fiberResp {
	t.Helper()
	r := httptest.NewRequest("GET", target, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := a.Test(r, -1)
	if err != nil {
		t.Fatal(err)
	}
	return &fiberResp{resp}
}

type fiberResp struct{ *http.Response }

func (r *fiberResp) bodyLen() int {
	b, _ := io.ReadAll(r.Body)
	return len(b)
}

func TestSendHeaders(t *testing.T) {
	a := app(t)

	r := get(t, a, "/x?locale=en")
	if r.StatusCode != 200 || r.Header.Get("Cache-Control") != CacheControlOK || r.Header.Get("Content-Language") != "en" || r.Header.Get("Vary") != "" {
		t.Errorf("?locale: %d %v", r.StatusCode, r.Header)
	}
	etag := r.Header.Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}

	r = get(t, a, "/x", "Accept-Language", "lo-LA,en;q=0.5")
	if r.Header.Get("Content-Language") != "lo" || r.Header.Get("Vary") != "Accept-Language" {
		t.Errorf("Accept-Language: %v", r.Header)
	}

	r = get(t, a, "/x?locale=en", "If-None-Match", etag)
	if r.StatusCode != 304 || r.Header.Get("ETag") != etag || r.Header.Get("Cache-Control") != CacheControlOK || r.Header.Get("Content-Language") != "en" {
		t.Errorf("304: %d %v", r.StatusCode, r.Header)
	}
	if r.bodyLen() != 0 {
		t.Error("a 304 has no body")
	}

	r = get(t, a, "/x?locale=en", "If-None-Match", `W/"nope"`)
	if r.StatusCode != 200 {
		t.Errorf("non matching validator: %d", r.StatusCode)
	}

	r = get(t, a, "/plain")
	if r.Header.Get("Content-Language") != "" || r.Header.Get("Vary") != "" {
		t.Errorf("language independent: %v", r.Header)
	}

	r = get(t, a, "/x?locale=EN_us")
	if r.StatusCode != 422 {
		t.Errorf("bad locale: %d", r.StatusCode)
	}

	r = get(t, a, "/missing")
	if r.StatusCode != 404 || r.Header.Get("Cache-Control") != CacheControlNotFound {
		t.Errorf("404: %d %v", r.StatusCode, r.Header)
	}
}

func TestGuardSetsCacheControlOfErrors(t *testing.T) {
	a := fiber.New(fiber.Config{ErrorHandler: apperror.Handler})
	a.Get("/404", Guard(func(*fiber.Ctx) error { return apperror.NotFound() }))
	a.Get("/422", Guard(func(*fiber.Ctx) error { return apperror.Validation() }))
	a.Get("/500", Guard(func(*fiber.Ctx) error { return context.DeadlineExceeded }))
	a.Get("/200", Guard(func(c *fiber.Ctx) error { return Send(c, nil, response.Envelope{Success: true}) }))
	for path, want := range map[string]string{"/404": CacheControlNotFound, "/422": CacheControlNone, "/500": CacheControlNone, "/200": CacheControlOK} {
		if got := get(t, a, path).Header.Get("Cache-Control"); got != want {
			t.Errorf("%s: Cache-Control %q, want %q", path, got, want)
		}
	}
}
