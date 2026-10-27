package application

import (
	"context"
	"fmt"

	"github.com/materm-organization/materm-web-portal-backend/bootstrap"
	"github.com/materm-organization/materm-web-portal-backend/pkg/logs"
	"github.com/materm-organization/materm-web-portal-backend/pkg/publiccache"
)

// SetupInvalidation starts the subscriber that clears the response cache when the admin backend publishes a
// signed invalidation message (doc/08 section 2.7). It returns (nil, nil) - and the portal serves from the
// cache until cache.ttl - when
//
//   - Redis is not connected (dev degraded start) or cache.enabled is false: there is no cache to clear;
//   - cache.invalidateKey is blank OUTSIDE dev: the subscriber refuses to run unsigned (a warning is logged;
//     Validate already warned at start).
//
// With a blank key in dev the subscriber runs and accepts unsigned messages (so the admin side can be
// developed before it signs). A subscribe failure is an error outside dev; in dev it is logged and the
// portal starts without invalidation.
func SetupInvalidation(ctx context.Context, env *bootstrap.Env, repos *Repositories) (*publiccache.Subscriber, error) {
	if repos.Redis == nil || !env.Cache.Enabled {
		return nil, nil
	}
	key := []byte(env.Cache.InvalidateKey)
	if len(key) == 0 && env.Strict() {
		logs.Warn("cache invalidation subscriber is OFF: cache.invalidateKey is blank outside dev; cached responses expire only by cache.ttl")
		return nil, nil
	}
	sub, err := publiccache.NewSubscriber(publiccache.SubscriberOptions{
		Client:        repos.Redis,
		Cache:         repos.PublicCache,
		Channel:       env.Cache.InvalidateChannel,
		Key:           key,
		AllowUnsigned: len(key) == 0, // only reachable in dev (see above)
		MaxAge:        env.Cache.InvalidateMaxAge,
	})
	if err != nil {
		return nil, fmt.Errorf("cache invalidation: %w", err)
	}
	if err = sub.Start(ctx); err != nil {
		_ = sub.Close()
		if env.Strict() {
			return nil, fmt.Errorf("cache invalidation: %w", err)
		}
		logs.Warn("cache invalidation subscriber could not start in dev; continuing without it", logs.Err(err))
		return nil, nil
	}
	if len(key) == 0 {
		logs.Warn("cache invalidation subscriber runs UNSIGNED (dev only): set cache.invalidateKey")
	}
	logs.Info("cache invalidation subscriber started", logs.String("channel", env.Cache.InvalidateChannel))
	return sub, nil
}
