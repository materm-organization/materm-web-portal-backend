package bootstrap

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/materm-organization/materm-web-portal-backend/pkg/cache"
	constants "github.com/materm-organization/materm-web-portal-backend/pkg/constants/services"
	"github.com/materm-organization/materm-web-portal-backend/pkg/logs"
	"github.com/materm-organization/materm-web-portal-backend/pkg/redisguard"
)

// NewRedisClient builds the Redis client WITHOUT connecting (B1, fast-fail). One attempt per command with short
// timeouts, so that a Redis that is down costs milliseconds, and a circuit breaker (pkg/redisguard) on the
// client so that, while it is down, commands are refused without any network call; the library's own log is
// throttled (B4). Both traps of go-redis are handled here: MaxRetries 0 means "3" there (-1 = none) and
// DialerRetries 0 means "5" (it dials 5 times, 100 ms apart, per attempt: 4 x 5 x 100 ms was the 2.15 s of the
// outage run). cfg must have passed Defaults().
func NewRedisClient(cfg RedisConfig) *redis.Client {
	redisguard.InstallLibraryLogger()
	maxRetries := cfg.MaxRetries
	if maxRetries <= 0 {
		maxRetries = -1
	}
	rdb := redis.NewClient(&redis.Options{
		Addr:          fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Password:      cfg.Password,
		DB:            cfg.DB,
		Username:      cfg.Username,
		DialTimeout:   cfg.DialTimeout,
		ReadTimeout:   cfg.ReadTimeout,
		WriteTimeout:  cfg.WriteTimeout,
		MaxRetries:    maxRetries,
		DialerRetries: 1,
		// the pool sleeps this long after EVERY failed dial, also the last one: 0 would mean 100 ms
		DialerRetryTimeout: time.Millisecond,
		// a saturated pool must not stall a request for go-redis's default ReadTimeout + 1s
		PoolTimeout: cfg.ReadTimeout,
	})
	redisguard.Attach(rdb, cfg.BreakerCooldown)
	return rdb
}

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
	rdb := NewRedisClient(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		redisguard.Forget(rdb)
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
