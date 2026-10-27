// Package redistest gives tests a Redis client on the scratch Redis started by scripts/testdb.sh.
// It refuses the developer's shared Redis (port 6379) and any server lacking the scratch marker key.
package redistest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// EnvURL is written to .env.test by scripts/testdb.sh.
	EnvURL = "MATERM_TEST_REDIS"

	userPort    = 6379
	markerKey   = "materm:scratch"
	markerValue = "on"
)

// ErrRefused wraps every guard refusal.
var ErrRefused = errors.New("redistest: refused")

// Guard is the static guard: no network. It refuses port 6379 (explicit or implied by a missing port).
func Guard(rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("%w: empty URL", ErrRefused)
	}
	opt, err := redis.ParseURL(rawURL)
	if err != nil {
		return fmt.Errorf("%w: URL is not parseable", ErrRefused) // do not wrap err: it can echo the URL
	}
	_, portStr, err := net.SplitHostPort(opt.Addr)
	if err != nil {
		return fmt.Errorf("%w: address has no port", ErrRefused)
	}
	port, _ := strconv.Atoi(portStr)
	if port == userPort {
		return fmt.Errorf("%w: port %d belongs to the shared Redis", ErrRefused, userPort)
	}
	return nil
}

// Redis is a client bound to a random key prefix on the scratch Redis.
type Redis struct {
	Client *redis.Client
	// Prefix is a random key prefix owned by this test; use it for every key.
	Prefix string
}

// Key returns Prefix + name.
func (r *Redis) Key(name string) string { return r.Prefix + name }

// New connects to the scratch Redis named by MATERM_TEST_REDIS. It skips the test when the variable
// is unset and fails it when the guard refuses. The cleanup deletes only keys under Prefix
// (never FLUSHDB / FLUSHALL).
func New(t testing.TB) *Redis {
	t.Helper()

	raw := os.Getenv(EnvURL)
	if raw == "" {
		t.Skipf("%s is not set: run `make testdb-up` and use `make test-db`", EnvURL)
	}
	if err := Guard(raw); err != nil {
		t.Fatalf("%v", err)
	}
	opt, err := redis.ParseURL(raw)
	if err != nil {
		t.Fatalf("%v: unparseable URL", ErrRefused)
	}
	client := redis.NewClient(opt)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if v, err := client.Get(ctx, markerKey).Result(); err != nil || v != markerValue {
		_ = client.Close()
		t.Fatalf("%v: server does not carry the scratch marker", ErrRefused)
	}

	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("redistest: random: %v", err)
	}
	r := &Redis{Client: client, Prefix: "materm-test:" + hex.EncodeToString(b) + ":"}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		iter := client.Scan(cctx, 0, r.Prefix+"*", 200).Iterator()
		for iter.Next(cctx) {
			client.Del(cctx, iter.Val())
		}
		_ = client.Close()
	})
	return r
}
