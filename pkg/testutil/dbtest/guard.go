// Package dbtest gives tests a throw-away PostgreSQL database on the scratch cluster
// started by scripts/testdb.sh. It refuses (hard) to run against anything else: the
// developer's own database (`materm`, port 5432) must never be touched by a test.
package dbtest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

const (
	// EnvDSN is the environment variable (written to .env.test by scripts/testdb.sh) that holds the
	// superuser DSN of the scratch cluster.
	EnvDSN = "MATERM_TEST_DSN"

	// scratchGUC is set on the scratch cluster with `postgres -c materm.scratch=on`.
	scratchGUC = "materm.scratch"

	// userPort is the port of the developer's own Postgres (postgres-maxdb).
	userPort = 5432
)

// forbiddenDatabases are database names no test may connect to.
var forbiddenDatabases = map[string]struct{}{"materm": {}}

// ErrRefused wraps every guard refusal so callers can match on it.
var ErrRefused = errors.New("dbtest: refused")

func refuse(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrRefused, fmt.Sprintf(format, args...))
}

// Guard is the static part of the safety net (no network): it parses the DSN exactly as the driver
// will (so environment defaults such as PGHOST/PGPORT count) and refuses when the effective target is
// the developer's database, port 5432, or the app's own configured database. Messages never contain
// the DSN or the password.
func Guard(dsn string) error {
	if strings.TrimSpace(dsn) == "" {
		return refuse("empty DSN")
	}
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil {
		return refuse("DSN is not parseable") // do not wrap err: it can echo the DSN
	}

	targets := []*pgconn.Config{cfg}
	targets = append(targets, fallbackConfigs(cfg)...)

	for _, t := range targets {
		if _, bad := forbiddenDatabases[strings.ToLower(t.Database)]; bad {
			return refuse("database %q is not a scratch database", t.Database)
		}
		if int(t.Port) == userPort {
			return refuse("port %d belongs to the developer's Postgres", t.Port)
		}
		if err := guardAgainstAppConfig(t.Host, int(t.Port), t.Database); err != nil {
			return err
		}
	}
	return nil
}

func fallbackConfigs(cfg *pgconn.Config) []*pgconn.Config {
	out := make([]*pgconn.Config, 0, len(cfg.Fallbacks))
	for _, f := range cfg.Fallbacks {
		c := *cfg
		c.Host, c.Port = f.Host, f.Port
		out = append(out, &c)
	}
	return out
}

// guardAgainstAppConfig refuses a target equal to the one the application itself is configured with
// (DATABASE_ADMIN_* variables, as documented in CLAUDE.md). Only the variable names are read here;
// values are compared, never printed.
func guardAgainstAppConfig(host string, port int, db string) error {
	appHost := os.Getenv("DATABASE_ADMIN_HOST")
	appPort, _ := strconv.Atoi(os.Getenv("DATABASE_ADMIN_PORT"))
	appDB := os.Getenv("DATABASE_ADMIN_DBNAME")
	if appDB != "" && strings.EqualFold(appDB, db) && sameHost(appHost, host) && (appPort == 0 || appPort == port) {
		return refuse("target equals DATABASE_ADMIN_* of the application")
	}
	if appHost != "" && appPort != 0 && sameHost(appHost, host) && appPort == port {
		return refuse("host:port equals DATABASE_ADMIN_HOST/PORT of the application")
	}
	return nil
}

func sameHost(a, b string) bool {
	if strings.EqualFold(a, b) {
		return true
	}
	return isLoopback(a) && isLoopback(b)
}

func isLoopback(h string) bool {
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// CheckScratchCluster is the dynamic part of the guard: the connected cluster must carry the GUC
// materm.scratch=on (set by `postgres -c materm.scratch=on` in scripts/testdb.sh) and the current
// database must not be a forbidden one. A cluster without the marker is refused.
func CheckScratchCluster(ctx context.Context, db *sql.DB) error {
	return checkScratch(func(dest ...any) error {
		return db.QueryRowContext(ctx, scratchQuery).Scan(dest...)
	})
}

const scratchQuery = `select coalesce(current_setting('` + scratchGUC + `', true), ''), current_database()`

func checkScratch(read func(dest ...any) error) error {
	var marker, current string
	if err := read(&marker, &current); err != nil {
		return refuse("cannot read the scratch marker of the cluster")
	}
	if marker != "on" {
		return refuse("cluster does not have %s=on (not a scratch cluster)", scratchGUC)
	}
	if _, bad := forbiddenDatabases[strings.ToLower(current)]; bad {
		return refuse("connected to forbidden database %q", current)
	}
	return nil
}
