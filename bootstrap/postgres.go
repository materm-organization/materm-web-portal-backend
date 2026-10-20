package bootstrap

import (
	"fmt"
	"strings"
	"time"

	"go-fiber/pkg/logs"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

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
		parts = append(parts, "TimeZone="+dsnQuote(c.Timezone))
	}
	return strings.Join(parts, " ")
}

func dsnQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return "'" + s + "'"
}

// ************************Postgres Connection ***************************
type PostgresConnection struct {
	db *gorm.DB
}

// NewPostgresConnection opens one PostgreSQL connection and applies the pool
// settings. Non-positive pool values are skipped so database/sql defaults apply.
func NewPostgresConnection(conn DBConnection, pool PoolConfig) (*PostgresConnection, error) {
	db, err := gorm.Open(postgres.Open(conn.DSN()), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect postgres database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get postgres database connection: %w", err)
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

	logs.Info("postgres database connection success")
	return &PostgresConnection{db: db}, nil
}

func (m *PostgresConnection) Open() *gorm.DB {
	return m.db
}

func (m *PostgresConnection) Close() {
	sqlDB, err := m.db.DB()
	if err != nil {
		logs.Error(fmt.Sprintf("failed to get postgres database connection: %v", err))
		return
	}
	_ = sqlDB.Close()
}

// Migrate auto-migrates the given GORM models. Not called on startup.
func (m *PostgresConnection) Migrate(models ...any) error {
	if err := m.db.AutoMigrate(models...); err != nil {
		return fmt.Errorf("migration failed: %w", err)
	}
	logs.Info("Migrate successfully")
	return nil
}
