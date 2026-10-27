package bootstrap

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/helmet/v2"

	"github.com/materm-organization/materm-web-portal-backend/pkg/apperror"
	serviceconstants "github.com/materm-organization/materm-web-portal-backend/pkg/constants/services"
	"github.com/materm-organization/materm-web-portal-backend/pkg/logs"
	"github.com/materm-organization/materm-web-portal-backend/pkg/portalkey"
	"github.com/materm-organization/materm-web-portal-backend/pkg/requestid"
)

// MaxBodyBytes is the request body limit (NFR-13): 1 MB. The contact form is far smaller (16 KB + envelope).
const MaxBodyBytes = 1 * 1024 * 1024

// CORS contract (doc/08 section 2.10). X-Portal-Key is deliberately not an allowed header: a browser must
// never be given the portal key, the API is called server-to-server.
const (
	corsAllowHeaders  = "Content-Type,Accept-Language,X-Request-Id"
	corsExposeHeaders = "X-Request-Id,ETag,Retry-After,X-RateLimit-Limit,X-RateLimit-Remaining,X-RateLimit-Reset"
)

// NewFiber builds the Fiber app with the middleware pipeline shared with the admin backend: request id ->
// access log -> recover -> security headers -> CORS allow-list (only when server.corsOrigins is set) ->
// request-scoped DB deadline. The portal key is NOT part of this pipeline: the router mounts it on the
// /api/v1 group (pkg/portalkey), so the probes at the root stay open. Errors returned by handlers are
// written by apperror.Handler.
func NewFiber(cfg ServerConfig) *fiber.App {
	fiberCfg := fiber.Config{
		AppName:      "materm-web-portal-backend",
		BodyLimit:    MaxBodyBytes,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		ErrorHandler: apperror.Handler,
	}
	// Forwarding headers are trusted only from the configured proxies; with none configured the
	// connection's own address is the client IP.
	if len(cfg.TrustedProxies) > 0 {
		fiberCfg.EnableTrustedProxyCheck = true
		fiberCfg.TrustedProxies = cfg.TrustedProxies
		fiberCfg.ProxyHeader = cfg.ProxyHeader
	}
	app := fiber.New(fiberCfg)

	app.Use(requestid.New())
	app.Use(accessLog)
	app.Use(recover.New(recover.Config{
		EnableStackTrace: true,
		StackTraceHandler: func(c *fiber.Ctx, e any) {
			logs.Error("panic recovered",
				logs.String("request_id", requestid.FromFiber(c)), logs.String("panic", fmt.Sprint(e)),
				logs.String("stack", string(debug.Stack())))
		},
	}))
	app.Use(helmet.New())
	// An empty AllowOrigins would make the fiber cors middleware answer "*": with no configured origin the
	// middleware is not mounted at all, so no response ever carries Access-Control-Allow-Origin.
	if len(cfg.CORSOrigins) > 0 {
		app.Use(cors.New(corsConfig(cfg.CORSOrigins)))
	}
	app.Use(dbQueryTimeoutMiddleware)
	return app
}

// corsConfig is the allow-list CORS policy: only the configured origins, read methods, no credentials.
func corsConfig(origins []string) cors.Config {
	return cors.Config{
		AllowOrigins:     strings.Join(origins, ","),
		AllowMethods:     "GET,POST,OPTIONS",
		AllowHeaders:     corsAllowHeaders,
		ExposeHeaders:    corsExposeHeaders,
		AllowCredentials: false,
		MaxAge:           600,
	}
}

// accessLog writes one JSON line per request: request_id, method, route template, status, latency and
// whether the caller presented a valid portal key. It never logs the query string, body, headers (the
// portal key included), cookies, user agent or the client IP (NFR-22, doc/08 section 2.10). The error
// returned by the chain is written here (through the app's error handler) so that the logged status is
// the final one.
func accessLog(c *fiber.Ctx) error {
	start := time.Now()
	if err := c.Next(); err != nil {
		if herr := c.App().ErrorHandler(c, err); herr != nil {
			_ = c.SendStatus(fiber.StatusInternalServerError)
		}
	}
	internal, _ := c.Locals(portalkey.LocalsKey).(bool)
	logs.Info("request",
		logs.String("request_id", requestid.FromFiber(c)),
		logs.String("method", c.Method()),
		logs.String("route", routeTemplate(c)),
		logs.Int("status", c.Response().StatusCode()),
		logs.Int64("latency_ms", time.Since(start).Milliseconds()),
		logs.Bool("internal", internal))
	return nil
}

// dbQueryTimeoutMiddleware sets DBQueryTimeout (3s, the statement_timeout of the read-only role) on every
// request context. All downstream WithContext(ctx) calls in repositories inherit this deadline.
func dbQueryTimeoutMiddleware(c *fiber.Ctx) error {
	ctx, cancel := context.WithTimeout(c.UserContext(), serviceconstants.DBQueryTimeout)
	defer cancel()
	c.SetUserContext(ctx)
	return c.Next()
}

// routeTemplate is the matched route pattern ("/api/v1/public/pages/:id"), not the concrete path, so that
// ids stay out of the logs and the cardinality is bounded. Unmatched requests log "unmatched".
func routeTemplate(c *fiber.Ctx) string {
	r := c.Route()
	if r == nil || r.Method == fiber.MethodHead && r.Path == "" || len(r.Handlers) == 0 {
		return "unmatched"
	}
	if r.Method == "USE" || r.Path == "/" && c.Path() != "/" {
		return "unmatched"
	}
	return r.Path
}
