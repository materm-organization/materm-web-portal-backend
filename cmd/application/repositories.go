package application

import (
	"go-fiber/bootstrap"
	"go-fiber/pkg/cache"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// Repositories holds shared infrastructure handles. The auth module is
// stateless (local JWT) and uses none of them yet; new modules add their
// repositories here.
type Repositories struct {
	postgres  *bootstrap.PostgresConnection
	DB        *gorm.DB
	CacheRepo cache.RedisRepository
}

// SetupRepositories connects only the `admin` database; the sell, erp and device
// connections are parsed from config but not opened yet.
func SetupRepositories(db bootstrap.DatabaseConfig, rdb *redis.Client) *Repositories {
	conn, err := bootstrap.NewPostgresConnection(db.Admin, db.Pool)
	if err != nil {
		panic(err)
	}
	gormDB := conn.Open()

	cacheRepo := cache.NewRedisRepository(rdb)

	// migrate database
	// _ = conn.Migrate(&YourEntity{})

	return &Repositories{
		postgres:  conn,
		DB:        gormDB,
		CacheRepo: cacheRepo,
	}
}

func CloseRepositories(repos *Repositories) {
	if repos.postgres != nil {
		repos.postgres.Close()
	}
}
