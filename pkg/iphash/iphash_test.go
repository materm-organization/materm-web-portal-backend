package iphash

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/materm-organization/materm-web-portal-backend/pkg/rediskeys"
)

func TestComputeIsKeyedAndDeterministic(t *testing.T) {
	saltA := bytes.Repeat([]byte{1}, 32)
	saltB := bytes.Repeat([]byte{2}, 32)
	a1, a2 := Compute("203.0.113.7", saltA), Compute("203.0.113.7", saltA)
	if a1 != a2 {
		t.Fatal("not deterministic")
	}
	if a1 == Compute("203.0.113.7", saltB) {
		t.Fatal("the salt does not matter: yesterday's hash would equal today's")
	}
	if a1 == Compute("203.0.113.8", saltA) {
		t.Fatal("different addresses collide")
	}
	if len(a1.Bytes()) != 32 || len(a1.Hex()) != 64 || len(a1.Short()) != 32 || !strings.HasPrefix(a1.Hex(), a1.Short()) {
		t.Fatalf("shapes: %d %d %d", len(a1.Bytes()), len(a1.Hex()), len(a1.Short()))
	}
	// the raw address is not in any rendering of the hash
	if strings.Contains(a1.Hex(), "203") && strings.Contains(a1.Hex(), "0.113") {
		t.Fatal("looks like the address")
	}
}

func TestHashWithoutRedisIsRefused(t *testing.T) {
	h := New(nil, rediskeys.New("test:"))
	if _, err := h.Hash(context.Background(), "203.0.113.7"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("nil client: %v", err)
	}
	// a dead Redis is the production failure mode: the salt cannot be read, so there is no hash
	dead := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 100 * time.Millisecond, MaxRetries: -1})
	defer dead.Close()
	h = New(dead, rediskeys.New("test:"))
	if _, err := h.Hash(context.Background(), "203.0.113.7"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("dead redis: %v", err)
	}
}

func TestTypedNilClientIsTreatedAsNoRedis(t *testing.T) {
	var rc *redis.Client // what the application holds in dev when Redis is unreachable
	h := New(rc, rediskeys.New("test:"))
	if _, err := h.Hash(context.Background(), "203.0.113.7"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("typed nil: %v", err)
	}
}

func TestHashRefusesTextThatIsNotAnAddress(t *testing.T) {
	// refused before Redis is even asked: nothing attacker-chosen becomes a key
	h := New(nil, rediskeys.New("test:"))
	for _, bad := range []string{"", "evil", "1.2.3.4, 5.6.7.8", "1.2.3.4:80"} {
		_, err := h.Hash(context.Background(), bad)
		if err == nil || errors.Is(err, ErrUnavailable) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}
