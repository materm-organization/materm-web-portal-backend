package bootstrap

import (
	"testing"
	"time"

	"github.com/materm-organization/materm-web-portal-backend/pkg/redisguard"
)

// go-redis reads 0 as "use my default" for MaxRetries (3) and DialerRetries (5) and sleeps DialerRetryTimeout
// (100 ms) after every failed dial: the three together made one command on a dead Redis cost ~2 s (B1). The
// client must carry the normalised values.
func TestNewRedisClientIsFastFail(t *testing.T) {
	e := &Env{Redis: RedisConfig{Host: "127.0.0.1", Port: 1}}
	e.Defaults()
	c := NewRedisClient(e.Redis)
	t.Cleanup(func() { redisguard.Forget(c); _ = c.Close() })
	o := c.Options()
	if o.MaxRetries != 0 {
		t.Errorf("MaxRetries = %d, want 0 (config 0 must map to go-redis -1 = no retry)", o.MaxRetries)
	}
	if o.DialerRetries != 1 {
		t.Errorf("DialerRetries = %d, want 1", o.DialerRetries)
	}
	if o.DialerRetryTimeout > 5*time.Millisecond {
		t.Errorf("DialerRetryTimeout = %v", o.DialerRetryTimeout)
	}
	if o.DialTimeout != defaultRedisDialTimeout || o.ReadTimeout != defaultRedisIOTimeout || o.WriteTimeout != defaultRedisIOTimeout {
		t.Errorf("timeouts = %v %v %v", o.DialTimeout, o.ReadTimeout, o.WriteTimeout)
	}
	if o.PoolTimeout != o.ReadTimeout {
		t.Errorf("PoolTimeout = %v, want the read timeout", o.PoolTimeout)
	}
	if redisguard.Of(c) == nil {
		t.Error("the circuit breaker must be attached")
	}
}

func TestNewRedisClientHonoursRetriesFromConfig(t *testing.T) {
	e := &Env{Redis: RedisConfig{Host: "127.0.0.1", Port: 1, MaxRetries: 2}}
	e.Defaults()
	c := NewRedisClient(e.Redis)
	t.Cleanup(func() { redisguard.Forget(c); _ = c.Close() })
	if got := c.Options().MaxRetries; got != 2 {
		t.Errorf("MaxRetries = %d, want 2", got)
	}
}

func TestRedisFastFailKeysDefaultsAndBounds(t *testing.T) {
	e := validEnv()
	e.Defaults()
	if e.Redis.DialTimeout != 200*time.Millisecond || e.Redis.ReadTimeout != 300*time.Millisecond || e.Redis.WriteTimeout != 300*time.Millisecond ||
		e.Redis.MaxRetries != 0 || e.Redis.BreakerCooldown != 3*time.Second {
		t.Errorf("defaults: %+v", e.Redis)
	}
	for name, mut := range map[string]func(*Env){
		"redis.dialTimeout":     func(e *Env) { e.Redis.DialTimeout = time.Millisecond },
		"redis.readTimeout":     func(e *Env) { e.Redis.ReadTimeout = time.Minute },
		"redis.writeTimeout":    func(e *Env) { e.Redis.WriteTimeout = time.Hour },
		"redis.maxRetries":      func(e *Env) { e.Redis.MaxRetries = 9 },
		"redis.breakerCooldown": func(e *Env) { e.Redis.BreakerCooldown = time.Hour },
	} {
		v := validEnv()
		v.Defaults()
		mut(v)
		_, err := v.Validate()
		ce, ok := err.(*ConfigError)
		if !ok || !hasProblem(ce.Problems, name) {
			t.Errorf("%s: want a problem naming the key, got %v", name, err)
		}
	}
}

// The fast-fail keys are not in the user's config.yaml template: SetDefault makes them reachable by env.
func TestRedisFastFailKeysComeFromTheEnvironment(t *testing.T) {
	clearEnv(t)
	dir := writeConfig(t, blankTemplate)
	t.Setenv("REDIS_DIALTIMEOUT", "150ms")
	t.Setenv("REDIS_READTIMEOUT", "250ms")
	t.Setenv("REDIS_WRITETIMEOUT", "260ms")
	t.Setenv("REDIS_MAXRETRIES", "1")
	t.Setenv("REDIS_BREAKERCOOLDOWN", "5s")
	e, err := LoadEnv(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := e.Redis
	if r.DialTimeout != 150*time.Millisecond || r.ReadTimeout != 250*time.Millisecond || r.WriteTimeout != 260*time.Millisecond || r.MaxRetries != 1 || r.BreakerCooldown != 5*time.Second {
		t.Errorf("%+v", r)
	}
}
