package cache

import (
	"context"
	"encoding/json"
	"fmt"
	constants "github.com/materm-organization/materm-web-portal-backend/pkg/constants/services"
	"github.com/materm-organization/materm-web-portal-backend/pkg/logs"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisRepository interface {
	// Get retrieves a value from the cache by key
	Get(ctx context.Context, key, path string) (json.RawMessage, error)

	// Set stores a value in the cache with an optional expiration time
	Set(ctx context.Context, key string, path string, value interface{}, expiration time.Duration) error

	// Delete removes a value from the cache by key
	Delete(ctx context.Context, key string) error

	// Exists checks if a key exists in the cache
	Exists(ctx context.Context, key string) (bool, error)

	// Increment increments a numeric value in the cache
	Increment(ctx context.Context, key string) (int64, error)

	// Expire sets an expiration time on a key
	Expire(ctx context.Context, key string, expiration time.Duration) error

	DeleteByPrefix(ctx context.Context, prefixes []string) error

	DeleteAll(ctx context.Context) error

	InvalidateEntityCaches(ctx context.Context, keys constants.Keys)

	GetClient() *redis.Client
}

type cacheRepository struct {
	client *redis.Client
	pubsub *redis.PubSub
}

// Get retrieves a value from the cache by key.
// Returns the stored JSON wrapped in an array (e.g. [value]) to preserve
// the same unmarshal behaviour as the former JSONPath "$" responses.
func (r *cacheRepository) Get(ctx context.Context, key string, _ string) (json.RawMessage, error) {
	raw, err := r.client.Get(ctx, key).Bytes()
	if err != nil {
		return nil, err
	}
	wrapped := make([]byte, 0, len(raw)+2)
	wrapped = append(wrapped, '[')
	wrapped = append(wrapped, raw...)
	wrapped = append(wrapped, ']')
	return json.RawMessage(wrapped), nil
}

// Set stores a value in the cache with an optional expiration time.
func (r *cacheRepository) Set(ctx context.Context, key, _ string, value interface{}, expiration time.Duration) error {
	b, err := json.Marshal(value)
	if err != nil {
		logs.Error(fmt.Sprintf("failed to marshal cache value for key %s: %v", key, err))
		return err
	}
	if err := r.client.Set(ctx, key, b, expiration).Err(); err != nil {
		logs.Error(fmt.Sprintf("failed to set cache key %s: %v", key, err))
		return err
	}
	return nil
}

// Delete removes a value from the cache by key
func (r *cacheRepository) Delete(ctx context.Context, key string) error {
	return r.client.Del(ctx, key).Err()
}

// Exists checks if a key exists in the cache
func (r *cacheRepository) Exists(ctx context.Context, key string) (bool, error) {
	result, err := r.client.Exists(ctx, key).Result()
	if err != nil {
		return false, err
	}
	return result > 0, nil
}

// Increment increments a numeric value in the cache
func (r *cacheRepository) Increment(ctx context.Context, key string) (int64, error) {
	return r.client.Incr(ctx, key).Result()
}

// Expire sets an expiration time on a key
func (r *cacheRepository) Expire(ctx context.Context, key string, expiration time.Duration) error {
	return r.client.Expire(ctx, key, expiration).Err()
}

// DeleteAll implements output.CacheRepository.
func (r *cacheRepository) DeleteAll(ctx context.Context) error {
	return r.client.FlushAll(ctx).Err()
}

// DeleteByPrefix implements output.CacheRepository.
func (r *cacheRepository) DeleteByPrefix(ctx context.Context, prefixes []string) error {
	for _, prefix := range prefixes {
		iter := r.client.Scan(ctx, 0, prefix+"*", 0).Iterator()
		for iter.Next(ctx) {
			if err := r.client.Del(ctx, iter.Val()).Err(); err != nil {
				return err
			}
		}
		if err := iter.Err(); err != nil {
			return err
		}
	}
	return nil
}

// InvalidateEntityCaches invalidates both list and item caches for any entity
// Uses generics for type safety while supporting different ID types
func (r *cacheRepository) InvalidateEntityCaches(ctx context.Context, key constants.Keys) {
	// Get all keys matching key.Key (all keys are under constants.AppKeyPrefix)
	keys, err := r.client.Keys(ctx, key.Key).Result()
	if err != nil {
		logs.Error(fmt.Sprintf("failed to get user keys: %v", err))
	}

	if len(keys) == 0 {
		logs.Info("no user keys to delete")
	}
	logs.Info(fmt.Sprintf("keys: %v", keys))
	// Delete all keys found
	for _, k := range keys {
		if err := r.Delete(ctx, k); err != nil {
			logs.Error(fmt.Sprintf("failed to delete key %s: %v", k, err))
		}
	}
}

// Close closes the Redis client
func (r *cacheRepository) Close() error {
	if r.pubsub != nil {
		_ = r.pubsub.Close()
	}

	return r.client.Close()
}

// Add implementation
func (r *cacheRepository) GetClient() *redis.Client {
	return r.client
}

func NewRedisRepository(client *redis.Client) RedisRepository {
	return &cacheRepository{
		client: client,
		pubsub: nil,
	}
}
