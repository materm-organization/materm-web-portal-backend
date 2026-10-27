//go:build dbtest

package dbtest

import (
	"context"
	"strings"
	"testing"
)

func TestNewCreatesAndDropsScratchDatabase(t *testing.T) {
	var name string
	t.Run("create", func(t *testing.T) {
		d := New(t)
		name = d.Name
		if !strings.HasPrefix(name, "materm_test_") {
			t.Fatalf("unexpected database name %q", name)
		}
		var cur, marker string
		if err := d.SQL.QueryRowContext(context.Background(),
			`select current_database(), current_setting('materm.scratch')`).Scan(&cur, &marker); err != nil {
			t.Fatal(err)
		}
		if cur != name || marker != "on" {
			t.Fatalf("connected to %q marker %q", cur, marker)
		}
	})
	// after the subtest's cleanup the database must be gone
	d := New(t)
	var n int
	if err := d.SQL.QueryRowContext(context.Background(),
		`select count(*) from pg_database where datname = $1`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("database %s was not dropped", name)
	}
}
