// Package jsondiff compares a golden JSON value with a produced one structurally, for the golden-file tests of
// the public API.
package jsondiff

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Options tune a comparison.
type Options struct {
	// Mask reports paths whose values are ignored (generated ids, timestamps). A path is ".a.b[0].c".
	Mask func(path string) bool
	// Tolerated lists keys that may exist in the produced value beyond the golden one, inside objects whose
	// path starts with TolerateUnder; the keys found are recorded in Extras.
	Tolerated map[string]bool
	// TolerateUnder restricts Tolerated to objects whose path starts with this prefix (".media.").
	TolerateUnder string
	// Extras collects the tolerated extra keys that were seen.
	Extras map[string]bool
}

// Generic decodes JSON bytes into a generic value (numbers as float64).
func Generic(b []byte) (any, error) {
	var v any
	err := json.Unmarshal(b, &v)
	return v, err
}

// ToGeneric marshals v and decodes it again.
func ToGeneric(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return Generic(b)
}

// Diff returns the sorted differences between want (golden) and got.
func Diff(want, got any, o Options) []string {
	var out []string
	if o.Extras == nil {
		o.Extras = map[string]bool{}
	}
	diff("", want, got, o, &out)
	sort.Strings(out)
	return out
}

func masked(o Options, path string) bool { return o.Mask != nil && o.Mask(path) }

func diff(path string, want, got any, o Options, out *[]string) {
	if masked(o, path) {
		return
	}
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			*out = append(*out, fmt.Sprintf("%s: want object, got %T", path, got))
			return
		}
		for k, wv := range w {
			gv, has := g[k]
			if !has {
				*out = append(*out, fmt.Sprintf("%s.%s: missing", path, k))
				continue
			}
			diff(path+"."+k, wv, gv, o, out)
		}
		for k := range g {
			if _, has := w[k]; has || masked(o, path+"."+k) {
				continue
			}
			if o.Tolerated[k] && o.TolerateUnder != "" && strings.HasPrefix(path, o.TolerateUnder) {
				o.Extras[k] = true
				continue
			}
			*out = append(*out, fmt.Sprintf("%s.%s: unexpected key", path, k))
		}
	case []any:
		g, ok := got.([]any)
		if !ok {
			*out = append(*out, fmt.Sprintf("%s: want array, got %T", path, got))
			return
		}
		if len(w) != len(g) {
			*out = append(*out, fmt.Sprintf("%s: length %d, want %d", path, len(g), len(w)))
			return
		}
		for i := range w {
			diff(fmt.Sprintf("%s[%d]", path, i), w[i], g[i], o, out)
		}
	default:
		wb, _ := json.Marshal(want)
		gb, _ := json.Marshal(got)
		if string(wb) != string(gb) {
			*out = append(*out, fmt.Sprintf("%s: got %s, want %s", path, gb, wb))
		}
	}
}
