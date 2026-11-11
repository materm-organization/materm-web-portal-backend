package assemble

import (
	"fmt"
	"sort"
	"strings"

	"github.com/materm-organization/materm-web-portal-backend/pkg/richtext"
)

// kind is what a node of a section schema means for the assembler. The walk is a port of `collapse()` of
// materm-web-portal-frontend/scripts/cms/build-seed.mjs, the reference the golden bodies were generated with.
type kind uint8

const (
	kPlain  kind = iota // copied unchanged
	kText               // x-localized string: a locale map (collapsed by pkg/localize)
	kRich               // x-localized + x-richtext: a locale map of rich text documents
	kImage              // $defs/image: {media_id, alt: locale map, decorative}
	kLink               // $defs/link: {type: page|url, ...}
	kObject             // object with properties
	kArray              // array with items
	kMedia              // integer with x-ref media_assets (a media id that is not under the key "media_id")
)

// node is the compiled form of one schema node.
type node struct {
	kind    kind
	profile richtext.Profile // kRich
	props   map[string]*node // kObject
	items   *node            // kArray
	// video is the x-media-kind "video" annotation of a kMedia node: the id must be a video asset (every other media slot takes anything but a video).
	video bool
}

const (
	refImage = "#/$defs/image"
	refLink  = "#/$defs/link"
	maxDepth = 16
)

// compileSchema turns a json_schema into a node tree. root is the whole schema (for $ref resolution).
func compileSchema(root map[string]any) (*node, error) {
	return compile(root, root, 0)
}

func compile(schema, root map[string]any, depth int) (*node, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("assemble: schema nested deeper than %d", maxDepth)
	}
	switch ref, _ := schema["$ref"].(string); ref {
	case refImage:
		return &node{kind: kImage}, nil
	case refLink:
		return &node{kind: kLink}, nil
	}
	s, err := deref(schema, root)
	if err != nil {
		return nil, err
	}
	if loc, _ := s["x-localized"].(bool); loc {
		if p, _ := s["x-richtext"].(string); p != "" {
			prof := richtext.Lite
			if p == string(richtext.Full) {
				prof = richtext.Full
			}
			return &node{kind: kRich, profile: prof}, nil
		}
		return &node{kind: kText}, nil
	}
	if it, ok := s["items"].(map[string]any); ok {
		child, err := compile(it, root, depth+1)
		if err != nil {
			return nil, err
		}
		return &node{kind: kArray, items: child}, nil
	}
	if props, ok := s["properties"].(map[string]any); ok {
		n := &node{kind: kObject, props: make(map[string]*node, len(props))}
		for k, v := range props {
			vm, ok := v.(map[string]any)
			if !ok {
				continue
			}
			child, err := compile(vm, root, depth+1)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", k, err)
			}
			n.props[k] = child
		}
		return n, nil
	}
	if ref, _ := s["x-ref"].(string); ref == "media_assets" {
		k, _ := s["x-media-kind"].(string)
		return &node{kind: kMedia, video: k == "video"}, nil
	}
	return &node{kind: kPlain}, nil
}

// deref follows "$ref": "#/$defs/<name>" and lays the siblings of the $ref over the target (the way the JS
// walker does: `{...target, ...rest}`).
func deref(schema, root map[string]any) (map[string]any, error) {
	cur := schema
	for i := 0; i < 8; i++ {
		ref, _ := cur["$ref"].(string)
		if ref == "" {
			return cur, nil
		}
		const prefix = "#/$defs/"
		if !strings.HasPrefix(ref, prefix) {
			return nil, fmt.Errorf("assemble: unsupported $ref %q", ref)
		}
		defs, _ := root["$defs"].(map[string]any)
		target, ok := defs[strings.TrimPrefix(ref, prefix)].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("assemble: unresolved $ref %q", ref)
		}
		merged := make(map[string]any, len(target)+len(cur))
		for k, v := range target {
			merged[k] = v
		}
		for k, v := range cur {
			if k != "$ref" {
				merged[k] = v
			}
		}
		cur = merged
	}
	return nil, fmt.Errorf("assemble: $ref chain too long")
}

// hintPaths lists the leaves that are locale maps in pkg/localize's path grammar ("cta.label",
// "images[].alt", "rows[].slots[]..."): text and rich text leaves, and the alt of every image placement.
func (n *node) hintPaths() []string {
	var out []string
	var walk func(path string, n *node)
	walk = func(path string, n *node) {
		switch n.kind {
		case kText, kRich:
			if path != "" {
				out = append(out, path)
			}
		case kImage:
			out = append(out, join(path, "alt"))
		case kObject:
			for k, c := range n.props {
				walk(join(path, k), c)
			}
		case kArray:
			if path == "" {
				return
			}
			// an array directly inside an array has no path in the hint grammar; the schemas have none
			if strings.HasSuffix(path, "[]") {
				return
			}
			walk(path+"[]", n.items)
		}
	}
	walk("", n)
	sort.Strings(out)
	return out
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}
