package bootstrap

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/materm-organization/materm-web-portal-backend/pkg/metrics"
)

// The HTTP counters are labelled with the matched route TEMPLATE and the status CLASS, never the concrete path,
// and they see the FINAL status (the access log writes the error answer itself).
func TestMetricsMiddlewareLabelsByTemplateAndClass(t *testing.T) {
	app := testApp(t, ServerConfig{})
	app.Get("/things/:slug", func(c *fiber.Ctx) error { return c.SendString("ok") })
	app.Get("/limited", func(c *fiber.Ctx) error { return c.SendStatus(429) })

	cases := []struct{ path, route, class string }{
		{"/ok", "/ok", "2xx"},
		{"/things/secret-slug-1", "/things/:slug", "2xx"},
		{"/things/secret-slug-2", "/things/:slug", "2xx"},
		{"/fail", "/fail", "5xx"}, // an error returned by a handler: final status 500
		{"/boom", "/boom", "5xx"}, // a recovered panic
		{"/nope/abc", "unmatched", "4xx"},
	}
	before := map[string]uint64{}
	for _, c := range cases {
		before[c.route+c.class] = metrics.RequestCount("GET", c.route, c.class)
	}
	limitedBefore := metrics.RateLimitedCount("/limited")
	for _, c := range cases {
		do(t, app, httptest.NewRequest("GET", c.path, nil))
	}
	do(t, app, httptest.NewRequest("GET", "/limited", nil))

	want := map[string]uint64{"/ok2xx": 1, "/things/:slug2xx": 2, "/fail5xx": 1, "/boom5xx": 1, "unmatched4xx": 1}
	for _, c := range cases {
		k := c.route + c.class
		if got := metrics.RequestCount("GET", c.route, c.class) - before[k]; got != want[k] {
			t.Errorf("%s %s: +%d requests, want +%d", c.route, c.class, got, want[k])
		}
	}
	if metrics.RateLimitedCount("/limited") != limitedBefore+1 {
		t.Error("a 429 must increment rate_limited_total for its route")
	}
	if metrics.RequestCount("GET", "/things/secret-slug-1", "2xx") != 0 {
		t.Error("a concrete path must never become a label")
	}
}
