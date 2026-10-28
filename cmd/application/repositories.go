package application

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/materm-organization/materm-web-portal-backend/bootstrap"
	contentrepositories "github.com/materm-organization/materm-web-portal-backend/modules/content/repositories"
	formsrepositories "github.com/materm-organization/materm-web-portal-backend/modules/forms/repositories"
	previewrepositories "github.com/materm-organization/materm-web-portal-backend/modules/preview/repositories"
	"github.com/materm-organization/materm-web-portal-backend/pkg/cache"
	"github.com/materm-organization/materm-web-portal-backend/pkg/logs"
	"github.com/materm-organization/materm-web-portal-backend/pkg/media"
	"github.com/materm-organization/materm-web-portal-backend/pkg/publiccache"
	"github.com/materm-organization/materm-web-portal-backend/pkg/rediskeys"
)

// Repositories holds the shared infrastructure handles. Module repositories (PB-3 and later) are added
// here. The portal only READS: there is no migration, no seed and no write through GORM models.
type Repositories struct {
	postgres *bootstrap.PostgresConnection
	DB       *gorm.DB
	// Redis is nil when Redis was unreachable in dev (degraded start).
	Redis     *redis.Client
	CacheRepo cache.RedisRepository
	// Content reads the 21 public views (modules/content, PB-3): the repositories every content module uses.
	Content *contentrepositories.Set
	// PublicCache is the response cache (PB-4): generation + tag sets in Redis, a direct database read when
	// Redis is missing or cache.enabled is false.
	PublicCache *publiccache.Cache
	// Submissions is the one write the portal makes: forms_submit_form (db.Raw, EXECUTE only), used by P-15.
	Submissions formsrepositories.SubmissionRepository
	// PreviewTokens calls cms_consume_preview_token (db.Raw, EXECUTE only), used by P-19.
	PreviewTokens previewrepositories.TokenRepository
	// RC is Redis as the interface every consumer takes (the cache, the rate limiter, the IP hash, the duplicate
	// marker). It is a NIL interface when Redis is absent, never a typed-nil *redis.Client (a nil pointer in an
	// interface is not == nil and would be called).
	RC redis.Cmdable
	// Keys names every Redis key under the service prefix (materm-portal:).
	Keys rediskeys.Keys
	// Media builds public media URLs from file.path_ip and media.staticPrefix (PB-3).
	Media *media.Builder
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
	rc := publiccache.FromClient(rdb)
	keys := rediskeys.New("")
	return &Repositories{
		postgres: conn, DB: db, Redis: rdb, CacheRepo: cache.NewRedisRepository(rdb),
		Content:       contentrepositories.New(db),
		Submissions:   formsrepositories.New(db),
		PreviewTokens: previewrepositories.New(db),
		RC:            rc,
		Keys:          keys,
		Media:         media.NewBuilder(media.Config{PathIP: env.File.PathIP, StaticPrefix: env.Media.StaticPrefix}),
		PublicCache: publiccache.New(publiccache.Options{
			Client: rc, Keys: keys, Enabled: env.Cache.Enabled, TTL: env.Cache.TTL,
		}),
	}, nil
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
