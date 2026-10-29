package readapi

// Offline (no database, no Redis) API tests of the read modules P-07 ... P-14 over the in-memory seed
// (pkg/testutil/seedfake): the shapes, error codes and headers that the dbtest suite in this directory proves
// against PostgreSQL are also pinned here, so that `make test` alone catches a change of contract.

import (
	"context"
	"encoding/json"
	"testing"

	blogcontroller "github.com/materm-organization/materm-web-portal-backend/modules/blog/controller"
	blogroutes "github.com/materm-organization/materm-web-portal-backend/modules/blog/routes"
	blogservices "github.com/materm-organization/materm-web-portal-backend/modules/blog/services"
	"github.com/materm-organization/materm-web-portal-backend/modules/content/repositories"
	faqcontroller "github.com/materm-organization/materm-web-portal-backend/modules/faq/controller"
	faqroutes "github.com/materm-organization/materm-web-portal-backend/modules/faq/routes"
	faqservices "github.com/materm-organization/materm-web-portal-backend/modules/faq/services"
	formscontroller "github.com/materm-organization/materm-web-portal-backend/modules/forms/controller"
	formsroutes "github.com/materm-organization/materm-web-portal-backend/modules/forms/routes"
	formsservices "github.com/materm-organization/materm-web-portal-backend/modules/forms/services"
	gallerycontroller "github.com/materm-organization/materm-web-portal-backend/modules/gallery/controller"
	galleryroutes "github.com/materm-organization/materm-web-portal-backend/modules/gallery/routes"
	galleryservices "github.com/materm-organization/materm-web-portal-backend/modules/gallery/services"
	"github.com/materm-organization/materm-web-portal-backend/pkg/localize"
	"github.com/materm-organization/materm-web-portal-backend/pkg/media"
	"github.com/materm-organization/materm-web-portal-backend/pkg/publiccache"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/portalapp"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/seedfake"
)

func offlineApp(t *testing.T) *portalapp.App {
	t.Helper()
	db := seedfake.Load(t)
	content := db.Set()
	mb := media.NewBuilder(media.Config{})
	lookup := repositories.NewMediaLookup(content.Assets)
	chain := localize.NewProvider(func(ctx context.Context) ([]localize.Locale, error) {
		rows, err := content.Locales.ListActive(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]localize.Locale, 0, len(rows))
		for _, l := range rows {
			fb := ""
			if l.FallbackCode != nil {
				fb = *l.FallbackCode
			}
			out = append(out, localize.Locale{Code: l.Code, FallbackCode: fb, IsDefault: l.IsDefault})
		}
		return out, nil
	}, 0)
	cache := publiccache.New(publiccache.Options{}) // no Redis: every read goes to the (fake) repositories
	app := portalapp.New()
	blogroutes.Register(app.API, blogcontroller.NewBlogCtrl(blogservices.NewBlogService(blogservices.Deps{
		Content: content, Media: mb, Lookup: lookup, Cache: cache, Locales: chain, DefaultBaseURL: "https://fallback.example.test",
	}), chain))
	galleryroutes.Register(app.API, gallerycontroller.NewGalleryCtrl(galleryservices.NewGalleryService(galleryservices.Deps{
		Content: content, Media: mb, Lookup: lookup, Cache: cache,
	}), chain))
	faqroutes.Register(app.API, faqcontroller.NewFaqCtrl(faqservices.NewFaqService(faqservices.Deps{Content: content, Cache: cache}), chain))
	formsroutes.Register(app.API, formscontroller.NewFormCtrl(formsservices.NewFormService(formsservices.Deps{
		Forms: content.Forms, Cache: cache,
	}), chain))
	return app
}

func offlineBody(r portalapp.Response) map[string]any {
	var m map[string]any
	_ = json.Unmarshal(r.Body, &m)
	return m
}

// Every read endpoint: 401 without the key, the standard envelope on every error, a request id, the right code
// and a field error located in the query for a malformed parameter.
func TestOfflineErrorContractOfEveryReadEndpoint(t *testing.T) {
	app := offlineApp(t)
	cases := []struct {
		name, path string
		status     int
		code       string
		in         string // "query" when a field error must point into the query
	}{
		{"posts bad locale", "/api/v1/public/posts?locale=EN_us", 422, "VALIDATION_FAILED", "query"},
		{"posts bad limit", "/api/v1/public/posts?limit=1", 422, "VALIDATION_FAILED", "query"},
		{"posts bad page", "/api/v1/public/posts?page=0", 422, "VALIDATION_FAILED", "query"},
		{"posts bad category", "/api/v1/public/posts?category=Bad!", 422, "VALIDATION_FAILED", "query"},
		{"post unknown slug", "/api/v1/public/posts/no-such-post", 404, "NOT_FOUND", ""},
		{"post bad locale", "/api/v1/public/posts/x?locale=1", 422, "VALIDATION_FAILED", "query"},
		{"categories bad locale", "/api/v1/public/categories?locale=!!", 422, "VALIDATION_FAILED", "query"},
		{"tags bad locale", "/api/v1/public/tags?locale=!!", 422, "VALIDATION_FAILED", "query"},
		{"albums bad locale", "/api/v1/public/albums?locale=!!", 422, "VALIDATION_FAILED", "query"},
		{"album unknown key", "/api/v1/public/albums/no-such-album", 404, "NOT_FOUND", ""},
		{"album bad locale", "/api/v1/public/albums/x?locale=!!", 422, "VALIDATION_FAILED", "query"},
		{"faq unknown group", "/api/v1/public/faq/no-such-group", 404, "NOT_FOUND", ""},
		{"faq bad locale", "/api/v1/public/faq/contact?locale=!!", 422, "VALIDATION_FAILED", "query"},
		{"form unknown slug", "/api/v1/public/forms/no-such-form", 404, "NOT_FOUND", ""},
		{"form bad locale", "/api/v1/public/forms/contact?locale=!!", 422, "VALIDATION_FAILED", "query"},
	}
	for _, c := range cases {
		r := app.Get(t, c.path, nil)
		b := offlineBody(r)
		if r.Status != c.status || b["code"] != c.code || b["success"] != false {
			t.Errorf("%s: %d %v, want %d %s (%s)", c.name, r.Status, b["code"], c.status, c.code, r.Body)
			continue
		}
		if rid, _ := b["request_id"].(string); rid == "" || r.Header.Get("X-Request-Id") != rid {
			t.Errorf("%s: request id missing or not echoed in the header", c.name)
		}
		if c.in != "" {
			errs, _ := b["errors"].([]any)
			if len(errs) == 0 || errs[0].(map[string]any)["in"] != c.in {
				t.Errorf("%s: want a %s field error: %s", c.name, c.in, r.Body)
			}
		}
	}
}

// The success shapes (envelope + the members the frontend loaders read) of each read endpoint. Lists answer
// data as an array with meta (paginated ones) and media; single resources answer an object.
func TestOfflineSuccessShapes(t *testing.T) {
	app := offlineApp(t)
	type shape struct {
		path   string
		list   bool     // data is an array
		meta   bool     // top-level meta
		member []string // data[0] members (list) or data members (object)
	}
	for _, c := range []shape{
		{"/api/v1/public/posts?locale=en", true, true, []string{"id", "slug", "href", "title", "resolved_locale", "is_fallback"}},
		{"/api/v1/public/categories?locale=en", true, false, []string{"key", "name", "slug", "sort_order"}},
		{"/api/v1/public/albums?locale=en", true, true, []string{"id", "key", "title", "cover", "resolved_locale"}},
		{"/api/v1/public/faq/contact?locale=en", false, false, []string{"locale", "group_key", "resolved_locale", "items"}},
		{"/api/v1/public/forms/contact?locale=en", false, false, []string{"slug", "resolved_locale", "title", "submit_label", "success"}},
	} {
		r := app.Get(t, c.path, nil)
		b := offlineBody(r)
		if r.Status != 200 || b["success"] != true {
			t.Errorf("%s: %d %s", c.path, r.Status, r.Body)
			continue
		}
		var obj map[string]any
		if c.list {
			arr, ok := b["data"].([]any)
			if !ok || len(arr) == 0 {
				t.Errorf("%s: data must be a non-empty array: %s", c.path, r.Body)
				continue
			}
			obj, _ = arr[0].(map[string]any)
		} else {
			obj, _ = b["data"].(map[string]any)
		}
		for _, k := range c.member {
			if _, ok := obj[k]; !ok {
				t.Errorf("%s: member %q missing: %v", c.path, k, obj)
			}
		}
		if _, ok := b["meta"]; ok != c.meta {
			t.Errorf("%s: meta present=%v, want %v", c.path, ok, c.meta)
		}
	}
	// an empty list is still 200 with an array, never null or 404
	r := app.Get(t, "/api/v1/public/tags?locale=en", nil)
	if arr, ok := offlineBody(r)["data"].([]any); r.Status != 200 || !ok || len(arr) != 0 {
		t.Errorf("tags: %d %s", r.Status, r.Body)
	}
}
