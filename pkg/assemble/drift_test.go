package assemble

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The embedded schemas are copies of ../doc/content-schemas (the portal role cannot read cms_section_types).
// Whenever the design folder is next to this repository, they must still be equal to it.
func TestEmbeddedSchemasEqualTheDesignFolder(t *testing.T) {
	wd, _ := os.Getwd()
	var docDir string
	for dir := wd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			docDir = filepath.Join(filepath.Dir(dir), "doc", "content-schemas")
			break
		}
		if dir == filepath.Dir(dir) {
			t.Fatal("go.mod not found")
		}
	}
	if _, err := os.Stat(docDir); err != nil {
		t.Skipf("%s is not present: drift check skipped", docDir)
	}
	entries, err := schemaFS.ReadDir("schemas")
	if err != nil || len(entries) != 12 {
		t.Fatalf("embedded schemas: %d %v", len(entries), err)
	}
	for _, e := range entries {
		mine, _ := schemaFS.ReadFile("schemas/" + e.Name())
		theirs, err := os.ReadFile(filepath.Join(docDir, e.Name())) //nolint:gosec // fixed design files
		if err != nil {
			t.Errorf("%s: %v", e.Name(), err)
			continue
		}
		var a, b any
		if json.Unmarshal(mine, &a) != nil || json.Unmarshal(theirs, &b) != nil {
			t.Errorf("%s: not JSON", e.Name())
			continue
		}
		if !reflect.DeepEqual(a, b) {
			t.Errorf("%s: the embedded copy differs from doc/content-schemas: copy it again", e.Name())
		}
	}
}

func TestRegistryRefusesAnInconsistentTable(t *testing.T) {
	saved := resolvers["faq"]
	delete(resolvers, "faq")
	defer func() { resolvers["faq"] = saved }()
	if _, err := NewRegistry(); err == nil {
		t.Fatal("a schema without a resolver must be refused")
	}
	resolvers["faq"] = saved
	resolvers["ghost"] = resolveNone
	defer delete(resolvers, "ghost")
	if _, err := NewRegistry(); err == nil {
		t.Fatal("a resolver without a schema must be refused")
	}
}
