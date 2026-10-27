package entities

import (
	"reflect"
	"strings"
	"testing"
)

func TestJSONBScan(t *testing.T) {
	var j JSONB
	for _, in := range []any{[]byte(`{"a":1}`), `{"a":1}`} {
		if err := j.Scan(in); err != nil || string(j) != `{"a":1}` {
			t.Errorf("%T: %q %v", in, j, err)
		}
	}
	if err := j.Scan(nil); err != nil || j != nil || j.Bytes() != nil {
		t.Errorf("NULL: %v %v", j, err)
	}
	if err := j.Scan(42); err == nil {
		t.Error("int must fail")
	}
	// the source buffer is copied
	src := []byte(`[1]`)
	_ = j.Scan(src)
	src[1] = '9'
	if string(j) != `[1]` {
		t.Error("JSONB aliases the driver buffer")
	}
	if v, _ := JSONB(nil).Value(); v != nil {
		t.Error("nil Value")
	}
	if v, _ := JSONB(`{}`).Value(); v != "{}" {
		t.Error("Value")
	}
}

func TestStringArrayScan(t *testing.T) {
	cases := map[string][]string{
		`{}`:                          {},
		`{a}`:                         {"a"},
		`{name,email,phone,topic}`:    {"name", "email", "phone", "topic"},
		`{"a b","c,d","e\"f","g\\h"}`: {"a b", "c,d", `e"f`, `g\h`},
		`{a,NULL,"NULL"}`:             {"a", "", "NULL"},
		`{"",x}`:                      {"", "x"},
	}
	for in, want := range cases {
		var a StringArray
		if err := a.Scan(in); err != nil || !reflect.DeepEqual([]string(a), want) {
			t.Errorf("%s -> %q (%v), want %q", in, a, err, want)
		}
		var b StringArray
		if err := b.Scan([]byte(in)); err != nil || !reflect.DeepEqual([]string(b), want) {
			t.Errorf("[]byte %s -> %q", in, b)
		}
	}
	var a StringArray
	if err := a.Scan(nil); err != nil || a != nil {
		t.Error("NULL")
	}
	if err := a.Scan([]string{"x"}); err != nil || a[0] != "x" {
		t.Error("[]string")
	}
	for _, bad := range []string{"", "a,b", "{a", "a}", `{"a`, `{"a"b}`} {
		if err := a.Scan(bad); err == nil {
			t.Errorf("%q must fail", bad)
		}
	}
	if err := a.Scan(3); err == nil {
		t.Error("int must fail")
	}
	// Value round-trips
	v, _ := StringArray{"a b", `c"d`, `e\f`}.Value()
	var back StringArray
	if err := back.Scan(v); err != nil || !reflect.DeepEqual([]string(back), []string{"a b", `c"d`, `e\f`}) {
		t.Errorf("round trip: %v %q", v, back)
	}
	if v, _ := StringArray(nil).Value(); v != nil {
		t.Error("nil Value")
	}
}

type tabler interface{ TableName() string }

// Every entity maps a view, names every column explicitly and has no json tags (doc/08 section 2.5).
func TestEntityConventions(t *testing.T) {
	for _, e := range All() {
		typ := reflect.TypeOf(e)
		tn, ok := e.(tabler)
		if !ok || !strings.HasPrefix(tn.TableName(), "v_") {
			t.Errorf("%s: TableName must return a view name", typ)
		}
		checkFields(t, typ)
	}
}

func checkFields(t *testing.T, typ reflect.Type) {
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Anonymous {
			checkFields(t, f.Type)
			continue
		}
		if f.Tag.Get("json") != "" {
			t.Errorf("%s.%s has a json tag", typ, f.Name)
		}
		g := f.Tag.Get("gorm")
		if !strings.HasPrefix(g, "column:") {
			t.Errorf("%s.%s has no explicit gorm column", typ, f.Name)
		}
	}
}
