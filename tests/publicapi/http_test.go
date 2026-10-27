package publicapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/entities"
	"github.com/materm-organization/materm-web-portal-backend/modules/content/repositories"
	"github.com/materm-organization/materm-web-portal-backend/pkg/assemble/httpx"
	"github.com/materm-organization/materm-web-portal-backend/pkg/media"
	"github.com/materm-organization/materm-web-portal-backend/pkg/publiccache"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/seedfake"
)

func newStack(t *testing.T) *stack {
	t.Helper()
	return wire(t, seedfake.Load(t).Set(), publiccache.New(publiccache.Options{}), media.NewBuilder(media.Config{}), "")
}

// NFR-12: every route of the three modules is registered through a routepolicy group, needs the portal key
// and is open to nobody else.
func TestEveryRouteNeedsThePortalKey(t *testing.T) {
	st := newStack(t)
	st.App.AssertEveryRouteDeclaresAPolicy(t)
	want := []string{
		"/api/v1/public/locales", "/api/v1/public/menus/:key", "/api/v1/public/pages/resolve", "/api/v1/public/settings",
		"/api/v1/public/site", "/api/v1/public/sitemap", "/api/v1/public/ui-strings",
	}
	got := st.App.Reg.Entries()
	if len(got) != len(want) {
		t.Fatalf("registry:\n%s", st.App.Reg.Describe())
	}
	for i, e := range got {
		if e.Path != want[i] || e.Method != http.MethodGet {
			t.Errorf("route %d: %s %s, want GET %s", i, e.Method, e.Path, want[i])
		}
	}
	if r := st.App.Get(t, "/api/v1/public/pages/by-path?path=/", "X-Portal-Key: -"); r.Status != http.StatusUnauthorized {
		t.Errorf("an unknown path without the key must answer 401, got %d", r.Status)
	}
	if r := st.App.Get(t, "/api/v1/public/pages/by-path?path=/"); r.Status != http.StatusNotFound {
		t.Errorf("by-path is not a route (the frontend calls /resolve): %d", r.Status)
	}
}

func TestHeadersOfASuccessfulRead(t *testing.T) {
	app := newStack(t).App
	r := app.Get(t, "/api/v1/public/pages/resolve?path=/our-path&locale=en")
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	h := r.Header
	if h.Get("Cache-Control") != httpx.CacheControlOK {
		t.Errorf("Cache-Control %q", h.Get("Cache-Control"))
	}
	if etag := h.Get("ETag"); len(etag) != len(`W/"0123456789abcdef"`) || etag[:3] != `W/"` {
		t.Errorf("ETag %q", etag)
	}
	if h.Get("Content-Language") != "en" || h.Get("Vary") != "" {
		t.Errorf("?locale: Content-Language %q Vary %q", h.Get("Content-Language"), h.Get("Vary"))
	}
	if h.Get("X-Request-Id") == "" || h.Get("Content-Type") == "" {
		t.Errorf("headers: %v", h)
	}
	if d := r.Data(); d["locale"] != "en" {
		t.Errorf("data.locale = %v", d["locale"])
	}

	// the default locale is announced as lo (Q-08-1); the header decides, so the answer varies on it
	r = app.Get(t, "/api/v1/public/pages/resolve?path=/", "Accept-Language: lo")
	if r.Header.Get("Content-Language") != "lo" || r.Header.Get("Vary") != "Accept-Language" || r.Data()["locale"] != "la" {
		t.Errorf("lo: %v %v", r.Header, r.Data()["locale"])
	}
	r = app.Get(t, "/api/v1/public/pages/resolve?path=/", "Accept-Language: en-GB,en;q=0.8,la;q=0.5")
	if r.Data()["locale"] != "en" || r.Header.Get("Vary") != "Accept-Language" {
		t.Errorf("en-GB: %v", r.Data()["locale"])
	}
	// an unknown language answers the default with 200 (FR-LOC-03), and ?locale for a closed language too
	r = app.Get(t, "/api/v1/public/pages/resolve?path=/", "Accept-Language: fr")
	if r.Status != 200 || r.Data()["locale"] != "la" {
		t.Errorf("fr: %d %v", r.Status, r.Data()["locale"])
	}
	r = app.Get(t, "/api/v1/public/pages/resolve?path=/&locale=fr")
	if r.Status != 200 || r.Data()["locale"] != "la" || r.Header.Get("Vary") != "" {
		t.Errorf("?locale=fr: %d %v", r.Status, r.Data()["locale"])
	}
	r = app.Get(t, "/api/v1/public/pages/resolve?path=/&locale=lo")
	if r.Data()["locale"] != "la" || r.Header.Get("Content-Language") != "lo" {
		t.Errorf("?locale=lo: %v", r.Data()["locale"])
	}
}

func TestConditionalGet(t *testing.T) {
	app := newStack(t).App
	for _, target := range []string{
		"/api/v1/public/locales", "/api/v1/public/settings?locale=en", "/api/v1/public/ui-strings?locale=en", "/api/v1/public/menus/main?locale=en",
		"/api/v1/public/site?locale=en", "/api/v1/public/pages/resolve?path=/blog&locale=en", "/api/v1/public/sitemap",
	} {
		first := app.Get(t, target)
		etag := first.Header.Get("ETag")
		if first.Status != 200 || etag == "" {
			t.Fatalf("%s: %d %q", target, first.Status, etag)
		}
		if again := app.Get(t, target); again.Header.Get("ETag") != etag {
			t.Errorf("%s: the ETag must be stable: %q vs %q", target, again.Header.Get("ETag"), etag)
		}
		nm := app.Get(t, target, "If-None-Match: "+etag)
		if nm.Status != http.StatusNotModified || len(nm.Body) != 0 || nm.Header.Get("ETag") != etag || nm.Header.Get("Cache-Control") != httpx.CacheControlOK {
			t.Errorf("%s: If-None-Match: %d body=%d %v", target, nm.Status, len(nm.Body), nm.Header)
		}
		if other := app.Get(t, target, `If-None-Match: W/"0000000000000000"`); other.Status != 200 {
			t.Errorf("%s: a different validator must give 200, got %d", target, other.Status)
		}
	}
	// the validator belongs to the locale
	a := app.Get(t, "/api/v1/public/site?locale=en").Header.Get("ETag")
	b := app.Get(t, "/api/v1/public/site?locale=la").Header.Get("ETag")
	if a == b {
		t.Error("different locales must have different validators")
	}
}

func TestErrors(t *testing.T) {
	app := newStack(t).App
	type tc struct {
		name, target string
		status       int
		code         string
	}
	for _, c := range []tc{
		{"unknown page", "/api/v1/public/pages/resolve?path=/nothing-here", 404, "NOT_FOUND"},
		{"reserved path", "/api/v1/public/pages/resolve?path=/api/x", 404, "NOT_FOUND"},
		{"missing path", "/api/v1/public/pages/resolve", 422, "VALIDATION_FAILED"},
		{"trailing slash", "/api/v1/public/pages/resolve?path=/blog/", 422, "VALIDATION_FAILED"},
		{"upper case", "/api/v1/public/pages/resolve?path=/Blog", 422, "VALIDATION_FAILED"},
		{"dot segment", "/api/v1/public/pages/resolve?path=/a/../b", 422, "VALIDATION_FAILED"},
		{"long path", "/api/v1/public/pages/resolve?path=/" + repeat("a", 201), 422, "VALIDATION_FAILED"},
		{"bad locale", "/api/v1/public/pages/resolve?path=/&locale=EN_us", 422, "VALIDATION_FAILED"},
		{"bad locale on settings", "/api/v1/public/settings?locale=123", 422, "VALIDATION_FAILED"},
		{"bad group", "/api/v1/public/settings?group=Brand", 422, "VALIDATION_FAILED"},
		{"bad ns", "/api/v1/public/ui-strings?ns=secrets", 422, "VALIDATION_FAILED"},
		{"unknown menu", "/api/v1/public/menus/nope", 404, "NOT_FOUND"},
		{"invalid menu key", "/api/v1/public/menus/NOPE!", 404, "NOT_FOUND"},
	} {
		r := app.Get(t, c.target)
		if r.Status != c.status || r.Code() != c.code {
			t.Errorf("%s: %d %s, want %d %s (%s)", c.name, r.Status, r.Code(), c.status, c.code, r.Body)
		}
		if c.status == 404 && r.Header.Get("Cache-Control") != httpx.CacheControlNotFound {
			t.Errorf("%s: a 404 is cacheable for a short time, got %q", c.name, r.Header.Get("Cache-Control"))
		}
		if c.status == 422 {
			if r.Header.Get("Cache-Control") != httpx.CacheControlNone {
				t.Errorf("%s: a 422 must not be cached, got %q", c.name, r.Header.Get("Cache-Control"))
			}
			errs, _ := r.JSON()["errors"].([]any)
			if len(errs) == 0 || errs[0].(map[string]any)["in"] != "query" {
				t.Errorf("%s: 422 must carry a query field error: %s", c.name, r.Body)
			}
		}
		if rid, _ := r.JSON()["request_id"].(string); rid == "" {
			t.Errorf("%s: no request_id", c.name)
		}
	}
	r := app.Get(t, "/api/v1/public/pages/resolve?path=/&locale=Bad")
	errs := r.JSON()["errors"].([]any)[0].(map[string]any)
	if errs["path"] != "/locale" {
		t.Errorf("locale error path: %v", errs)
	}
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}

func TestSettingsAndUIFilters(t *testing.T) {
	app := newStack(t).App
	d := app.Get(t, "/api/v1/public/settings?locale=en&group=brand,footer").Data()
	settings := d["settings"].(map[string]any)
	if len(settings) != 2 || settings["brand"] == nil || settings["footer"] == nil {
		t.Errorf("groups: %v", settings)
	}
	if _, ok := d["media"].(map[string]any)["23"]; !ok {
		t.Errorf("media of the brand settings: %v", d["media"])
	}
	if got := app.Get(t, "/api/v1/public/settings?group=footer,brand").Header.Get("ETag"); got != app.Get(t, "/api/v1/public/settings?group=brand,footer").Header.Get("ETag") {
		t.Error("the order of ?group must not change the answer")
	}
	ns := app.Get(t, "/api/v1/public/ui-strings?locale=en&ns=blog").Data()["strings"].(map[string]any)
	if len(ns) == 0 {
		t.Fatal("no blog strings")
	}
	for k := range ns {
		if len(k) < 5 || k[:5] != "blog." {
			t.Errorf("ns=blog returned %q", k)
		}
	}
}

// failingPages makes the page lookup fail the way a database outage does.
type failingPages struct{ repositories.PageRepository }

func (failingPages) FindByPath(context.Context, string, string) (*entities.PublishedPage, error) {
	return nil, errors.New("connection refused: secret-host:5432")
}

// failingLocales fails the locale list, which the locale chain is built from.
type failingLocales struct{ repositories.LocaleRepository }

func (failingLocales) ListActive(context.Context) ([]entities.PublicLocale, error) {
	return nil, errors.New("connection refused: secret-host:5432")
}

// A failure is a 5xx that is never cached (no-store), and its text never reaches the client.
func TestInfrastructureFailuresAreNotCachedAndNotLeaked(t *testing.T) {
	db := seedfake.Load(t)

	set := db.Set()
	set.Pages = failingPages{set.Pages}
	app := wire(t, set, publiccache.New(publiccache.Options{}), media.NewBuilder(media.Config{}), "").App
	r := app.Get(t, "/api/v1/public/pages/resolve?path=/")
	if r.Status != http.StatusInternalServerError || r.Code() != "INTERNAL_ERROR" || r.Header.Get("Cache-Control") != httpx.CacheControlNone {
		t.Errorf("repository failure: %d %s %q", r.Status, r.Code(), r.Header.Get("Cache-Control"))
	}
	if strings.Contains(string(r.Body), "secret-host") {
		t.Errorf("the cause leaked to the client: %s", r.Body)
	}

	set = db.Set()
	set.Locales = failingLocales{set.Locales}
	app = wire(t, set, publiccache.New(publiccache.Options{}), media.NewBuilder(media.Config{}), "").App
	for _, target := range []string{"/api/v1/public/pages/resolve?path=/", "/api/v1/public/site", "/api/v1/public/sitemap", "/api/v1/public/locales"} {
		r = app.Get(t, target)
		if r.Status < 500 || r.Header.Get("Cache-Control") != httpx.CacheControlNone || strings.Contains(string(r.Body), "secret-host") {
			t.Errorf("%s with the locales unavailable: %d %q %s", target, r.Status, r.Header.Get("Cache-Control"), r.Body)
		}
	}
	if r = app.Get(t, "/api/v1/public/sitemap"); r.Status != http.StatusServiceUnavailable || r.Code() != "SERVICE_UNAVAILABLE" {
		t.Errorf("the locale chain cannot be loaded: %d %s", r.Status, r.Code())
	}
}
