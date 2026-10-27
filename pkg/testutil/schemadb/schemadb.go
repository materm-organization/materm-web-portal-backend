// Package schemadb builds the CMS schema on the scratch PostgreSQL for the portal's database tests.
//
// The portal never migrates (the admin backend owns the schema), so its tests need the schema from somewhere
// else: the FULL reference SQL of the design, ../doc/sql/migrations/*.sql plus the seeds. This package
// applies exactly those files, in order, as the owner role cms_migrator (SET ROLE on the scratch superuser's
// connection), loads a small fixture (fixture.sql) so that every view has rows, and then hands out a second
// connection as a read-only role that has exactly the privileges of cms_portal_ro: a throw-away LOGIN role
// that is a member of cms_portal_ro and nothing else. The tests therefore prove what the production role
// can and cannot do (views yes, base tables no).
//
// Everything refuses to run anywhere but the scratch cluster (pkg/testutil/dbtest guards: never the database
// `materm`, never port 5432). The reference SQL is looked up relative to the module root
// (../doc/sql); Shared skips the test when it is missing.
package schemadb

import (
	"context"
	"crypto/rand"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/dbtest"
)

//go:embed fixture.sql
var fixtureSQL string

// Roles of the reference SQL (000_roles_bootstrap.sql).
const (
	migratorRole = "cms_migrator"
	portalRole   = "cms_portal_ro"
	adminRole    = "cms_admin_rw"
)

// Schema is a scratch database with the full CMS schema, seeds and fixture.
type Schema struct {
	// DB is the database (owner = cms_migrator, pools as the scratch superuser).
	DB *dbtest.Database
	// Owner is a pool whose every connection runs with SET ROLE cms_migrator: the owner of every object.
	Owner *sql.DB
	// RO is a pool as the read-only role; ROUser is its name; RODSN its DSN (a secret: never log it).
	RO     *sql.DB
	ROUser string
	RODSN  string

	roPassword string
	gormRO     *gorm.DB
}

// GormRO returns a GORM handle on the read-only pool. Warn-level logger, parameters hidden.
func (s *Schema) GormRO(t testing.TB) *gorm.DB {
	t.Helper()
	if s.gormRO == nil {
		db, err := gorm.Open(postgres.New(postgres.Config{Conn: s.RO}), &gorm.Config{Logger: logger.Discard})
		if err != nil {
			t.Fatalf("schemadb: gorm open: %v", err)
		}
		s.gormRO = db
	}
	return s.gormRO
}

var (
	mu     sync.Mutex
	shared *Schema
	failed error
)

// Shared returns the one Schema of this test binary, building it on first use (a few seconds). It skips the
// test when MATERM_TEST_DSN is unset or the reference SQL is missing, and fails it when the build fails.
// Call Teardown from TestMain after m.Run() to drop the database and the role.
func Shared(t testing.TB) *Schema {
	t.Helper()
	if os.Getenv(dbtest.EnvDSN) == "" {
		t.Skipf("%s is not set: run `make testdb-up` and use `make test-db`", dbtest.EnvDSN)
	}
	root, err := referenceDir()
	if err != nil {
		t.Skipf("schemadb: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if failed != nil {
		t.Fatalf("schemadb: the schema could not be built earlier: %v", failed)
	}
	if shared == nil {
		s, err := build(root)
		if err != nil {
			failed = err
			if s != nil {
				s.close()
			}
			t.Fatalf("schemadb: %v", err)
		}
		shared = s
	}
	return shared
}

// Teardown drops the shared database and its role. Safe to call when nothing was built.
func Teardown() {
	mu.Lock()
	defer mu.Unlock()
	if shared != nil {
		shared.close()
		shared = nil
	}
}

// referenceDir finds ../doc/sql relative to the module root (the directory that holds go.mod).
func referenceDir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for dir := wd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			ref := filepath.Join(filepath.Dir(dir), "doc", "sql")
			if _, err := os.Stat(filepath.Join(ref, "migrations")); err != nil {
				return "", fmt.Errorf("the reference SQL %s is not present", ref)
			}
			return ref, nil
		}
		if dir == filepath.Dir(dir) {
			return "", fmt.Errorf("go.mod not found above %s", wd)
		}
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failing is unrecoverable
	}
	return hex.EncodeToString(b)
}

func build(ref string) (*Schema, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	d, err := dbtest.Create()
	s := &Schema{DB: d}
	if err != nil {
		return s, err
	}
	if err = ensureRoles(ctx, d, ref); err != nil {
		return s, err
	}
	q := `"` + d.Name + `"`
	for _, stmt := range []string{
		`alter database ` + q + ` owner to ` + migratorRole,
		`revoke all on database ` + q + ` from public`,
		`grant connect on database ` + q + ` to ` + adminRole + `, ` + portalRole,
	} {
		if _, err = d.SQL.ExecContext(ctx, stmt); err != nil {
			return s, fmt.Errorf("prepare database: %w", err)
		}
	}

	// the owner pool: every connection of it runs as cms_migrator
	ownerDSN, err := withParam(d.DSN, "options", "-c role="+migratorRole)
	if err != nil {
		return s, err
	}
	if err = dbtest.Guard(ownerDSN); err != nil {
		return s, err
	}
	if s.Owner, err = sql.Open("pgx", ownerDSN); err != nil {
		return s, fmt.Errorf("open owner pool: %v", scrub(err, d.DSN))
	}
	s.Owner.SetMaxOpenConns(2)

	conn, err := s.Owner.Conn(ctx) // ONE session for all files: the seeds' `set cms.live_write` must persist
	if err != nil {
		return s, fmt.Errorf("owner connection: %v", scrub(err, d.DSN))
	}
	defer conn.Close()

	migs, err := filepath.Glob(filepath.Join(ref, "migrations", "2*.sql"))
	if err != nil || len(migs) == 0 {
		return s, fmt.Errorf("no migrations under %s", ref)
	}
	sort.Strings(migs)
	for _, f := range migs {
		if strings.Contains(filepath.Base(f), "_000_") {
			continue // roles: cluster-wide, done by ensureRoles
		}
		if err = execFile(ctx, conn, f); err != nil {
			return s, err
		}
	}
	// seeds: locales, settings/menus/pages, taxonomy/faq/forms, ui strings. 002 (IAM catalog) is not needed.
	for _, name := range []string{"001_locales.sql", "004_settings_menus_pages.sql", "005_taxonomy_faq_forms.sql", "006_ui_strings_sample.sql"} {
		if err = execFile(ctx, conn, filepath.Join(ref, "seeds", name)); err != nil {
			return s, err
		}
	}
	if err = seedSectionTypes(ctx, conn, filepath.Join(ref, "seeds", "section_types")); err != nil {
		return s, err
	}
	if _, err = conn.ExecContext(ctx, fixtureSQL); err != nil {
		return s, fmt.Errorf("fixture.sql: %w", err)
	}

	return s, s.createReadOnlyRole(ctx)
}

// execFile runs one SQL file (many statements, $$ bodies) over the simple protocol: no arguments.
func execFile(ctx context.Context, conn *sql.Conn, path string) error {
	b, err := os.ReadFile(path) //nolint:gosec // a fixed reference file of the repository tree
	if err != nil {
		return err
	}
	if _, err = conn.ExecContext(ctx, string(b)); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return nil
}

// seedSectionTypes does what 003_section_types.psql does with psql meta-commands: every JSON file is the
// payload of cms_upsert_section_type.
func seedSectionTypes(ctx context.Context, conn *sql.Conn, dir string) error {
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(files) == 0 {
		return fmt.Errorf("no section type JSON under %s", dir)
	}
	sort.Strings(files)
	for _, f := range files {
		b, err := os.ReadFile(f) //nolint:gosec // fixed reference files
		if err != nil {
			return err
		}
		var id int64
		if err = conn.QueryRowContext(ctx, `select cms_upsert_section_type($1::jsonb)`, string(b)).Scan(&id); err != nil {
			return fmt.Errorf("section type %s: %w", filepath.Base(f), err)
		}
	}
	return nil
}

// ensureRoles makes sure the three cluster-wide roles of 000_roles_bootstrap.sql exist. The scratch cluster is
// shared with other test packages that may do the same at the same moment, so the check and the idempotent
// DO block run under an advisory lock taken on the maintenance database (advisory locks are per database,
// and every caller uses that one).
func ensureRoles(ctx context.Context, d *dbtest.Database, ref string) error {
	cluster, err := sql.Open("pgx", d.ClusterDSN())
	if err != nil {
		return fmt.Errorf("open cluster: %v", scrub(err, d.ClusterDSN()))
	}
	defer cluster.Close()
	conn, err := cluster.Conn(ctx)
	if err != nil {
		return fmt.Errorf("cluster connection: %v", scrub(err, d.ClusterDSN()))
	}
	defer conn.Close()
	const lockKey = 727_001_000
	if _, err = conn.ExecContext(ctx, `select pg_advisory_lock($1)`, lockKey); err != nil {
		return err
	}
	defer func() { _, _ = conn.ExecContext(context.WithoutCancel(ctx), `select pg_advisory_unlock($1)`, lockKey) }()

	var n int
	if err = conn.QueryRowContext(ctx, `select count(*) from pg_roles where rolname in ($1, $2, $3)`, migratorRole, adminRole, portalRole).Scan(&n); err != nil {
		return err
	}
	if n == 3 {
		return nil
	}
	return execFile(ctx, conn, filepath.Join(ref, "migrations", "20261021_000_roles_bootstrap.sql"))
}

// createReadOnlyRole makes the LOGIN role the tests connect as: a member of cms_portal_ro, nothing else, with
// the production statement_timeout. Membership (INHERIT) gives it exactly cms_portal_ro's privileges.
func (s *Schema) createReadOnlyRole(ctx context.Context) error {
	s.ROUser = "materm_test_ro_" + randomHex(5)
	s.roPassword = randomHex(16)
	cluster, err := sql.Open("pgx", s.DB.ClusterDSN())
	if err != nil {
		return fmt.Errorf("open cluster: %v", scrub(err, s.DB.ClusterDSN()))
	}
	defer cluster.Close()
	for _, stmt := range []string{
		fmt.Sprintf(`create role %q login inherit password '%s'`, s.ROUser, s.roPassword),
		fmt.Sprintf(`grant %s to %q`, portalRole, s.ROUser),
		fmt.Sprintf(`alter role %q set statement_timeout = '3s'`, s.ROUser),
	} {
		if _, err = cluster.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("create read-only role: %v", scrub(err, s.roPassword))
		}
	}
	u, err := url.Parse(s.DB.DSN)
	if err != nil {
		return err
	}
	u.User = url.UserPassword(s.ROUser, s.roPassword)
	s.RODSN = u.String()
	if err = dbtest.Guard(s.RODSN); err != nil {
		return err
	}
	if s.RO, err = sql.Open("pgx", s.RODSN); err != nil {
		return fmt.Errorf("open read-only pool: %v", scrub(err, s.RODSN))
	}
	s.RO.SetMaxOpenConns(8)
	if err = s.RO.PingContext(ctx); err != nil {
		return fmt.Errorf("connect as the read-only role: %v", scrub(err, s.RODSN))
	}
	return nil
}

// close closes the pools, drops the database and then the role (LIFO: the role owns nothing, but the
// database holds its CONNECT grant).
func (s *Schema) close() {
	if s.RO != nil {
		_ = s.RO.Close()
	}
	if s.Owner != nil {
		_ = s.Owner.Close()
	}
	if s.DB == nil {
		return
	}
	cluster := s.DB.ClusterDSN()
	s.DB.Drop()
	if s.ROUser != "" && strings.HasPrefix(s.ROUser, "materm_test_ro_") {
		if db, err := sql.Open("pgx", cluster); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			_, _ = db.ExecContext(ctx, fmt.Sprintf(`drop role if exists %q`, s.ROUser))
			cancel()
			_ = db.Close()
		}
	}
}

func withParam(dsn, key, val string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set(key, val)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// scrub removes a secret from an error text.
func scrub(err error, secret string) string {
	msg := err.Error()
	if secret != "" {
		msg = strings.ReplaceAll(msg, secret, "<redacted>")
	}
	return msg
}
