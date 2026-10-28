// Package publiccache is the Redis response cache of the public API (doc/08 section 2.7, PB-4): finished
// response DTOs keyed by resource, locale and parameters, invalidated by public tags that the admin backend
// publishes on a Redis channel (see Subscriber).
//
// Keys (pkg/rediskeys): c:<gen>:<resource>:<locale>:<sha1(params)> holds the JSON; tag:<tag> is the SET of
// cache keys that depend on a tag; gen is the generation (INCR = logical flush of everything); inv is the
// invalidation counter used by the conditional write.
//
// Correctness rules:
//
//   - Redis down, nil or cache.enabled=false: the loader runs directly (the database answers), never an error.
//     A Redis error in the middle of a request is logged (rate limited) and the database answer is returned.
//   - Stampede: concurrent requests for the same key share ONE load (singleflight), cached or not.
//   - Stale write after an invalidation (doc/10 R-024): a load remembers gen and inv before it reads the
//     database; the single Lua script that stores the entry refuses when either changed meanwhile. A request
//     that read the database before an editor's commit can therefore never store its old answer after the
//     invalidation, however long it took (the second delete 2 s later is then only a backstop).
//   - Invalidation is atomic per tag (a Lua script reads the tag set and unlinks its members and the set), so a
//     key stored by a concurrent load can never end up outside its tag set.
package publiccache

import (
	"context"
	"crypto/sha1" //nolint:gosec // not a security use: a short stable name for the normalised parameters (doc/08 section 2.7)
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"

	constants "github.com/materm-organization/materm-web-portal-backend/pkg/constants/services"
	"github.com/materm-organization/materm-web-portal-backend/pkg/logs"
	"github.com/materm-organization/materm-web-portal-backend/pkg/rediskeys"
)

// Options configure a Cache.
type Options struct {
	// Client is the Redis client. nil (or a nil *redis.Client wrapped by FromClient) = no cache: every Get calls
	// the loader.
	Client redis.Cmdable
	// Keys names the keys; the zero value means the service prefix (rediskeys.New("")).
	Keys rediskeys.Keys
	// Enabled is cache.enabled; false behaves like a missing Redis.
	Enabled bool
	// TTL is cache.ttl, the longest an entry lives (default constants.ExpireTime = 24 h).
	TTL time.Duration
	// TagGrace is added to the TTL for the lifetime of a tag set (default 1 h).
	TagGrace time.Duration
}

// FromClient converts a possibly nil *redis.Client into the interface without the typed-nil trap.
func FromClient(c *redis.Client) redis.Cmdable {
	if c == nil {
		return nil
	}
	return c
}

// Cache is safe for concurrent use.
type Cache struct {
	client  redis.Cmdable
	keys    rediskeys.Keys
	enabled bool
	ttl     time.Duration
	grace   time.Duration
	flight  singleflight.Group
}

// New builds a Cache.
func New(o Options) *Cache {
	c := &Cache{client: o.Client, keys: o.Keys, enabled: o.Enabled, ttl: o.TTL, grace: o.TagGrace}
	if c.keys.Prefix() == "" {
		c.keys = rediskeys.New("")
	}
	if c.ttl <= 0 {
		c.ttl = constants.ExpireTime
	}
	if c.grace <= 0 {
		c.grace = time.Hour
	}
	return c
}

// Active reports whether responses are cached at all (Redis configured and cache.enabled).
func (c *Cache) Active() bool { return c != nil && c.enabled && c.client != nil }

// Keys returns the key builder (tests and the subscriber use it).
func (c *Cache) Keys() rediskeys.Keys { return c.keys }

// Status says how a Get was answered.
type Status string

const (
	// Hit: the answer came from Redis.
	Hit Status = "hit"
	// Miss: the loader ran and the answer was offered to Redis (it may still have been refused, see R-024).
	Miss Status = "miss"
	// Bypass: the cache is off or Redis failed; the loader answered.
	Bypass Status = "bypass"
)

// Spec identifies one cacheable response.
type Spec struct {
	// Resource is a short fixed name ("page", "posts", "post", "site", ...): [a-z0-9][a-z0-9_.-]*.
	Resource string
	// Locale is the normalised locale code the response is built in ("-" when it has none).
	Locale string
	// Params are the normalised request parameters that change the answer (path, slug, page, limit, ...).
	Params map[string]string
	// TTL overrides the default lifetime (negative answers: 60 s; the locale chain: 10 min). 0 = default.
	TTL time.Duration
}

var (
	resourcePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,40}$`)
	specLocale      = regexp.MustCompile(`^(-|[a-z0-9]{1,8}(-[A-Za-z0-9]{1,8}){0,3})$`)
)

// ParamsHash is the stable name of a parameter set: sha1 over "k=v" pairs sorted by key (doc/08 section 2.7).
func ParamsHash(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha1.New() //nolint:gosec // see the import
	for _, k := range keys {
		h.Write([]byte(strconv.Itoa(len(k)) + ":" + k + "=" + strconv.Itoa(len(params[k])) + ":" + params[k] + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Loader produces the response and the public tags it depends on (doc/08 section 2.2 "Redis tags"; the list
// can include ids discovered while loading, e.g. album:<id>). It must not mutate shared state.
type Loader[T any] func(ctx context.Context) (value T, tags []string, err error)

// Get returns the response for spec: from Redis when present, else from load (shared between concurrent
// callers) and stored. Each caller receives its own decoded copy, so results may be modified freely. An error
// from load is returned as is and is never cached.
func Get[T any](ctx context.Context, c *Cache, spec Spec, load Loader[T]) (T, Status, error) {
	var zero T
	hash := ParamsHash(spec.Params)
	flightKey := spec.Resource + "|" + spec.Locale + "|" + hash

	produce := func(key string, gen, inv int64, store bool) ([]byte, error) {
		val, tags, err := load(ctx)
		if err != nil {
			return nil, err
		}
		payload, err := json.Marshal(val)
		if err != nil {
			return nil, err
		}
		if store {
			c.store(ctx, key, gen, inv, tags, payload, spec.TTL)
		}
		return payload, nil
	}
	run := func(key string, gen, inv int64, store bool) ([]byte, error) {
		v, err, shared := c.flight.Do(flightKey+"|"+key, func() (any, error) { return produce(key, gen, inv, store) })
		if err != nil && shared && ctx.Err() == nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			// the leader's context ended, ours did not: do the work ourselves once
			return produce(key, gen, inv, store)
		}
		if err != nil {
			return nil, err
		}
		return v.([]byte), nil
	}
	decode := func(payload []byte, st Status) (T, Status, error) {
		var out T
		if err := json.Unmarshal(payload, &out); err != nil {
			return zero, st, err
		}
		return out, st, nil
	}

	if !c.Active() || !validSpec(spec) {
		payload, err := run("-", 0, 0, false)
		if err != nil {
			return zero, Bypass, err
		}
		logs.Source(spec.Resource, "Postgres (cache off)")
		return decode(payload, Bypass)
	}

	gen, inv, err := c.snapshot(ctx)
	if err != nil {
		c.degraded("snapshot", err)
		payload, lerr := run("-", 0, 0, false)
		if lerr != nil {
			return zero, Bypass, lerr
		}
		logs.Source(spec.Resource, "Postgres (Redis failed)")
		return decode(payload, Bypass)
	}
	key := c.keys.Cache(gen, spec.Resource, spec.Locale, hash)
	logs.CacheKey(key)
	raw, err := c.client.Get(ctx, key).Bytes()
	switch {
	case err == nil:
		var out T
		if json.Unmarshal(raw, &out) == nil {
			logs.Source(spec.Resource, "Redis")
			return out, Hit, nil
		}
		_ = c.client.Del(ctx, key).Err() // corrupt entry: drop it and reload
	case !errors.Is(err, redis.Nil):
		c.degraded("get", err)
		payload, lerr := run("-", 0, 0, false)
		if lerr != nil {
			return zero, Bypass, lerr
		}
		logs.Source(spec.Resource, "Postgres (Redis failed)")
		return decode(payload, Bypass)
	}
	payload, err := run(key, gen, inv, true)
	if err != nil {
		return zero, Miss, err
	}
	logs.Source(spec.Resource, "Postgres")
	return decode(payload, Miss)
}

func validSpec(s Spec) bool {
	if !resourcePattern.MatchString(s.Resource) || !specLocale.MatchString(s.Locale) {
		logs.WarnEvery("publiccache.badspec", time.Minute, "publiccache: invalid cache spec, response not cached", logs.String("resource", s.Resource))
		return false
	}
	return true
}

func (c *Cache) degraded(what string, err error) {
	logs.WarnEvery("publiccache.degraded", time.Minute, "publiccache: Redis error, answering from the database",
		logs.String("op", what), logs.Err(err))
}

// snapshot reads gen and inv in one round trip. Missing counters are 0.
func (c *Cache) snapshot(ctx context.Context) (gen, inv int64, err error) {
	vals, err := c.client.MGet(ctx, c.keys.Gen(), c.keys.Inv()).Result()
	if err != nil {
		return 0, 0, err
	}
	return counter(vals[0]), counter(vals[1]), nil
}

func counter(v any) int64 {
	s, ok := v.(string)
	if !ok {
		return 0
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// storeScript stores an entry only if gen and inv are unchanged since the load started, and registers the key
// in its tag sets (each set lives at least TTL + grace).
//
//	KEYS[1]=gen KEYS[2]=inv KEYS[3]=cache key KEYS[4..]=tag sets
//	ARGV[1]=gen ARGV[2]=inv ARGV[3]=ttl ms ARGV[4]=tag set min ttl ms ARGV[5]=value
var storeScript = redis.NewScript(`
if (redis.call('GET', KEYS[1]) or '0') ~= ARGV[1] then return 0 end
if (redis.call('GET', KEYS[2]) or '0') ~= ARGV[2] then return 0 end
redis.call('SET', KEYS[3], ARGV[5], 'PX', ARGV[3])
for i = 4, #KEYS do
  redis.call('SADD', KEYS[i], KEYS[3])
  local t = redis.call('PTTL', KEYS[i])
  if t < tonumber(ARGV[4]) then redis.call('PEXPIRE', KEYS[i], ARGV[4]) end
end
return 1
`)

func (c *Cache) store(ctx context.Context, key string, gen, inv int64, tags []string, payload []byte, ttl time.Duration) {
	if ttl <= 0 || ttl > c.ttl {
		ttl = c.ttl
	}
	valid, rejected := FilterTags(tags)
	if rejected > 0 {
		logs.WarnEvery("publiccache.badtag", time.Minute, "publiccache: a loader returned tags outside the public vocabulary", logs.Int("count", rejected))
	}
	keys := []string{c.keys.Gen(), c.keys.Inv(), key}
	for _, t := range valid {
		keys = append(keys, c.keys.Tag(t))
	}
	err := storeScript.Run(ctx, c.client, keys,
		strconv.FormatInt(gen, 10), strconv.FormatInt(inv, 10),
		ttl.Milliseconds(), (ttl + c.grace).Milliseconds(), payload).Err()
	if err != nil {
		c.degraded("store", err)
	}
}

// invalidateScript unlinks every member of a tag set and the set itself, atomically. Returns the number of
// cache keys removed.
var invalidateScript = redis.NewScript(`
local members = redis.call('SMEMBERS', KEYS[1])
local n = #members
local i = 1
while i <= n do
  local j = math.min(i + 999, n)
  redis.call('UNLINK', unpack(members, i, j))
  i = j + 1
end
redis.call('UNLINK', KEYS[1])
return n
`)

// Invalidate runs ONE pass of an invalidation for tags (call FilterTags first: unknown tags are ignored
// here as well). It bumps the invalidation counter first, so loads already in flight cannot store anything,
// then unlinks the entries of each tag; "locales" and "*" also bump the generation (everything is stale).
// It returns the number of cache keys deleted. With no Redis it does nothing.
func (c *Cache) Invalidate(ctx context.Context, tags []string) (int64, error) {
	if !c.Active() {
		return 0, nil
	}
	valid, _ := FilterTags(tags)
	if len(valid) == 0 {
		return 0, nil
	}
	if err := c.client.Incr(ctx, c.keys.Inv()).Err(); err != nil {
		return 0, err
	}
	var deleted int64
	for _, t := range valid {
		if flushes(t) {
			if err := c.client.Incr(ctx, c.keys.Gen()).Err(); err != nil {
				return deleted, err
			}
		}
		n, err := invalidateScript.Run(ctx, c.client, []string{c.keys.Tag(t)}).Int64()
		if err != nil {
			return deleted, err
		}
		deleted += n
	}
	return deleted, nil
}

// Flush makes every cached entry unreachable (generation bump); the keys expire by TTL. Used when a
// subscriber (re)connects, because pub/sub does not replay what was missed (doc/08 section 2.7 item 4).
func (c *Cache) Flush(ctx context.Context) error {
	if !c.Active() {
		return nil
	}
	if err := c.client.Incr(ctx, c.keys.Inv()).Err(); err != nil {
		return err
	}
	return c.client.Incr(ctx, c.keys.Gen()).Err()
}

// Members lists the cache keys registered under a tag (diagnostics and tests, never the hot path).
func (c *Cache) Members(ctx context.Context, tag string) ([]string, error) {
	if !c.Active() {
		return nil, nil
	}
	return c.client.SMembers(ctx, c.keys.Tag(tag)).Result()
}
