package ratelimit

import (
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/materm-organization/materm-web-portal-backend/pkg/apperror"
	"github.com/materm-organization/materm-web-portal-backend/pkg/logs"
)

// Config configures Middleware.
type Config struct {
	// Name labels the limiter in logs ("login-ip").
	Name   string
	Limit  int
	Window time.Duration
	// Key builds the full Redis key of the request (include constants.AppKeyPrefix; hash identifiers).
	// An empty key skips limiting for the request.
	Key func(c *fiber.Ctx) string
	// FailOpen lets the request through (with a throttled warning) when Redis fails. Default false: the
	// request is answered 503, which is right for security throttles. Use true only for generic limits
	// that have no fallback.
	FailOpen bool
}

// Middleware limits requests per Config.Key. It sets X-RateLimit-Limit / -Remaining / -Reset (epoch
// seconds) on every limited response and answers 429 RATE_LIMITED with Retry-After when exceeded; the
// answer never says whether the account or the address caused it.
func Middleware(l Limiter, cfg Config) fiber.Handler {
	return func(c *fiber.Ctx) error {
		key := cfg.Key(c)
		if key == "" {
			return c.Next()
		}
		res, err := l.Hit(c.UserContext(), key, cfg.Limit, cfg.Window)
		if err != nil {
			if cfg.FailOpen {
				logs.WarnEvery("ratelimit:"+cfg.Name, time.Minute, "rate limiter unavailable, request allowed", logs.String("limiter", cfg.Name))
				return c.Next()
			}
			return apperror.ServiceUnavailable(err, 0)
		}
		SetHeaders(c, res)
		if !res.Allowed {
			return apperror.RateLimited(res.RetryAfter)
		}
		return c.Next()
	}
}

// SetHeaders writes the X-RateLimit-* headers for res.
func SetHeaders(c *fiber.Ctx, res Result) {
	c.Set("X-RateLimit-Limit", strconv.Itoa(res.Limit))
	c.Set("X-RateLimit-Remaining", strconv.Itoa(res.Remaining))
	c.Set("X-RateLimit-Reset", strconv.FormatInt(res.ResetAt.Unix(), 10))
}
