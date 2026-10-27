package readkit

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/materm-organization/materm-web-portal-backend/pkg/apperror"
	"github.com/materm-organization/materm-web-portal-backend/pkg/localize"
	"github.com/materm-organization/materm-web-portal-backend/pkg/media"
	"github.com/materm-organization/materm-web-portal-backend/pkg/pagination"
	"github.com/materm-organization/materm-web-portal-backend/pkg/routepolicy"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/portalapp"
)

func TestNoneMatch(t *testing.T) {
	const etag = `W/"0123456789abcdef"`
	for header, want := range map[string]bool{
		``:                                false,
		`W/"0123456789abcdef"`:            true,
		`"0123456789abcdef"`:              true, // weak comparison
		`W/"other", W/"0123456789abcdef"`: true,
		`W/"other"`:                       false,
		`*`:                               true,
		`W/"0123456789abcdeg"`:            false,
	} {
		if got := NoneMatch(header, etag); got != want {
			t.Errorf("If-None-Match %q: %v, want %v", header, got, want)
		}
	}
}

func appWith(h fiber.Handler) *portalapp.App {
	a := portalapp.New()
	a.API.Get("/x", routepolicy.PortalKey(), Guard(h))
	return a
}

func TestSendHeadersEtagAnd304(t *testing.T) {
	neg := localize.Negotiation{Code: "la", ContentLanguage: "lo", Vary: true}
	a := appWith(func(c *fiber.Ctx) error { return Send(c, neg, OKBody(map[string]string{"k": "v"})) })

	r := a.Get(t, "/api/v1/x", nil)
	if r.Status != 200 {
		t.Fatalf("status %d %s", r.Status, r.Body)
	}
	etag := r.Header.Get("ETag")
	if len(etag) != len(`W/"0123456789abcdef"`) || etag[:3] != `W/"` {
		t.Fatalf("etag %q", etag)
	}
	for h, want := range map[string]string{
		"Cache-Control":    CacheControlOK,
		"Content-Language": "lo",
		"Vary":             "Accept-Language",
		"Content-Type":     "application/json; charset=utf-8",
	} {
		if got := r.Header.Get(h); got != want {
			t.Errorf("%s = %q, want %q", h, got, want)
		}
	}
	var env struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
	}
	if err := json.Unmarshal(r.Body, &env); err != nil || !env.Success || env.Data["k"] != "v" {
		t.Fatalf("body %s", r.Body)
	}

	// the same bytes give the same ETag; a matching If-None-Match gets 304 with the headers and no body
	r2 := a.Get(t, "/api/v1/x", map[string]string{"If-None-Match": etag})
	if r2.Status != http.StatusNotModified || len(r2.Body) != 0 {
		t.Fatalf("304 expected, got %d %q", r2.Status, r2.Body)
	}
	for _, h := range []string{"ETag", "Cache-Control", "Content-Language", "Vary"} {
		if r2.Header.Get(h) == "" {
			t.Errorf("304 lost %s", h)
		}
	}
	if r2.Header.Get("ETag") != etag {
		t.Errorf("etag changed: %q", r2.Header.Get("ETag"))
	}
	// a stale validator gets the full answer
	if r3 := a.Get(t, "/api/v1/x", map[string]string{"If-None-Match": `W/"deadbeefdeadbeef"`}); r3.Status != 200 {
		t.Errorf("stale validator: %d", r3.Status)
	}
}

func TestNoVaryWhenAcceptLanguageWasNotUsed(t *testing.T) {
	a := appWith(func(c *fiber.Ctx) error {
		return Send(c, localize.Negotiation{Code: "en", ContentLanguage: "en"}, OKBody(1))
	})
	r := a.Get(t, "/api/v1/x", nil)
	if r.Header.Get("Vary") != "" && r.Header.Get("Vary") != "Origin" {
		t.Errorf("Vary = %q", r.Header.Get("Vary"))
	}
}

func TestSendRedirectShape(t *testing.T) {
	a := appWith(func(c *fiber.Ctx) error {
		return SendRedirect(c, localize.Negotiation{Code: "en", ContentLanguage: "en"}, "/blog/music-night")
	})
	r := a.Get(t, "/api/v1/x", nil)
	if r.Status != 308 || r.Header.Get("Location") != "/blog/music-night" || r.Header.Get("Cache-Control") != CacheControlShort {
		t.Fatalf("%d %v", r.Status, r.Header)
	}
	if string(r.Body) != `{"success":true,"data":{"redirect":{"location":"/blog/music-night","status":308}}}` {
		t.Fatalf("body %s", r.Body)
	}
}

func TestGuardSetsCacheControlOfErrors(t *testing.T) {
	for _, c := range []struct {
		err    error
		status int
		cc     string
	}{
		{apperror.NotFound(), 404, CacheControlShort},
		{apperror.Validation(apperror.Field("/x", "bad")), 422, CacheControlNone},
		{apperror.RateLimited(time.Second), 429, CacheControlNone},
		{apperror.ServiceUnavailable(errors.New("down"), 0), 503, CacheControlNone},
		{errors.New("boom"), 500, CacheControlNone},
	} {
		a := appWith(func(*fiber.Ctx) error { return c.err })
		r := a.Get(t, "/api/v1/x", nil)
		if r.Status != c.status || r.Header.Get("Cache-Control") != c.cc {
			t.Errorf("%v: %d %q, want %d %q", c.err, r.Status, r.Header.Get("Cache-Control"), c.status, c.cc)
		}
	}
}

func TestNoStoreAppliesToSuccessAndError(t *testing.T) {
	a := portalapp.New()
	a.API.Post("/y", routepolicy.PortalKey(), NoStore(func(c *fiber.Ctx) error { return c.SendString("ok") }))
	a.API.Post("/z", routepolicy.PortalKey(), NoStore(func(*fiber.Ctx) error { return apperror.NotFound() }))
	for _, p := range []string{"/api/v1/y", "/api/v1/z"} {
		r := a.Do(t, portalapp.Request{Method: "POST", Path: p})
		if r.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("%s: Cache-Control %q", p, r.Header.Get("Cache-Control"))
		}
	}
}

func TestMediaListNeverNull(t *testing.T) {
	b, _ := json.Marshal(NewMediaList[int](nil, pagination.Meta{Total: 0, Page: 1, Limit: 12}, nil))
	if string(b) != `{"success":true,"data":[],"meta":{"total":0,"page":1,"limit":12,"total_page":0},"media":{}}` {
		t.Fatalf("%s", b)
	}
	b, _ = json.Marshal(NewMediaList([]int{1}, pagination.Meta{Total: 1, Page: 1, Limit: 12, TotalPage: 1}, media.Map{"7": {URL: "/a.jpg"}}))
	var out map[string]json.RawMessage
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"success", "data", "meta", "media"} {
		if _, ok := out[k]; !ok {
			t.Errorf("missing %s: %s", k, b)
		}
	}
}

func TestDateFormats(t *testing.T) {
	lo := ParseDateFormats([]byte(`{"short":"{month}. {day}.{year}","months":["Jan","Feb","Mar","Apr","May","Jun","Jul","Aug","Sep","Oct","Nov","Dec"]}`))
	d := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	if got := lo.Format(d); got != "Sep. 05.2026" {
		t.Errorf("%q", got)
	}
	// Asia/Vientiane is UTC+7: 20:00 UTC on 5 Sep is already 6 Sep in Laos
	if got := lo.Format(time.Date(2026, 9, 5, 20, 0, 0, 0, time.UTC)); got != "Sep. 06.2026" {
		t.Errorf("zone: %q", got)
	}
	if got := lo.Format(time.Date(2026, 12, 31, 16, 59, 0, 0, time.UTC)); got != "Dec. 31.2026" {
		t.Errorf("year end before midnight: %q", got)
	}
	if got := lo.Format(time.Date(2026, 12, 31, 17, 0, 0, 0, time.UTC)); got != "Jan. 01.2027" {
		t.Errorf("year end after midnight: %q", got)
	}
	// no usable formats: an ISO-like date, still in Vientiane
	if got := (DateFormats{}).Format(d); got != "2026-09-05" {
		t.Errorf("zero: %q", got)
	}
	if got := ParseDateFormats([]byte(`not json`)).Format(d); got != "2026-09-05" {
		t.Errorf("invalid: %q", got)
	}
	// months not a list of 12: numeric month
	if got := (DateFormats{Short: "{day}/{month}/{year}", Months: []string{"x"}}).Format(d); got != "05/09/2026" {
		t.Errorf("short months: %q", got)
	}
	if ISO(time.Date(2026, 9, 5, 7, 0, 0, 0, Vientiane)) != "2026-09-05T00:00:00Z" {
		t.Errorf("ISO is UTC")
	}
}

func TestPostHrefAndPatterns(t *testing.T) {
	if PostHref("a-b") != "/blog/a-b" {
		t.Fatal(PostHref("a-b"))
	}
	for s, want := range map[string]bool{"music-night": true, "a": true, "a--b": false, "-a": false, "A": false, "a_b": false, "a/b": false, "": false} {
		if SlugPattern.MatchString(s) != want {
			t.Errorf("slug %q", s)
		}
	}
	for s, want := range map[string]bool{"forest-story": true, "a1_b": true, "1a": false, "": false, "A": false} {
		if KeyPattern.MatchString(s) != want {
			t.Errorf("key %q", s)
		}
	}
}
