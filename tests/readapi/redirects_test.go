package readapi

// Offline contract of P-17 GET /public/redirects/resolve over an in-memory rule table: normalising, locale
// precedence, chains, loops, the hop limit, the status of the last hop, error shapes and headers.

import (
	"context"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/entities"
	"github.com/materm-organization/materm-web-portal-backend/modules/content/repositories"
	seocontroller "github.com/materm-organization/materm-web-portal-backend/modules/seo/controller"
	seoroutes "github.com/materm-organization/materm-web-portal-backend/modules/seo/routes"
	seoservices "github.com/materm-organization/materm-web-portal-backend/modules/seo/services"
	"github.com/materm-organization/materm-web-portal-backend/pkg/localize"
	"github.com/materm-organization/materm-web-portal-backend/pkg/publiccache"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/portalapp"
)

type rule struct {
	src, locale, target string
	status              int
}

// ruleRepo is a SEORepository over a slice, with the lookup rule of the real one (locale first, then all).
type ruleRepo struct {
	rules []rule
	calls atomic.Int64
}

func (r *ruleRepo) Sitemap(context.Context, string) ([]entities.SitemapEntry, error) { return nil, nil }

func (r *ruleRepo) Redirect(_ context.Context, path, locale string) (*entities.ActiveRedirect, error) {
	r.calls.Add(1)
	var all *rule
	for i := range r.rules {
		x := &r.rules[i]
		if x.src != path {
			continue
		}
		if x.locale == locale {
			return &entities.ActiveRedirect{SourcePath: x.src, LocaleCode: &x.locale, Target: x.target, StatusCode: x.status}, nil
		}
		if x.locale == "" {
			all = x
		}
	}
	if all != nil {
		return &entities.ActiveRedirect{SourcePath: all.src, Target: all.target, StatusCode: all.status}, nil
	}
	return nil, repositories.ErrNotFound
}

func redirectApp(t *testing.T, rules []rule) (*portalapp.App, *ruleRepo) {
	t.Helper()
	repo := &ruleRepo{rules: rules}
	chain := localize.NewProvider(func(context.Context) ([]localize.Locale, error) {
		return []localize.Locale{{Code: "la", IsDefault: true}, {Code: "en", FallbackCode: "la"}}, nil
	}, 0)
	svc := seoservices.NewSEOService(seoservices.Deps{SEO: repo, Cache: publiccache.New(publiccache.Options{})})
	app := portalapp.New()
	seoroutes.Register(app.API, seocontroller.NewSEOCtrl(svc, chain))
	return app, repo
}

func TestOfflineRedirectResolve(t *testing.T) {
	app, _ := redirectApp(t, []rule{
		{"/old-blog", "", "/blog", 301},
		{"/old-blog", "en", "/blog?en=1", 302},
		{"/a", "", "/b", 301},
		{"/b", "", "/c", 302},
		{"/c", "", "https://example.com/final", 308},
		{"/p", "", "/q", 301},
		{"/q", "", "/r", 301},
		{"/r", "", "/s", 307},
		{"/s", "", "/t", 301}, // the 4th rule is never followed
		{"/loop1", "", "/loop2", 301},
		{"/loop2", "", "/loop1", 301},
		{"/self", "", "/self/?x=1", 301},
		{"/case", "", "/CASE-target", 301},
	})
	type want struct {
		status int
		target string
		code   int
		hops   float64
	}
	for _, c := range []struct {
		name, query string
		w           want
	}{
		{"all-locales rule", "path=/old-blog&locale=la", want{200, "/blog", 301, 1}},
		{"locale rule wins", "path=/old-blog&locale=en", want{200, "/blog?en=1", 302, 1}},
		{"normalised: case, trailing slash, query, fragment", "path=/OLD-Blog/%3Futm=1&locale=la", want{200, "/blog", 301, 1}},
		{"normalised: fragment", "path=" + url.QueryEscape("/old-blog/#top") + "&locale=la", want{200, "/blog", 301, 1}},
		{"chain to an absolute URL: status of the last hop", "path=/a&locale=la", want{200, "https://example.com/final", 308, 3}},
		{"chain stops at 3 hops", "path=/p&locale=la", want{200, "/s", 307, 3}},
		{"loop is not found", "path=/loop1&locale=la", want{status: 404}},
		{"self redirect through normalising is a loop", "path=/self&locale=la", want{status: 404}},
		{"unknown path", "path=/nothing&locale=la", want{status: 404}},
		{"root is not special", "path=/&locale=la", want{status: 404}},
		{"target is returned as stored", "path=/case&locale=la", want{200, "/CASE-target", 301, 1}},
	} {
		r := app.Get(t, "/api/v1/public/redirects/resolve?"+c.query, nil)
		if r.Status != c.w.status {
			t.Errorf("%s: %d, want %d (%s)", c.name, r.Status, c.w.status, r.Body)
			continue
		}
		b := offlineBody(r)
		if c.w.status == 404 {
			if b["code"] != "NOT_FOUND" || b["success"] != false {
				t.Errorf("%s: %s", c.name, r.Body)
			}
			if cc := r.Header.Get("Cache-Control"); cc != "public, max-age=30" {
				t.Errorf("%s: 404 Cache-Control %q", c.name, cc)
			}
			continue
		}
		d, _ := b["data"].(map[string]any)
		if d["target"] != c.w.target || d["status_code"] != float64(c.w.code) || d["hops"] != c.w.hops || len(d) != 3 {
			t.Errorf("%s: %s", c.name, r.Body)
		}
		if r.Header.Get("ETag") == "" || !strings.Contains(r.Header.Get("Cache-Control"), "max-age=30") {
			t.Errorf("%s: missing ETag / Cache-Control: %v", c.name, r.Header)
		}
	}
}

func TestOfflineRedirectValidationAndKey(t *testing.T) {
	app, repo := redirectApp(t, nil)
	for _, c := range []struct{ name, query string }{
		{"missing path", "locale=la"},
		{"empty path", "path="},
		{"no leading slash", "path=old-blog"},
		{"only a query", "path=" + url.QueryEscape("?x=1")},
		{"space", "path=" + url.QueryEscape("/a b")},
		{"control character", "path=" + url.QueryEscape("/a\x00b")},
		{"too long", "path=/" + strings.Repeat("a", 600)},
		{"bad locale", "path=/x&locale=!!"},
	} {
		r := app.Get(t, "/api/v1/public/redirects/resolve?"+c.query, nil)
		b := offlineBody(r)
		errs, _ := b["errors"].([]any)
		if r.Status != 422 || b["code"] != "VALIDATION_FAILED" || len(errs) == 0 || errs[0].(map[string]any)["in"] != "query" {
			t.Errorf("%s: %d %s", c.name, r.Status, r.Body)
		}
	}
	if n := repo.calls.Load(); n != 0 {
		t.Errorf("a refused request must not reach the repository (%d calls)", n)
	}
	r := app.Do(t, portalapp.Request{Path: "/api/v1/public/redirects/resolve?path=/x", NoKey: true})
	if r.Status != 401 {
		t.Errorf("without the portal key: %d", r.Status)
	}
}

func TestOfflineRedirectIfNoneMatch(t *testing.T) {
	app, _ := redirectApp(t, []rule{{"/x", "", "/y", 301}})
	r := app.Get(t, "/api/v1/public/redirects/resolve?path=/x&locale=la", nil)
	etag := r.Header.Get("ETag")
	if r.Status != 200 || etag == "" {
		t.Fatalf("%d %v", r.Status, r.Header)
	}
	if r2 := app.Get(t, "/api/v1/public/redirects/resolve?path=/x&locale=la", map[string]string{"If-None-Match": etag}); r2.Status != 304 || len(r2.Body) != 0 {
		t.Errorf("If-None-Match: %d %q", r2.Status, r2.Body)
	}
}
