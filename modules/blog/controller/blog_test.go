package controller

import (
	"net/url"
	"strings"
	"testing"

	"github.com/materm-organization/materm-web-portal-backend/modules/blog/services"
	"github.com/materm-organization/materm-web-portal-backend/pkg/apperror"
)

func parse(raw string) (services.PostsQuery, error) {
	v, _ := url.ParseQuery(raw)
	return ParsePostsQuery(func(k string, _ ...string) string { return v.Get(k) })
}

func TestParsePostsQueryDefaultsAndBounds(t *testing.T) {
	q, err := parse("")
	if err != nil || q.Page != 1 || q.Limit != 12 || q.Category != "" || q.Tag != "" {
		t.Fatalf("%+v %v", q, err)
	}
	q, err = parse("category=music&tag=night&page=3&limit=48")
	if err != nil || q != (services.PostsQuery{Category: "music", Tag: "night", Page: 3, Limit: 48}) {
		t.Fatalf("%+v %v", q, err)
	}
	if q, err = parse("limit=3"); err != nil || q.Limit != 3 {
		t.Fatalf("lower bound: %+v %v", q, err)
	}
	for raw, path := range map[string]string{
		"limit=2": "/limit", "limit=49": "/limit", "limit=0": "/limit", "limit=abc": "/limit", "limit=-1": "/limit",
		"page=0": "/page", "page=-2": "/page", "page=x": "/page", "page=100001": "/page", "page=1.5": "/page",
		"category=Music": "/category", "category=1abc": "/category", "category=a%20b": "/category", "category=" + strings.Repeat("a", 41): "/category",
		"tag=-x": "/tag", "tag=Night": "/tag", "tag=a/b": "/tag",
	} {
		_, perr := parse(raw)
		ae, ok := apperror.AsError(perr)
		if !ok || ae.Status != 422 || len(ae.Errors) != 1 || ae.Errors[0].Path != path || ae.Errors[0].In != "query" {
			t.Errorf("%s: %v", raw, perr)
		}
	}
	// every problem is reported at once
	_, err = parse("page=0&limit=1&category=X&tag=Y")
	if ae, _ := apperror.AsError(err); ae == nil || len(ae.Errors) != 4 {
		t.Errorf("all errors at once: %v", err)
	}
}
