package controller_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/materm-organization/materm-web-portal-backend/modules/forms/controller"
	"github.com/materm-organization/materm-web-portal-backend/modules/forms/models"
	"github.com/materm-organization/materm-web-portal-backend/modules/forms/routes"
	"github.com/materm-organization/materm-web-portal-backend/modules/forms/services"
	"github.com/materm-organization/materm-web-portal-backend/pkg/apperror"
	"github.com/materm-organization/materm-web-portal-backend/pkg/localize"
	"github.com/materm-organization/materm-web-portal-backend/pkg/portalkey"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/portalapp"
)

type fakeService struct {
	got  *services.SubmitInput
	err  error
	defn *models.FormDefinition
}

func (f *fakeService) Definition(context.Context, string, string) (*models.FormDefinition, error) {
	return f.defn, f.err
}
func (f *fakeService) Submit(_ context.Context, in services.SubmitInput) error {
	f.got = &in
	return f.err
}

func newApp(svc services.FormService) *portalapp.App {
	a := portalapp.New()
	locales := localize.NewProvider(func(context.Context) ([]localize.Locale, error) {
		return []localize.Locale{{Code: "la", IsDefault: true}, {Code: "en", FallbackCode: "la"}}, nil
	}, 0)
	routes.Register(a.API, controller.NewFormCtrl(svc, locales))
	return a
}

const submitPath = "/api/v1/public/forms/contact/submissions"

func post(a *portalapp.App, t *testing.T, body string, h map[string]string) portalapp.Response {
	t.Helper()
	if h == nil {
		h = map[string]string{}
	}
	if _, ok := h["Content-Type"]; !ok {
		h["Content-Type"] = "application/json"
	}
	return a.Do(t, portalapp.Request{Method: "POST", Path: submitPath, Body: []byte(body), Header: h})
}

func errCodes(t *testing.T, b []byte) (string, map[string]string) {
	t.Helper()
	var e apperror.Body
	if err := json.Unmarshal(b, &e); err != nil {
		t.Fatalf("%s", b)
	}
	m := map[string]string{}
	for _, fe := range e.Errors {
		m[fe.In+":"+fe.Path] = fe.Code
	}
	return e.Code, m
}

func TestSubmitHappyPathAndWhatTheServiceReceives(t *testing.T) {
	svc := &fakeService{}
	a := newApp(svc)
	r := post(a, t, `{"locale":"en","source_path":"/contact","data":{"name":"Noy"},"honeypot":"","elapsed_ms":8200}`,
		map[string]string{"X-Client-IP": "2001:db8:1:2:3:4:5:6", "X-Client-User-Agent": "UA/1.0"})
	if r.Status != 200 || string(r.Body) != `{"success":true,"data":{"status":"received"}}` {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if r.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control %q", r.Header.Get("Cache-Control"))
	}
	g := svc.got
	if g == nil || g.Slug != "contact" || g.Locale != "en" || g.Visitor.IP != "2001:db8:1:2::" || g.Visitor.UserAgent != "UA/1.0" {
		t.Fatalf("%+v", g)
	}
	if g.Request.Data["name"] != "Noy" || g.Request.SourcePath != "/contact" || *g.Request.ElapsedMS != 8200 || g.BodySize == 0 {
		t.Errorf("%+v", g.Request)
	}
	if len(g.Visitor.KeyID) != 16 || strings.Contains(portalapp.Key, g.Visitor.KeyID) {
		t.Errorf("key id %q", g.Visitor.KeyID)
	}
}

func TestSubmitWithoutKeyIs401AndNeverReachesTheService(t *testing.T) {
	svc := &fakeService{}
	a := newApp(svc)
	r := a.Do(t, portalapp.Request{Method: "POST", Path: submitPath, Body: []byte(`{}`), NoKey: true, Header: map[string]string{"Content-Type": "application/json"}})
	if r.Status != 401 || svc.got != nil {
		t.Fatalf("%d %+v", r.Status, svc.got)
	}
}

func TestSubmitHeadersAreIgnoredWithoutTheKey(t *testing.T) {
	// X-Client-IP of a caller without the key cannot even arrive (401), so the flag is what makes the header
	// count; with the key the header is honoured (checked above). A forged header with a wrong key is 401:
	r := post(newApp(&fakeService{}), t, `{}`, map[string]string{portalkey.Header: "wrong", "X-Client-IP": "1.2.3.4"})
	if r.Status != 401 {
		t.Fatalf("%d", r.Status)
	}
}

func TestSubmitRejectsMalformedRequests(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		header map[string]string
		status int
		code   string
		paths  map[string]string
	}{
		{"not json content type", `{}`, map[string]string{"Content-Type": "text/plain"}, 415, apperror.CodeUnsupportedMediaType, nil},
		{"no content type", `{}`, map[string]string{"Content-Type": ""}, 415, apperror.CodeUnsupportedMediaType, nil},
		{"syntax error", `{"locale":`, nil, 400, apperror.CodeBadRequest, nil},
		{"empty body", ``, nil, 400, apperror.CodeBadRequest, nil},
		{"trailing data", `{} {}`, nil, 400, apperror.CodeBadRequest, nil},
		{"array body", `[]`, nil, 422, apperror.CodeValidationFailed, nil},
		{"unknown member", `{"locale":"en","admin":true}`, nil, 422, apperror.CodeValidationFailed, map[string]string{"body:/admin": "unknown_field"}},
		{"unknown member is escaped", `{"a/b~c":1}`, nil, 422, apperror.CodeValidationFailed, map[string]string{"body:/a~1b~0c": "unknown_field"}},
		{"data is not an object", `{"data":"x"}`, nil, 422, apperror.CodeValidationFailed, map[string]string{"body:/data": "invalid_type"}},
		{"elapsed_ms is a string", `{"elapsed_ms":"fast"}`, nil, 422, apperror.CodeValidationFailed, map[string]string{"body:/elapsed_ms": "invalid_type"}},
		{"locale shape", `{"locale":"EN us"}`, nil, 422, apperror.CodeValidationFailed, map[string]string{"body:/locale": "invalid_format"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := &fakeService{}
			r := post(newApp(svc), t, c.body, c.header)
			if r.Status != c.status {
				t.Fatalf("%d %s", r.Status, r.Body)
			}
			code, paths := errCodes(t, r.Body)
			if code != c.code {
				t.Errorf("code %q", code)
			}
			for k, v := range c.paths {
				if paths[k] != v {
					t.Errorf("errors %v, want %s=%s", paths, k, v)
				}
			}
			if r.Header.Get("Cache-Control") != "no-store" {
				t.Errorf("Cache-Control %q", r.Header.Get("Cache-Control"))
			}
			if svc.got != nil {
				t.Error("reached the service")
			}
		})
	}
}

func TestSubmitErrorsFromTheServiceKeepNoStore(t *testing.T) {
	for _, c := range []struct {
		err    error
		status int
	}{
		{apperror.RateLimited(5e9), 429},
		{apperror.NotFound(), 404},
		{apperror.ServiceUnavailable(errors.New("x"), 0), 503},
		{apperror.PayloadTooLarge(), 413},
	} {
		r := post(newApp(&fakeService{err: c.err}), t, `{"locale":"en"}`, nil)
		if r.Status != c.status || r.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("%v: %d %q", c.err, r.Status, r.Header.Get("Cache-Control"))
		}
	}
	r := post(newApp(&fakeService{err: apperror.RateLimited(90e9)}), t, `{}`, nil)
	if r.Header.Get("Retry-After") != "90" {
		t.Errorf("Retry-After %q", r.Header.Get("Retry-After"))
	}
}

func TestSubmitLocaleNegotiation(t *testing.T) {
	for locale, want := range map[string]string{`"en"`: "en", `"lo"`: "la", `"fr"`: "la", `""`: "la", `"en-US"`: "en"} {
		svc := &fakeService{}
		post(newApp(svc), t, `{"locale":`+locale+`}`, nil)
		if svc.got == nil || svc.got.Locale != want {
			t.Errorf("locale %s -> %+v, want %s", locale, svc.got, want)
		}
	}
	// no locale member at all: the default
	svc := &fakeService{}
	post(newApp(svc), t, `{}`, nil)
	if svc.got == nil || svc.got.Locale != "la" {
		t.Errorf("%+v", svc.got)
	}
}

func TestDefinitionIsCacheableAndPolicyIsEnforced(t *testing.T) {
	a := newApp(&fakeService{defn: &models.FormDefinition{Slug: "contact", Fields: []models.FormField{}}})
	r := a.Get(t, "/api/v1/public/forms/contact?locale=en", nil)
	if r.Status != 200 || r.Header.Get("ETag") == "" || r.Header.Get("Content-Language") != "en" {
		t.Fatalf("%d %v", r.Status, r.Header)
	}
	if r2 := a.Get(t, "/api/v1/public/forms/contact?locale=en", map[string]string{"If-None-Match": r.Header.Get("ETag")}); r2.Status != 304 {
		t.Errorf("304 expected: %d", r2.Status)
	}
	if r3 := a.Get(t, "/api/v1/public/forms/contact?locale=!!", nil); r3.Status != 422 {
		t.Errorf("bad locale: %d", r3.Status)
	}
	if r4 := a.Do(t, portalapp.Request{Path: "/api/v1/public/forms/contact", NoKey: true}); r4.Status != 401 {
		t.Errorf("no key: %d", r4.Status)
	}
	for _, e := range a.Registry.Entries() {
		if e.Policy.String() != "portal-key" {
			t.Errorf("%s %s: %s", e.Method, e.Path, e.Policy)
		}
	}
	if len(a.Registry.Entries()) != 2 {
		t.Errorf("routes:\n%s", a.Registry.Describe())
	}
}
