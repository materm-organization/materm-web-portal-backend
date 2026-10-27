package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/materm-organization/materm-web-portal-backend/pkg/logs"
)

// plainZone matches a time zone name that needs no quoting (UTC, Asia/Bangkok, Etc/GMT+7, UTC+07:00).
var plainZone = regexp.MustCompile(`^[A-Za-z0-9_+:./-]+$`)

// DSN builds a libpq keyword/value connection string. Values are single-quoted
// and escaped so passwords containing spaces or quotes are safe; sslmode and
// timezone are omitted when empty.
func (c DBConnection) DSN() string {
	parts := []string{
		"host=" + dsnQuote(c.Host),
		fmt.Sprintf("port=%d", c.Port),
		"user=" + dsnQuote(c.User),
		"password=" + dsnQuote(c.Password),
		"dbname=" + dsnQuote(c.DBName),
	}
	if c.SSLMode != "" {
		parts = append(parts, "sslmode="+dsnQuote(c.SSLMode))
	}
	if c.Timezone != "" {
		// The gorm postgres driver re-reads the time zone from the DSN text with its own regexp and keeps the
		// quotes ("'Asia/Bangkok'" is then rejected by the server: invalid value for parameter "TimeZone"), so a
		// plain zone name is written unquoted; anything else is quoted like the other values.
		if plainZone.MatchString(c.Timezone) {
			parts = append(parts, "TimeZone="+c.Timezone)
		} else {
			parts = append(parts, "TimeZone="+dsnQuote(c.Timezone))
		}
	}
	return strings.Join(parts, " ")
}

func dsnQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return "'" + s + "'"
}

// PostgresConnection wraps a single GORM PostgreSQL connection. The portal only READS: it connects with the
// read-only role cms_portal_ro (doc/08 section 2.6), never migrates (there is no AutoMigrate, no SQL runner,
// no seed: the admin backend owns the schema) and writes only through the database function
// forms_submit_form().
type PostgresConnection struct {
	db *gorm.DB
}

// slowQueryThreshold is the duration above which GORM logs a query as slow.
const slowQueryThreshold = 500 * time.Millisecond

// newGormLogger logs warnings and errors only and never the bound parameter values: with the default Info
// level GORM prints every statement with its values (form submissions carry personal data).
func newGormLogger() logger.Interface { return newGormLoggerTo(os.Stdout) }

func newGormLoggerTo(w io.Writer) logger.Interface {
	base := logger.New(log.New(w, "", log.LstdFlags), logger.Config{
		SlowThreshold:             slowQueryThreshold,
		LogLevel:                  logger.Warn,
		IgnoreRecordNotFoundError: true,
		ParameterizedQueries:      true,
		Colorful:                  false,
	})
	return paramsFilterLogger{base}
}

// paramsFilterLogger forwards gorm.ParamsFilter explicitly: GORM finds it by type assertion on db.Logger, and an
// embedded interface only promotes its own methods. Without it ParameterizedQueries would silently stop
// working and bound values would appear in slow-query and error log lines. LogMode keeps the wrapper when GORM
// derives a logger with another level (db.Debug(), Session).
type paramsFilterLogger struct{ logger.Interface }

func (l paramsFilterLogger) ParamsFilter(ctx context.Context, sql string, params ...any) (string, []any) {
	if f, ok := l.Interface.(gorm.ParamsFilter); ok {
		return f.ParamsFilter(ctx, sql, params...)
	}
	return sql, params
}

func (l paramsFilterLogger) LogMode(level logger.LogLevel) logger.Interface {
	return paramsFilterLogger{l.Interface.LogMode(level)}
}

// NewPostgresConnection opens one PostgreSQL connection from conn and applies the pool settings
// (non-positive values keep the database/sql defaults). It returns an error instead of exiting; the error
// never contains the DSN or the password.
func NewPostgresConnection(conn DBConnection, pool PoolConfig) (*PostgresConnection, error) {
	db, err := gorm.Open(postgres.Open(conn.DSN()), &gorm.Config{Logger: newGormLogger()})
	if err != nil {
		return nil, fmt.Errorf("postgres: connect to %s: %w", conn.Target(), sanitize(err, conn))
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("postgres: get database handle: %w", err)
	}
	if pool.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(pool.MaxOpenConns)
	}
	if pool.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(pool.MaxIdleConns)
	}
	if pool.ConnMaxLifetimeMinutes > 0 {
		sqlDB.SetConnMaxLifetime(time.Duration(pool.ConnMaxLifetimeMinutes) * time.Minute)
	}
	if pool.ConnMaxIdleTimeMinutes > 0 {
		sqlDB.SetConnMaxIdleTime(time.Duration(pool.ConnMaxIdleTimeMinutes) * time.Minute)
	}

	logs.Info("postgres database connection success", logs.String("target", conn.Target()))
	return &PostgresConnection{db: db}, nil
}

// sanitize removes the password from an error text (a driver may echo the connection parameters).
func sanitize(err error, conn DBConnection) error {
	if err == nil || conn.Password == "" {
		return err
	}
	msg := err.Error()
	clean := strings.ReplaceAll(msg, conn.Password, "<password>")
	if clean == msg {
		return err
	}
	return errors.New(clean)
}

// Open returns the GORM handle.
func (m *PostgresConnection) Open() *gorm.DB {
	return m.db
}

// Close closes the pool.
func (m *PostgresConnection) Close() error {
	sqlDB, err := m.db.DB()
	if err != nil {
		return fmt.Errorf("postgres: get database handle: %w", err)
	}
	return sqlDB.Close()
}

// RoleReport is what CheckRuntimeRole learns about the connected database role.
type RoleReport struct {
	CurrentUser   string
	Superuser     bool
	DatabaseOwner bool
	// CanReadCMSPages is true when the role holds SELECT on the base table cms_pages (false when the table
	// does not exist yet).
	CanReadCMSPages bool
}

// PortalRole is the database role the portal is meant to run as (doc/03 section 10, doc/08 section 2.6).
const PortalRole = "cms_portal_ro"

const roleQuery = `select current_user::text,
  coalesce((select rolsuper from pg_roles where rolname = current_user), false),
  coalesce((select pg_get_userbyid(datdba) = current_user from pg_database where datname = current_database()), false),
  coalesce(has_table_privilege(current_user, to_regclass('public.cms_pages'), 'select'), false)`

// CheckRuntimeRole guards against running the portal with a role that can write or read drafts (an owner, a
// superuser, cms_admin_rw). Strict (staging/prod): a superuser, the database owner or a role that can read
// cms_pages is an error. In dev each is a warning. A role other than cms_portal_ro is always a warning. The
// messages name the role-class, never a value other than the role name.
func CheckRuntimeRole(ctx context.Context, db *gorm.DB, strict bool) (warnings []string, err error) {
	var r RoleReport
	row := db.WithContext(ctx).Raw(roleQuery).Row()
	if scanErr := row.Scan(&r.CurrentUser, &r.Superuser, &r.DatabaseOwner, &r.CanReadCMSPages); scanErr != nil {
		return nil, fmt.Errorf("postgres: read the role of the connection: %w", scanErr)
	}
	var bad []string
	add := func(msg string) {
		if strict {
			bad = append(bad, msg)
		} else {
			warnings = append(warnings, msg)
		}
	}
	if r.Superuser {
		add("database.admin.user: the role is a superuser; the portal must connect as " + PortalRole)
	}
	if r.DatabaseOwner {
		add("database.admin.user: the role owns the database; the portal must connect as " + PortalRole)
	}
	if r.CanReadCMSPages {
		add("database.admin.user: the role can read the base table cms_pages (drafts); the portal must connect as " + PortalRole)
	}
	if r.CurrentUser != PortalRole {
		warnings = append(warnings, "database.admin.user: the connected role is not "+PortalRole)
	}
	if len(bad) > 0 {
		return warnings, &ConfigError{Problems: bad}
	}
	return warnings, nil
}
