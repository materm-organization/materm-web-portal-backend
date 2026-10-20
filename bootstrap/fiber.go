package bootstrap

import (
	"context"
	constants "go-fiber/pkg/constants/services"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/helmet/v2"
)

func NewFiber(cfg ServerConfig) *fiber.App {
	fiberCfg := fiber.Config{
		BodyLimit: 10 * 1024 * 1024, // Default: 4 * 1024 * 1024
	}
	// Zero means "not configured": keep Fiber's defaults.
	if cfg.ReadTimeout > 0 {
		fiberCfg.ReadTimeout = cfg.ReadTimeout
	}
	if cfg.WriteTimeout > 0 {
		fiberCfg.WriteTimeout = cfg.WriteTimeout
	}
	fiberApp := fiber.New(fiberCfg)
	fiberApp.Use(helmet.New())
	fiberApp.Use(logger.New())
	fiberApp.Use(recover.New())
	fiberApp.Use(cors.New())
	fiberApp.Use(dbQueryTimeoutMiddleware)
	return fiberApp
}

// dbQueryTimeoutMiddleware sets DBQueryTimeout (30s) on every request context.
// All downstream WithContext(ctx) calls in repositories inherit this deadline —
// if a query runs longer than 30s the driver cancels it and recycles the connection.
func dbQueryTimeoutMiddleware(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(c.UserContext(), constants.DBQueryTimeout)
	defer cancel()
	c.SetUserContext(ctx)
	return c.Next()
}
