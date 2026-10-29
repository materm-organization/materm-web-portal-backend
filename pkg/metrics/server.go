package metrics

import (
	"bytes"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"
)

// ContentType is the Prometheus text exposition format.
const ContentType = "text/plain; version=0.0.4; charset=utf-8"

// NewApp builds the Fiber app of the metrics listener: GET /metrics and nothing else (everything else is 404).
// It is a separate app on a separate port, never mounted on the public listener (Q-B10: internal port, no
// authentication, reachable only from inside the cluster/host).
func NewApp(reg *Registry) *fiber.App {
	app := fiber.New(fiber.Config{
		AppName:               "materm-web-portal-backend-metrics",
		DisableStartupMessage: true,
		ReadTimeout:           5 * time.Second,
		WriteTimeout:          10 * time.Second,
		BodyLimit:             1024,
	})
	app.Get("/metrics", func(c *fiber.Ctx) error {
		var buf bytes.Buffer
		if err := reg.Write(&buf); err != nil {
			return c.SendStatus(fiber.StatusInternalServerError)
		}
		c.Set(fiber.HeaderContentType, ContentType)
		c.Set(fiber.HeaderCacheControl, "no-store")
		return c.Send(buf.Bytes())
	})
	return app
}

// Addr is the listen address of the metrics port: bind:port.
func Addr(bind string, port int) string { return fmt.Sprintf("%s:%d", bind, port) }
