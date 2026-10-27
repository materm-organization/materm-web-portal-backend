package bootstrap

import (
	"context"
	"fmt"
	"github.com/materm-organization/materm-web-portal-backend/pkg/cache"
	constants "github.com/materm-organization/materm-web-portal-backend/pkg/constants/services"
	"github.com/materm-organization/materm-web-portal-backend/pkg/logs"
	"time"

	"github.com/redis/go-redis/v9"
)

func InitializeRedis(cfg RedisConfig) *redis.Client {
	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Password: cfg.Password,
		DB:       cfg.DB,
		Username: cfg.Username, // add user aname
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := rdb.Ping(ctx).Result()
	if err != nil {
		logs.Error(fmt.Sprintf("can not connect to redis: %v", err))
		return nil
	}

	if cfg.ClearOnStart {
		cacheRepo := cache.NewRedisRepository(rdb)
		clearCtx, clearCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer clearCancel()

		prefixes := []string{constants.AppKeyPrefix}
		if err := cacheRepo.DeleteByPrefix(clearCtx, prefixes); err != nil {
			logs.Error(fmt.Sprintf("failed to clear cache prefix %s: %v", prefixes, err))
		} else {
			logs.Info(fmt.Sprintf("cleared cache with prefix: %s", prefixes))
		}
	}

	logs.Info(fmt.Sprintf("redis connection success: %s", result))
	return rdb
}
