package localize

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Hints names the leaves of a props object that are locale maps ("x-localized": true in the content schema,
// doc/04 section 3.1). The portal has no read access to cms_section_types, so the list is compiled into the
// code (doc/08 section 2.4); guessing from the shape ({"la": ...}) is deliberately not done, because an object
// whose keys happen to be language codes would be collapsed wrongly.
//
// A path is dot separated. A segment ending in "[]" means "this key holds an array, continue in every
// element": "title", "cta.label", "items[].title", "rows[].slots[].alt". When "[]" ends the path, every
// element of the array is itself a locale map ("tags[]").
type Hints struct{ paths [][]segment }

type segment struct {
	key  string
	iter bool
}

// NewHints compiles paths. An empty path or an empty segment is an error (a programming mistake).
func NewHints(paths ...string) (*Hints, error) {
	h := &Hints{}
	for _, p := range paths {
		if p == "" {
			return nil, fmt.Errorf("localize: empty hint path")
		}
		var segs []segment
		for _, s := range strings.Split(p, ".") {
			iter := strings.HasSuffix(s, "[]")
			s = strings.TrimSuffix(s, "[]")
			if s == "" {
				return nil, fmt.Errorf("localize: empty segment in hint path %q", p)
			}
			segs = append(segs, segment{key: s, iter: iter})
		}
		h.paths = append(h.paths, segs)
	}
	return h, nil
}

// MustHints is NewHints for package-level tables; it panics on a malformed path.
func MustHints(paths ...string) *Hints {
	h, err := NewHints(paths...)
	if err != nil {
		panic(err)
	}
	return h
}

// Len is the number of compiled paths.
func (h *Hints) Len() int {
	if h == nil {
		return 0
	}
	return len(h.paths)
}

// DecodeObject decodes a JSON object keeping numbers exact (json.Number), so that ids survive a round trip.
func DecodeObject(raw []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	return m, nil
}

// Collapse returns props with every locale map named by hints replaced by one value, chosen leaf by leaf as
// the first locale of chain (requested, its fallback, the default) whose value is not empty (doc/04 section
// 4.2; "" and an empty rich text document count as missing). A leaf that has no value in any locale of the
// chain becomes nil (JSON null). fallback is true when at least one leaf used a locale other than chain[0].
//
// props is not modified: the maps along the hinted paths are copied, everything else is shared. A hinted
// path that does not exist in props is skipped.
func Collapse(props map[string]any, hints *Hints, chain []string) (out map[string]any, fallback bool) {
	if props == nil || hints.Len() == 0 || len(chain) == 0 {
		return props, false
	}
	st := &collapser{chain: chain}
	var cur any = props
	for _, p := range hints.paths {
		cur = st.apply(cur, p)
	}
	m, _ := cur.(map[string]any)
	return m, st.fallback
}

type collapser struct {
	chain    []string
	fallback bool
}

func (c *collapser) apply(node any, segs []segment) any {
	m, ok := node.(map[string]any)
	if !ok || len(segs) == 0 {
		return node
	}
	seg := segs[0]
	child, present := m[seg.key]
	if !present {
		return node
	}
	cp := make(map[string]any, len(m))
	for k, v := range m {
		cp[k] = v
	}
	last := len(segs) == 1
	switch {
	case last && !seg.iter:
		cp[seg.key] = c.pick(child)
	case seg.iter:
		arr, isArr := child.([]any)
		if !isArr {
			return node
		}
		na := make([]any, len(arr))
		for i, el := range arr {
			if last {
				na[i] = c.pick(el)
			} else {
				na[i] = c.apply(el, segs[1:])
			}
		}
		cp[seg.key] = na
	default:
		cp[seg.key] = c.apply(child, segs[1:])
	}
	return cp
}

// pick chooses the value of one locale map. A value that is not an object (a plain string already, null) is
// returned unchanged so that data written before a field became localised still renders.
func (c *collapser) pick(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	for i, code := range c.chain {
		if x, has := m[code]; has && nonEmpty(x) {
			if i > 0 {
				c.fallback = true
			}
			return x
		}
	}
	return nil
}

// nonEmpty is the "has a value" test of the fallback rule: "" is missing, and so is a rich text document
// without content.
func nonEmpty(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return x != ""
	case map[string]any:
		if t, _ := x["type"].(string); t == "doc" {
			content, _ := x["content"].([]any)
			return len(content) > 0
		}
		return len(x) > 0
	case []any:
		return len(x) > 0
	default:
		return true
	}
}
