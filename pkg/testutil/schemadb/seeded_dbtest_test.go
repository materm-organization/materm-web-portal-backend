//go:build dbtest

package schemadb

import (
	"context"
	"testing"
)

// Seeded must hand out the production privileges on an externally prepared database. The "external" database
// here is the one Shared built (same shape as what the admin Migrate() leaves: roles, views, grants).
func TestSeededOpensAnExternalDatabaseWithTheReadOnlyRole(t *testing.T) {
	base := Shared(t)
	t.Setenv(EnvSeededDSN, base.DB.DSN)
	s := Seeded(t)
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		if seeded != nil {
			seeded.close()
			seeded = nil
		}
	})
	if s.DB != nil || s.ROUser == "" || s.ROUser == base.ROUser {
		t.Fatalf("unexpected schema: %+v", s)
	}
	ctx := context.Background()
	var n int
	if err := s.RO.QueryRowContext(ctx, `select count(*) from v_public_locales`).Scan(&n); err != nil || n == 0 {
		t.Fatalf("the read-only role must read the public views: %d %v", n, err)
	}
	if err := s.RO.QueryRowContext(ctx, `select count(*) from cms_pages`).Scan(&n); err == nil {
		t.Fatal("the read-only role must NOT read base tables")
	}
	if err := s.Owner.QueryRowContext(ctx, `select count(*) from cms_pages`).Scan(&n); err != nil {
		t.Fatalf("the owner pool reads base tables: %v", err)
	}
}

func TestSeededRefusesTheDevelopersDatabase(t *testing.T) {
	t.Setenv(EnvSeededDSN, "postgres://u:p@127.0.0.1:5432/materm")
	mu.Lock()
	seededFailed = nil
	mu.Unlock()
	if _, err := openSeeded("postgres://u:p@127.0.0.1:5432/materm"); err == nil {
		t.Fatal("port 5432 / database materm must be refused")
	}
}
