package application

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/materm-organization/materm-web-portal-backend/bootstrap"
	"github.com/materm-organization/materm-web-portal-backend/pkg/cache"
	"github.com/materm-organization/materm-web-portal-backend/pkg/logs"
)

// Repositories holds the shared infrastructure handles. Module repositories (PB-3 and later) are added
// here. The portal only READS: there is no migration, no seed and no write through GORM models.
type Repositories struct {
	postgres *bootstrap.PostgresConnection
	DB       *gorm.DB
	// Redis is nil when Redis was unreachable in dev (degraded start).
	Redis     *redis.Client
	CacheRepo cache.RedisRepository
}

// SetupRepositories opens the one PostgreSQL connection (database.admin, which for the portal is the
// read-only role cms_portal_ro) and checks that the role is the intended one: outside dev a superuser, the
// database owner or a role that can read the draft tables is refused (CheckRuntimeRole). rdb may be nil.
// On an error nothing stays open.
func SetupRepositories(ctx context.Context, env *bootstrap.Env, rdb *redis.Client) (*Repositories, error) {
	conn, err := bootstrap.NewPostgresConnection(env.Database.Admin, env.Database.Pool)
	if err != nil {
		return nil, err
	}
	db := conn.Open()
	warnings, err := bootstrap.CheckRuntimeRole(ctx, db, env.Strict())
	for _, w := range warnings {
		logs.Warn("database: " + w)
	}
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("database role: %w", err)
	}
	return &Repositories{postgres: conn, DB: db, Redis: rdb, CacheRepo: cache.NewRedisRepository(rdb)}, nil
}

// CloseRepositories closes the database pool. Redis is closed by whoever created it.
func CloseRepositories(repos *Repositories) {
	if repos == nil || repos.postgres == nil {
		return
	}
	if err := repos.postgres.Close(); err != nil {
		logs.Error("closing the database pool failed", logs.Err(err))
	}
}
