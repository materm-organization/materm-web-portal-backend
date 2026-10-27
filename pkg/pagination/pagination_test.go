package pagination

import (
	"strings"
	"testing"

	"github.com/materm-organization/materm-web-portal-backend/pkg/apperror"
)

func values(m map[string]string) func(string, ...string) string {
	return func(k string, _ ...string) string { return m[k] }
}

var spec = Spec{
	Sorts:       map[string]string{"id": "id", "name": "name", "last_login_at": "u.last_login_at"},
	DefaultSort: "-id",
	Keyword:     true,
}

func TestDefaults(t *testing.T) {
	q, err := ParseValues(values(nil), spec)
	if err != nil {
		t.Fatal(err)
	}
	if q.Page != 1 || q.Limit != 20 || q.Offset() != 0 || q.Keyword != "" || q.OrderBy() != "id DESC" {
		t.Errorf("defaults: %+v order=%s", q, q.OrderBy())
	}
}

func TestParse(t *testing.T) {
	q, err := ParseValues(values(map[string]string{"page": "3", "limit": "50", "keyword": "  ann ", "sort": "-last_login_at,name"}), spec)
	if err != nil {
		t.Fatal(err)
	}
	if q.Page != 3 || q.Limit != 50 || q.Offset() != 100 || q.Keyword != "ann" {
		t.Errorf("%+v", q)
	}
	if got := q.OrderBy(); got != "u.last_login_at DESC, name ASC, id DESC" {
		t.Errorf("OrderBy = %q (id tie-breaker follows the first key)", got)
	}
	q, _ = ParseValues(values(map[string]string{"sort": "name"}), spec)
	if got := q.OrderBy(); got != "name ASC, id ASC" {
		t.Errorf("OrderBy = %q", got)
	}
	q, _ = ParseValues(values(map[string]string{"sort": "id"}), spec)
	if got := q.OrderBy(); got != "id ASC" {
		t.Errorf("explicit id must not be duplicated: %q", got)
	}
	custom := spec
	custom.IDColumn = "u.id"
	q, _ = ParseValues(values(map[string]string{"sort": "name"}), custom)
	if got := q.OrderBy(); got != "name ASC, u.id ASC" {
		t.Errorf("custom id column: %q", got)
	}
}

func TestRejectsInsteadOfClamping(t *testing.T) {
	cases := []struct {
		name   string
		in     map[string]string
		path   string
		code   string
		params string
	}{
		{"limit 0", map[string]string{"limit": "0"}, "/limit", "out_of_range", ""},
		{"limit 101", map[string]string{"limit": "101"}, "/limit", "out_of_range", ""},
		{"limit text", map[string]string{"limit": "abc"}, "/limit", "invalid_type", ""},
		{"page 0", map[string]string{"page": "0"}, "/page", "out_of_range", ""},
		{"page negative", map[string]string{"page": "-2"}, "/page", "out_of_range", ""},
		{"page text", map[string]string{"page": "x"}, "/page", "invalid_type", ""},
		{"unknown sort", map[string]string{"sort": "password_hash"}, "/sort", "not_in_enum", ""},
		{"sort injection", map[string]string{"sort": "name; drop table users"}, "/sort", "not_in_enum", ""},
		{"sort empty term", map[string]string{"sort": "name,,id"}, "/sort", "not_in_enum", ""},
		{"sort repeated", map[string]string{"sort": "name,-name"}, "/sort", "not_in_enum", ""},
		{"keyword too long", map[string]string{"keyword": strings.Repeat("ก", 101)}, "/keyword", "too_long", ""},
	}
	for _, c := range cases {
		_, err := ParseValues(values(c.in), spec)
		if err == nil || err.Status != 422 || err.Code != apperror.CodeValidationFailed {
			t.Errorf("%s: expected 422, got %v", c.name, err)
			continue
		}
		if len(err.Errors) != 1 || err.Errors[0].In != "query" || err.Errors[0].Path != c.path || err.Errors[0].Code != c.code {
			t.Errorf("%s: field errors %+v", c.name, err.Errors)
		}
	}
	// 100 code points is fine, counted as characters not bytes
	if _, err := ParseValues(values(map[string]string{"keyword": strings.Repeat("ກ", 100)}), spec); err != nil {
		t.Errorf("100 Lao characters must be accepted: %v", err)
	}
	// several problems are all reported
	_, err := ParseValues(values(map[string]string{"page": "0", "limit": "0"}), spec)
	if err == nil || len(err.Errors) != 2 {
		t.Errorf("both problems expected: %+v", err)
	}
	// keyword on an endpoint that does not search
	noKw := spec
	noKw.Keyword = false
	if _, err := ParseValues(values(map[string]string{"keyword": "x"}), noKw); err == nil || err.Errors[0].Code != "unknown_field" {
		t.Errorf("keyword without support: %+v", err)
	}
}

func TestMeta(t *testing.T) {
	cases := []struct {
		total       int64
		limit       int
		wantPages   int
		page, wantP int
	}{{0, 20, 0, 1, 1}, {1, 20, 1, 1, 1}, {20, 20, 1, 1, 1}, {21, 20, 2, 1, 1}, {42, 20, 3, 2, 2}, {100, 100, 1, 1, 1}, {101, 100, 2, 1, 1}}
	for _, c := range cases {
		m := NewMeta(c.total, ListQuery{Page: c.page, Limit: c.limit})
		if m.TotalPage != c.wantPages || m.Total != c.total || m.Limit != c.limit || m.Page != c.wantP {
			t.Errorf("NewMeta(%d,%d) = %+v", c.total, c.limit, m)
		}
	}
}

func TestLike(t *testing.T) {
	q := ListQuery{Keyword: `50%_off\now`}
	if got := q.Like(); got != `%50\%\_off\\now%` {
		t.Errorf("Like = %q", got)
	}
	if (ListQuery{}).Like() != "" {
		t.Error("no keyword, no pattern")
	}
}

func TestLoadMoreQuery(t *testing.T) {
	q := LoadMoreQuery{Limit: 500}
	if got := q.LimitOrDefault(10, 100); got != 10 {
		t.Errorf("out of range falls back to the default, got %d", got)
	}
	if got := (LoadMoreQuery{Limit: 25}).LimitOrDefault(10, 100); got != 25 {
		t.Errorf("in range is kept, got %d", got)
	}
	q.SetResult([]int{1, 2}, true, 7)
	if !q.HasMore || q.NextCursor != 7 {
		t.Errorf("more pages: %+v", q)
	}
	q.SetResult([]int{3}, false, 9)
	if q.HasMore || q.NextCursor != 0 {
		t.Errorf("a finished list reports no cursor: %+v", q)
	}
}
