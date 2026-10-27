package controller

import "testing"

func TestPathPatternIsTheDatabaseCheck(t *testing.T) {
	ok := []string{"/", "/our-path", "/a/b-c/d1", "/0"}
	bad := []string{"", "our-path", "/Our", "/a/", "//", "/a//b", "/a b", "/a_b", "/a/../b", "/é", "/a?x=1", "/a#x"}
	for _, p := range ok {
		if !PathPattern.MatchString(p) {
			t.Errorf("%q must be valid", p)
		}
	}
	for _, p := range bad {
		if PathPattern.MatchString(p) {
			t.Errorf("%q must be invalid", p)
		}
	}
}
