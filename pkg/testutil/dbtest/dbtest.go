package dbtest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Database is one freshly created database on the scratch cluster.
type Database struct {
	// Name is the database name (materm_test_<random>).
	Name string
	// DSN connects to Name as the scratch superuser (URL form). Treat as a secret: never log it.
	DSN string
	// SQL is a pool on Name as the scratch superuser.
	SQL *sql.DB

	clusterDSN string
}

// ClusterDSN returns the DSN of the scratch cluster (the maintenance database), for statements that are
// cluster-wide (roles). Treat as a secret.
func (d *Database) ClusterDSN() string { return d.clusterDSN }

// New creates materm_test_<random> on the scratch cluster named by MATERM_TEST_DSN and registers a
// cleanup that drops it. It skips the test when MATERM_TEST_DSN is unset (run `make testdb-up`) and
// fails the test when the guard refuses the target.
func New(t testing.TB) *Database {
	t.Helper()

	dsn := os.Getenv(EnvDSN)
	if dsn == "" {
		t.Skipf("%s is not set: run `make testdb-up` and use `make test-db`", EnvDSN)
	}
	d, err := Create()
	if d != nil {
		t.Cleanup(d.Drop) // registered before anything else can fail
	}
	if err != nil {
		t.Fatalf("%v", err)
	}
	return d
}

// Create is New without a testing.TB: it creates materm_test_<random> on the scratch cluster named by
// MATERM_TEST_DSN (all guards apply) for callers that share one database across a whole test binary
// (TestMain). The caller must call Drop. When the database was created but a later step failed, both a
// non-nil *Database (so Drop can remove it) and the error are returned.
func Create() (*Database, error) {
	dsn := os.Getenv(EnvDSN)
	if dsn == "" {
		return nil, fmt.Errorf("dbtest: %s is not set", EnvDSN)
	}
	if err := Guard(dsn); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("dbtest: open scratch cluster: %s", redact(err, dsn))
	}
	defer admin.Close()
	if err = CheckScratchCluster(ctx, admin); err != nil {
		return nil, err
	}

	name := "materm_test_" + randomHex(6)
	if _, err = admin.ExecContext(ctx, `create database `+quoteIdent(name)); err != nil {
		return nil, fmt.Errorf("dbtest: create database: %s", redact(err, dsn))
	}
	d := &Database{Name: name, clusterDSN: dsn}

	childDSN, err := withDatabase(dsn, name)
	if err != nil {
		return d, fmt.Errorf("dbtest: build DSN: %w", err)
	}
	if err = Guard(childDSN); err != nil {
		return d, err
	}
	pool, err := sql.Open("pgx", childDSN)
	if err != nil {
		return d, fmt.Errorf("dbtest: open %s: %s", name, redact(err, dsn))
	}
	if err := pool.PingContext(ctx); err != nil {
		_ = pool.Close()
		return d, fmt.Errorf("dbtest: ping %s: %s", name, redact(err, dsn))
	}

	d.DSN, d.SQL = childDSN, pool
	return d, nil
}

// Drop closes the pool and drops the database (only ever a materm_test_* one).
func (d *Database) Drop() { d.drop(d.clusterDSN) }

// Gorm opens a GORM handle on the database. cfg may be nil.
func (d *Database) Gorm(t testing.TB, cfg *gorm.Config) *gorm.DB {
	t.Helper()
	if cfg == nil {
		cfg = &gorm.Config{}
	}
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: d.SQL}), cfg)
	if err != nil {
		t.Fatalf("dbtest: gorm open: %v", err)
	}
	return db
}

func (d *Database) drop(clusterDSN string) {
	if d.SQL != nil {
		_ = d.SQL.Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := sql.Open("pgx", clusterDSN)
	if err != nil {
		return
	}
	defer admin.Close()
	// Refuse to drop anything that is not one of ours.
	if !strings.HasPrefix(d.Name, "materm_test_") {
		return
	}
	_, _ = admin.ExecContext(ctx, `drop database if exists `+quoteIdent(d.Name)+` with (force)`)
}

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failing is unrecoverable
	}
	return hex.EncodeToString(b)
}

// withDatabase swaps the database name in a URL-form DSN. scripts/testdb.sh always writes URL form.
func withDatabase(dsn, name string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return "", fmt.Errorf("MATERM_TEST_DSN must be a postgres:// URL")
	}
	u.Path = "/" + name
	return u.String(), nil
}

// redact removes the DSN (and so the password) from an error message.
func redact(err error, dsn string) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if dsn != "" {
		msg = strings.ReplaceAll(msg, dsn, "<dsn>")
	}
	if u, perr := url.Parse(dsn); perr == nil && u.User != nil {
		if pw, ok := u.User.Password(); ok && pw != "" {
			msg = strings.ReplaceAll(msg, pw, "<password>")
		}
	}
	return msg
}
