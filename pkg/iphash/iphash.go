// Package iphash turns a visitor's IP address into the only form in which the portal ever stores or counts
// it (doc/08 sections 2.7 and 6.5, NFR-18): HMAC-SHA256(ip, salt of the day).
//
// The salt is 32 random bytes per UTC day in Redis (rediskeys IPSalt, SETNX, TTL 48 h). Two consequences are
// the point of the design: the same visitor has a different hash every day, so nothing links submissions
// across days; and once the salt has expired nobody - the portal included - can recompute a hash from an IP
// address, so a leaked database does not reveal addresses. The hash goes to forms_submit_form (bytea) and,
// as its first 32 hex characters, into rate-limit keys; the raw address is never written anywhere.
//
// Hash needs Redis. When Redis is unavailable it returns ErrUnavailable and the caller fails closed (a
// submission is refused with 503): there is no unsalted fallback.
package iphash

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/materm-organization/materm-web-portal-backend/pkg/rediskeys"
)

// ErrUnavailable is returned when no Redis client is configured or the salt cannot be read.
var ErrUnavailable = errors.New("iphash: salt unavailable")

// SaltTTL is how long a day's salt lives in Redis (the day itself plus a day of slack for requests that
// straddle midnight).
const SaltTTL = 48 * time.Hour

const saltBytes = 32

// Sum is an HMAC-SHA256 value.
type Sum [sha256.Size]byte

// Bytes returns the value as a slice (the bytea column).
func (s Sum) Bytes() []byte { return s[:] }

// Hex returns the lower-case hex form (64 characters).
func (s Sum) Hex() string { return hex.EncodeToString(s[:]) }

// Short returns the first 32 hex characters: the <iphash> of the Redis key families.
func (s Sum) Short() string { return s.Hex()[:32] }

// Hasher computes daily salted IP hashes.
type Hasher struct {
	client redis.Cmdable
	keys   rediskeys.Keys
	now    func() time.Time
}

// New builds a Hasher. client may be nil (every Hash then returns ErrUnavailable).
func New(client redis.Cmdable, keys rediskeys.Keys) *Hasher {
	if c, ok := client.(*redis.Client); ok && c == nil {
		client = nil // a nil *redis.Client in the interface is not a nil interface
	}
	return &Hasher{client: client, keys: keys, now: time.Now}
}

// WithClock returns a copy that reads the time from now (tests).
func (h *Hasher) WithClock(now func() time.Time) *Hasher {
	c := *h
	c.now = now
	return &c
}

// Hash returns HMAC-SHA256(canonical ip, salt of the current UTC day). ip must already be canonical (see
// pkg/clientip); an address that does not parse is refused here too, because hashing arbitrary text would
// make the rate-limit key attacker-chosen.
func (h *Hasher) Hash(ctx context.Context, ip string) (Sum, error) {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return Sum{}, fmt.Errorf("iphash: not an IP address")
	}
	salt, err := h.Salt(ctx, h.now())
	if err != nil {
		return Sum{}, err
	}
	return Compute(a.Unmap().String(), salt), nil
}

// Compute is the pure part: HMAC-SHA256 with the salt as the key.
func Compute(ip string, salt []byte) Sum {
	m := hmac.New(sha256.New, salt)
	m.Write([]byte(ip))
	var s Sum
	copy(s[:], m.Sum(nil))
	return s
}

// Salt returns the salt of the UTC day of at, creating it if this is the first request of the day. Creation
// is SETNX followed by a read, so concurrent instances all end up with the winner's value.
func (h *Hasher) Salt(ctx context.Context, at time.Time) ([]byte, error) {
	if h.client == nil {
		return nil, ErrUnavailable
	}
	key := h.keys.IPSalt(at)
	if b, err := h.client.Get(ctx, key).Bytes(); err == nil && len(b) == saltBytes {
		return b, nil
	} else if err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	fresh := make([]byte, saltBytes)
	if _, err := rand.Read(fresh); err != nil {
		return nil, fmt.Errorf("iphash: random: %w", err)
	}
	if err := h.client.SetNX(ctx, key, fresh, SaltTTL).Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	b, err := h.client.Get(ctx, key).Bytes()
	if err != nil || len(b) != saltBytes {
		return nil, fmt.Errorf("%w: salt unreadable", ErrUnavailable)
	}
	return b, nil
}
