// Package portalkey authenticates the one caller of the portal API: the portal-frontend server, which sends
// a shared secret in the X-Portal-Key header (doc/00 section 10.4, doc/08 sections 2.5 and 2.9). The API is
// reached ONLY server-to-server; there is no keyless public access.
//
// Rules the code keeps:
//   - the presented key is compared in constant time against every configured key (two at most, so a key
//     can be rotated without downtime) and the loop never stops early; both sides are hashed first so the
//     comparison does not leak the length of a key;
//   - a missing, empty or wrong key gets the same answer: 401 UNAUTHENTICATED;
//   - with NO key configured nothing is accepted (fail closed; there is no development bypass);
//   - the key value is never logged, put in an error or echoed.
package portalkey

import (
	"crypto/sha256"
	"crypto/subtle"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/materm-organization/materm-web-portal-backend/pkg/apperror"
	"github.com/materm-organization/materm-web-portal-backend/pkg/logs"
)

// Header is the request header that carries the key.
const Header = "X-Portal-Key"

// LocalsKey is the fiber.Ctx Locals key set to true on a request that carried a valid key (read it with
// Internal). The access log reports it.
const LocalsKey = "internal"

// Verifier holds the digests of the accepted keys.
type Verifier struct {
	sums [][sha256.Size]byte
}

// New builds a Verifier for keys. Blank entries are ignored; no keys at all yields a Verifier that rejects
// everything.
func New(keys []string) *Verifier {
	v := &Verifier{}
	for _, k := range keys {
		if k == "" {
			continue
		}
		v.sums = append(v.sums, sha256.Sum256([]byte(k)))
	}
	return v
}

// Configured reports whether at least one key is configured.
func (v *Verifier) Configured() bool { return len(v.sums) > 0 }

// Match reports whether presented equals one of the configured keys. It examines every key even after a
// match, so the time taken does not tell which key (or whether any) matched.
func (v *Verifier) Match(presented string) bool {
	if presented == "" || len(v.sums) == 0 {
		return false
	}
	got := sha256.Sum256([]byte(presented))
	ok := 0
	for i := range v.sums {
		ok |= subtle.ConstantTimeCompare(got[:], v.sums[i][:])
	}
	return ok == 1
}

// Middleware rejects a request whose X-Portal-Key does not match with 401 UNAUTHENTICATED and sets Locals
// LocalsKey=true on one that does. Responses to a rejected request are never cacheable.
func (v *Verifier) Middleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if !v.Match(c.Get(Header)) {
			c.Set(fiber.HeaderCacheControl, "no-store")
			logs.WarnEvery("portalkey:rejected", time.Minute, "portal key missing or wrong: request refused")
			return apperror.Unauthenticated()
		}
		c.Locals(LocalsKey, true)
		return c.Next()
	}
}

// Internal reports whether the current request carried a valid portal key.
func Internal(c *fiber.Ctx) bool {
	ok, _ := c.Locals(LocalsKey).(bool)
	return ok
}
