package bootstrap

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/materm-organization/materm-web-portal-backend/pkg/cache"
	constants "github.com/materm-organization/materm-web-portal-backend/pkg/constants/services"
	"github.com/materm-organization/materm-web-portal-backend/pkg/logs"
)

// InitializeRedis connects to Redis and pings it.
//
// When Redis cannot be reached: strict mode (staging/prod) returns an error and the server must not start;
// dev logs a warning and returns (nil, nil) - the caller then runs degraded with cache.Unavailable() and
// every cache/throttle/revoke path falls back to the database. A nil client is therefore never a silent
// accident: it is only returned together with that warning.
//
// redis.clearOnStart clears only the materm-portal: key prefix (never FLUSH*: the Redis is shared) and only
// outside strict mode; Env.Validate rejects it in staging/prod.
func InitializeRedis(cfg RedisConfig, strict bool) (*redis.Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Password: cfg.Password,
		DB:       cfg.DB,
		Username: cfg.Username,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		if strict {
			return nil, fmt.Errorf("redis: cannot connect to %s:%d: %w", cfg.Host, cfg.Port, err)
		}
		logs.Warn("redis unreachable: running degraded (cache, throttle and revoke checks use the database)",
			logs.String("target", fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)), logs.Err(err))
		return nil, nil
	}

	if cfg.ClearOnStart && !strict {
		clearCtx, clearCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer clearCancel()
		prefixes := []string{constants.AppKeyPrefix}
		if err := cache.NewRedisRepository(rdb).DeleteByPrefix(clearCtx, prefixes); err != nil {
			logs.Error("failed to clear cache prefix", logs.String("prefix", constants.AppKeyPrefix), logs.Err(err))
		} else {
			logs.Info("cleared cache with prefix", logs.String("prefix", constants.AppKeyPrefix))
		}
	}

	logs.Info("redis connection success", logs.String("target", fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)))
	return rdb, nil
}
