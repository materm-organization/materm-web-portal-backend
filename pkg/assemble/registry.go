package assemble

import (
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strconv"

	"github.com/materm-organization/materm-web-portal-backend/pkg/localize"
)

// schemaFS holds the 12 section schemas of doc/content-schemas. The portal role cannot read
// cms_section_types, so the schemas travel with the code (doc/08 section 2.4); a test compares these copies
// with ../doc/content-schemas whenever that folder exists.
//
//go:embed schemas/*.json
var schemaFS embed.FS

// sectionType is one registered section type: its compiled schema (what is a locale map, an image, a link,
// rich text) and the resolver that adds `resolved`.
type sectionType struct {
	Key     string
	Version int
	plan    *node
	hints   *localize.Hints
	resolve resolverFunc
}

// Registry maps key@version to a sectionType. It is immutable after construction.
type Registry struct {
	types map[string]*sectionType
}

func typeID(key string, version int) string { return key + "@" + strconv.Itoa(version) }

func (r *Registry) lookup(key string, version int) *sectionType { return r.types[typeID(key, version)] }

// Keys lists the registered key@version ids, sorted.
func (r *Registry) Keys() []string {
	out := make([]string, 0, len(r.types))
	for id := range r.types {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// HintPaths returns the localize path hints of key@version (tests and the drift check use it).
func (r *Registry) HintPaths(key string, version int) ([]string, bool) {
	t := r.lookup(key, version)
	if t == nil {
		return nil, false
	}
	return t.plan.hintPaths(), true
}

// NewRegistry builds the registry from the embedded schemas and the resolver table. A schema that does not
// compile, or a resolver for an unknown key, is an error (the process refuses to start: a programming mistake).
func NewRegistry() (*Registry, error) {
	entries, err := schemaFS.ReadDir("schemas")
	if err != nil {
		return nil, err
	}
	reg := &Registry{types: map[string]*sectionType{}}
	for _, e := range entries {
		b, err := schemaFS.ReadFile(path.Join("schemas", e.Name()))
		if err != nil {
			return nil, err
		}
		var doc struct {
			Key        string         `json:"key"`
			Version    int            `json:"version"`
			JSONSchema map[string]any `json:"json_schema"`
		}
		if err = json.Unmarshal(b, &doc); err != nil {
			return nil, fmt.Errorf("assemble: %s: %w", e.Name(), err)
		}
		if doc.Key == "" || doc.Version < 1 || doc.JSONSchema == nil {
			return nil, fmt.Errorf("assemble: %s: key, version and json_schema are required", e.Name())
		}
		plan, err := compileSchema(doc.JSONSchema)
		if err != nil {
			return nil, fmt.Errorf("assemble: %s: %w", e.Name(), err)
		}
		hints, err := localize.NewHints(plan.hintPaths()...)
		if err != nil {
			return nil, fmt.Errorf("assemble: %s: %w", e.Name(), err)
		}
		rf, ok := resolvers[doc.Key]
		if !ok {
			return nil, fmt.Errorf("assemble: section type %q has no resolver entry", doc.Key)
		}
		reg.types[typeID(doc.Key, doc.Version)] = &sectionType{Key: doc.Key, Version: doc.Version, plan: plan, hints: hints, resolve: rf}
	}
	for key := range resolvers {
		found := false
		for _, t := range reg.types {
			if t.Key == key {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("assemble: resolver %q has no schema", key)
		}
	}
	return reg, nil
}

// MustRegistry is NewRegistry for package-level use.
func MustRegistry() *Registry {
	r, err := NewRegistry()
	if err != nil {
		panic(err)
	}
	return r
}
