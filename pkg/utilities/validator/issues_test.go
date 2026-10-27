package validator

import "testing"

type grant struct {
	Key     string   `json:"permission_key" validate:"required"`
	Actions []string `json:"actions" validate:"min=1,dive,oneof=read create update"`
}

type body struct {
	Email  string  `json:"email" validate:"required,email"`
	Name   string  `json:"name" validate:"required,min=2,max=5"`
	Grants []grant `json:"grants" validate:"dive"`
	Skip   string  `json:"-" validate:"required"`
}

func TestStructIssues(t *testing.T) {
	issues, err := Struct(body{Email: "nope", Name: "x", Grants: []grant{{Key: "a", Actions: []string{"read"}}, {Key: "", Actions: []string{"read", "fly"}}}})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Issue{}
	for _, is := range issues {
		got[is.Path] = is
	}
	check := func(path, code string) {
		t.Helper()
		if is, ok := got[path]; !ok || is.Code != code {
			t.Errorf("path %s: %+v (all: %+v)", path, got[path], issues)
		}
	}
	check("/email", "invalid_format")
	check("/name", "too_short")
	check("/grants/1/permission_key", "required")
	check("/grants/1/actions/1", "not_in_enum")
	if got["/name"].Params["min"] != int64(2) {
		t.Errorf("params: %+v", got["/name"].Params)
	}
	if got["/grants/1/actions/1"].Params["expected"] == nil {
		t.Error("oneof must list the expected values")
	}
	if _, ok := got["/Skip"]; !ok {
		if _, ok := got["/"]; ok {
			t.Error("unexpected")
		}
	}
}

func TestStructValid(t *testing.T) {
	issues, err := Struct(body{Email: "a@b.test", Name: "abc", Skip: "x"})
	if err != nil || len(issues) != 0 {
		t.Errorf("valid body: %v %v", issues, err)
	}
	if _, err := Struct(42); err == nil {
		t.Error("a non-struct is not a validation issue but an error")
	}
}

func TestPointer(t *testing.T) {
	cases := map[string]string{
		"Root.a":                 "/a",
		"Root.grants[3].actions": "/grants/3/actions",
		"Root.a.b[0][1]":         "/a/b/0/1",
		"Root.a/b":               "/a~1b",
		"Root":                   "",
	}
	for in, want := range cases {
		if got := pointer(in); got != want {
			t.Errorf("pointer(%q) = %q, want %q", in, got, want)
		}
	}
}
