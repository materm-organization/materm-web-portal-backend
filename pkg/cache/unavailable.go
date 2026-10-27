package cache

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	constants "github.com/materm-organization/materm-web-portal-backend/pkg/constants/services"
)

// ErrUnavailable is returned by every method of the degraded cache.
var ErrUnavailable = errors.New("cache: redis unavailable")

type unavailable struct{}

// Unavailable returns a RedisRepository that is used when Redis could not be reached at start-up (dev
// only; staging/prod refuse to start). Every method fails with ErrUnavailable, so cache users miss and
// security code (throttle, revoke checks, permission snapshots) falls back to the database instead of
// failing open (doc/06 section 5.5). GetClient returns nil.
func Unavailable() RedisRepository { return unavailable{} }

func (unavailable) Get(context.Context, string, string) (json.RawMessage, error) {
	return nil, ErrUnavailable
}
func (unavailable) Set(context.Context, string, string, interface{}, time.Duration) error {
	return ErrUnavailable
}
func (unavailable) Delete(context.Context, string) error                   { return ErrUnavailable }
func (unavailable) Exists(context.Context, string) (bool, error)           { return false, ErrUnavailable }
func (unavailable) Increment(context.Context, string) (int64, error)       { return 0, ErrUnavailable }
func (unavailable) Expire(context.Context, string, time.Duration) error    { return ErrUnavailable }
func (unavailable) DeleteByPrefix(context.Context, []string) error         { return ErrUnavailable }
func (unavailable) DeleteAll(context.Context) error                        { return ErrUnavailable }
func (unavailable) InvalidateEntityCaches(context.Context, constants.Keys) {}
func (unavailable) GetClient() *redis.Client                               { return nil }
