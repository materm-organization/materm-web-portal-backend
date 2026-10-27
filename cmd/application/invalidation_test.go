package application

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/materm-organization/materm-web-portal-backend/bootstrap"
	"github.com/materm-organization/materm-web-portal-backend/pkg/publiccache"
)

func TestInvalidationChannelDefaultsAreInStep(t *testing.T) {
	if bootstrap.DefaultInvalidateChannel != publiccache.DefaultChannel {
		t.Errorf("bootstrap default %q != publiccache default %q", bootstrap.DefaultInvalidateChannel, publiccache.DefaultChannel)
	}
}

// The subscriber only starts when there is something to clear and a way to trust the messages. None of these
// cases connects to Redis (the client points at a dead port and is never used).
func TestSetupInvalidationStaysOffWhenItShould(t *testing.T) {
	dead := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	defer dead.Close()
	cache := publiccache.New(publiccache.Options{Client: dead, Enabled: true})
	env := func(mode string, enabled bool, key string) *bootstrap.Env {
		e := &bootstrap.Env{Server: bootstrap.ServerConfig{Env: mode}, Cache: bootstrap.CacheConfig{Enabled: enabled, InvalidateKey: key}}
		e.Defaults()
		e.Cache.Enabled = enabled
		return e
	}
	cases := map[string]struct {
		env   *bootstrap.Env
		repos *Repositories
	}{
		"no redis (dev degraded)":      {env(bootstrap.EnvDev, true, ""), &Repositories{PublicCache: cache}},
		"cache disabled":               {env(bootstrap.EnvProd, false, "k"), &Repositories{Redis: dead, PublicCache: cache}},
		"prod, blank key: no unsigned": {env(bootstrap.EnvProd, true, ""), &Repositories{Redis: dead, PublicCache: cache}},
		"staging, blank key":           {env(bootstrap.EnvStaging, true, ""), &Repositories{Redis: dead, PublicCache: cache}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			sub, err := SetupInvalidation(context.Background(), tc.env, tc.repos)
			if sub != nil || err != nil {
				t.Errorf("want (nil, nil), got %v, %v", sub, err)
			}
		})
	}
}

// A subscribe failure is an error outside dev and a warning in dev.
func TestSetupInvalidationSubscribeFailure(t *testing.T) {
	dead := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	defer dead.Close()
	repos := &Repositories{Redis: dead, PublicCache: publiccache.New(publiccache.Options{Client: dead, Enabled: true})}
	prod := &bootstrap.Env{Server: bootstrap.ServerConfig{Env: bootstrap.EnvProd}, Cache: bootstrap.CacheConfig{Enabled: true, InvalidateKey: "0123456789abcdef0123456789abcdef"}}
	prod.Defaults()
	if sub, err := SetupInvalidation(context.Background(), prod, repos); err == nil || sub != nil {
		t.Errorf("prod must fail: %v %v", sub, err)
	}
	dev := &bootstrap.Env{Server: bootstrap.ServerConfig{Env: bootstrap.EnvDev}, Cache: bootstrap.CacheConfig{Enabled: true}}
	dev.Defaults()
	if sub, err := SetupInvalidation(context.Background(), dev, repos); err != nil || sub != nil {
		t.Errorf("dev must continue without the subscriber: %v %v", sub, err)
	}
}
