package bootstrap

import (
	"context"
	"fmt"
	"os"
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
	logs.SetSourceTrace(sourceTraceEnabled(cfg.Env))

	app.Use(requestid.New())
	app.Use(newAccessLog(prettyAccessLog(cfg.Env)))
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

// prettyAccessLog: the one-line console format of the access log is for development only (it shows the client
// address and the concrete path, which NFR-22 keeps out of the JSON log). LOG_CONSOLE=pretty|off overrides the
// default (pretty in dev, off elsewhere).
func prettyAccessLog(env string) bool {
	switch strings.ToLower(os.Getenv("LOG_CONSOLE")) {
	case "pretty", "on", "true", "1":
		return true
	case "off", "false", "0", "no", "json":
		return false
	}
	return strings.EqualFold(env, "dev")
}

// sourceTraceEnabled: the "From Redis / From Postgres" lines (logs.CacheKey, logs.Source) are on in development
// only. LOG_SOURCE=on|off overrides.
func sourceTraceEnabled(env string) bool {
	switch strings.ToLower(os.Getenv("LOG_SOURCE")) {
	case "on", "true", "1", "pretty":
		return true
	case "off", "false", "0", "no":
		return false
	}
	return strings.EqualFold(env, "dev")
}

// newAccessLog writes one JSON line per request: request_id, method, route template, status, latency and
// whether the caller presented a valid portal key. It never logs the query string, body, headers (the
// portal key included), cookies, user agent or the client IP in that JSON line (NFR-22, doc/08 section 2.10).
// The error returned by the chain is written here (through the app's error handler) so that the logged status
// is the final one.
//
// With pretty on (development) stdout gets a readable line instead of the JSON one, e.g.
//
//	09:29:40 | 200 |  203.338916ms | 127.0.0.1 | GET | /api/v1/public/site | -
//
// and the JSON line goes to the log file only. The last column is the error text of a failed request.
func newAccessLog(pretty bool) fiber.Handler {
	color := pretty && colorTerminal()
	return func(c *fiber.Ctx) error {
		start := time.Now()
		errText := "-"
		if err := c.Next(); err != nil {
			errText = err.Error()
			if herr := c.App().ErrorHandler(c, err); herr != nil {
				_ = c.SendStatus(fiber.StatusInternalServerError)
			}
		}
		latency := time.Since(start)
		status := c.Response().StatusCode()
		internal, _ := c.Locals(portalkey.LocalsKey).(bool)
		fields := []logs.Field{
			logs.String("request_id", requestid.FromFiber(c)),
			logs.String("method", c.Method()),
			logs.String("route", routeTemplate(c)),
			logs.Int("status", status),
			logs.Int64("latency_ms", latency.Milliseconds()),
			logs.Bool("internal", internal),
		}
		if !pretty {
			logs.Info("request", fields...)
			return nil
		}
		logs.InfoFileOnly("request", fields...)
		_, _ = fmt.Fprintln(os.Stdout, formatAccessLine(time.Now(), status, latency, c.IP(), c.Method(), c.Path(), errText, color))
		return nil
	}
}

// formatAccessLine builds the console line "15:04:05 | status | latency | ip | method | path | error". The query
// string is never part of it. With color the status and the method are colored like Fiber's logger.
func formatAccessLine(at time.Time, status int, latency time.Duration, ip, method, path, errText string, color bool) string {
	st := fmt.Sprintf("%3d", status)
	m := method
	if color {
		st = statusColor(status) + st + ansiReset
		m = methodColor(method) + m + ansiReset
	}
	if errText == "" {
		errText = "-"
	}
	return fmt.Sprintf("%s | %s | %13s | %s | %s | %s | %s", at.Format("15:04:05"), st, latency.String(), ip, m, path, errText)
}

const (
	ansiReset  = "\x1b[0m"
	ansiGreen  = "\x1b[32m"
	ansiCyan   = "\x1b[36m"
	ansiYellow = "\x1b[33m"
	ansiBlue   = "\x1b[34m"
	ansiRed    = "\x1b[31m"
	ansiMagent = "\x1b[35m"
)

func statusColor(status int) string {
	switch {
	case status >= 500:
		return ansiRed
	case status >= 400:
		return ansiYellow
	case status >= 300:
		return ansiCyan
	default:
		return ansiGreen
	}
}

func methodColor(method string) string {
	switch method {
	case fiber.MethodGet:
		return ansiCyan
	case fiber.MethodPost:
		return ansiGreen
	case fiber.MethodPut, fiber.MethodPatch:
		return ansiYellow
	case fiber.MethodDelete:
		return ansiRed
	case fiber.MethodOptions:
		return ansiBlue
	default:
		return ansiMagent
	}
}

// colorTerminal: colors only when stdout is a terminal and NO_COLOR is not set.
func colorTerminal() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
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
