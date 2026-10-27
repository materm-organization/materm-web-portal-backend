// Package requestid carries the X-Request-Id of a request through Fiber, context.Context, the error
// envelope, audit rows and logs (doc/05 section 1.7).
package requestid

import (
	"context"
	"regexp"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// Header is the request/response header name.
const Header = "X-Request-Id"

// LocalsKey is the fiber.Ctx Locals key.
const LocalsKey = "request_id"

type ctxKey struct{}

// valid accepts what a client may send; anything else is dropped and replaced (log-injection guard).
var valid = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)

// New is the middleware: it keeps a well-formed client id, otherwise creates a UUIDv7, then exposes it as
// the response header, a Locals value and a context value (c.UserContext()).
func New() fiber.Handler {
	return func(c *fiber.Ctx) error {
		id := c.Get(Header)
		if !valid.MatchString(id) {
			id = generate()
		}
		c.Locals(LocalsKey, id)
		c.Set(Header, id)
		c.SetUserContext(WithContext(c.UserContext(), id))
		return c.Next()
	}
}

func generate() string {
	if id, err := uuid.NewV7(); err == nil {
		return id.String()
	}
	return uuid.NewString()
}

// FromFiber returns the request id of the current request ("" outside the middleware).
func FromFiber(c *fiber.Ctx) string {
	id, _ := c.Locals(LocalsKey).(string)
	return id
}

// WithContext stores id in ctx.
func WithContext(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext returns the request id stored in ctx ("" if none).
func FromContext(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}
