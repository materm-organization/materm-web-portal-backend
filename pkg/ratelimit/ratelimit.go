// Package ratelimit counts events per key in a fixed window (Redis, one atomic Lua script) and answers
// whether a limit is exceeded (doc/05 section 1.8, doc/06 section 5.5).
//
// Redis is the fast path only. The limiter returns an error when Redis fails and never decides to let the
// request through by itself: security throttles (login, refresh) fall back to the database counters of the
// auth module, the generic Middleware offers an explicit FailOpen switch. Keys must carry the service key
// prefix (constants.AppKeyPrefix) because the Redis may be shared; never put a raw username or IP in a
// key - use Hash.
package ratelimit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrUnavailable is returned when no Redis client is configured (degraded mode).
var ErrUnavailable = errors.New("ratelimit: redis unavailable")

// Result is the outcome of one Hit.
type Result struct {
	// Allowed is true while Count <= Limit.
	Allowed   bool
	Count     int64
	Limit     int
	Remaining int
	// ResetAt is when the window ends (the counter is deleted by Redis then).
	ResetAt time.Time
	// RetryAfter is the time until ResetAt (only meaningful when !Allowed).
	RetryAfter time.Duration
}

// Limiter counts events in fixed windows.
type Limiter interface {
	// Hit records one event for key and reports whether the limit of the window is still respected. The
	// window starts at the first event of a key and lasts window.
	Hit(ctx context.Context, key string, limit int, window time.Duration) (Result, error)
	// Count returns the current count and the remaining window without recording an event.
	Count(ctx context.Context, key string) (count int64, remaining time.Duration, err error)
	// Reset deletes the counter of key (e.g. after a successful login or an admin unlock).
	Reset(ctx context.Context, key string) error
}

// Hash returns the first 32 hex characters of sha256(s): the form in which usernames and IPs are put into
// a Redis key.
func Hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:32]
}

// hitScript increments the counter and (re)arms its expiry in one step. A counter without a TTL (which
// cannot be created by this script, but may be by a crash elsewhere) would block a key forever, so the
// script repairs it.
const hitScript = `
local c = redis.call('INCR', KEYS[1])
local ttl = redis.call('PTTL', KEYS[1])
if c == 1 or ttl < 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
  ttl = tonumber(ARGV[1])
end
return {c, ttl}
`

// Redis is a Limiter backed by Redis.
type Redis struct {
	client redis.Scripter
	hit    *redis.Script
	raw    redis.Cmdable
	now    func() time.Time
}

// NewRedis builds a Redis limiter. A nil client yields a limiter whose methods return ErrUnavailable, so
// the app can start in degraded mode and callers can fall back.
func NewRedis(client *redis.Client) *Redis {
	l := &Redis{hit: redis.NewScript(hitScript), now: time.Now}
	if client != nil {
		l.client, l.raw = client, client
	}
	return l
}

// Hit implements Limiter.
func (l *Redis) Hit(ctx context.Context, key string, limit int, window time.Duration) (Result, error) {
	if l.client == nil {
		return Result{}, ErrUnavailable
	}
	if limit < 1 || window < time.Millisecond {
		return Result{}, fmt.Errorf("ratelimit: invalid limit %d / window %s", limit, window)
	}
	res, err := l.hit.Run(ctx, l.client, []string{key}, window.Milliseconds()).Int64Slice()
	if err != nil {
		return Result{}, fmt.Errorf("ratelimit: hit: %w", err)
	}
	if len(res) != 2 {
		return Result{}, errors.New("ratelimit: unexpected script reply")
	}
	count, ttl := res[0], time.Duration(res[1])*time.Millisecond
	r := Result{Allowed: count <= int64(limit), Count: count, Limit: limit, ResetAt: l.now().Add(ttl), RetryAfter: ttl}
	if rem := int64(limit) - count; rem > 0 {
		r.Remaining = int(rem)
	}
	return r, nil
}

// Count implements Limiter.
func (l *Redis) Count(ctx context.Context, key string) (int64, time.Duration, error) {
	if l.raw == nil {
		return 0, 0, ErrUnavailable
	}
	pipe := l.raw.Pipeline()
	get := pipe.Get(ctx, key)
	ttl := pipe.PTTL(ctx, key)
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return 0, 0, fmt.Errorf("ratelimit: count: %w", err)
	}
	n, err := get.Result()
	if errors.Is(err, redis.Nil) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, fmt.Errorf("ratelimit: count: %w", err)
	}
	count, err := strconv.ParseInt(n, 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("ratelimit: count: %w", err)
	}
	remaining := ttl.Val()
	if remaining < 0 {
		remaining = 0
	}
	return count, remaining, nil
}

// Reset implements Limiter.
func (l *Redis) Reset(ctx context.Context, key string) error {
	if l.raw == nil {
		return ErrUnavailable
	}
	if err := l.raw.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("ratelimit: reset: %w", err)
	}
	return nil
}
