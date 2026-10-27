// Command server runs the portal backend: validate configuration (fail fast), connect Redis and the
// read-only PostgreSQL role, wire the modules, serve, and shut down gracefully on SIGINT/SIGTERM. The portal
// never migrates or seeds: the admin backend owns the schema.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/materm-organization/materm-web-portal-backend/bootstrap"
	"github.com/materm-organization/materm-web-portal-backend/cmd/application"
	"github.com/materm-organization/materm-web-portal-backend/pkg/logs"
)

// shutdownTimeout is how long in-flight requests get to finish.
const shutdownTimeout = 15 * time.Second

func main() { os.Exit(run()) }

func run() int {
	defer logs.Sync()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	app, err := bootstrap.App()
	if err != nil {
		return fatal("startup", err)
	}
	if app.Redis != nil {
		defer func() { _ = app.Redis.Close() }()
	}

	rt, err := application.Setup(ctx, app)
	if err != nil {
		return fatal("startup", err)
	}
	defer rt.Close()

	serveErr := make(chan error, 1)
	go func() { serveErr <- app.Fiber.Listen(fmt.Sprintf(":%d", app.Env.Server.Port)) }()

	select {
	case err := <-serveErr:
		return fatal("server", err)
	case <-ctx.Done():
		logs.Info("shutting down", logs.Duration("timeout", shutdownTimeout))
		if err := app.Fiber.ShutdownWithTimeout(shutdownTimeout); err != nil && !errors.Is(err, context.Canceled) {
			return fatal("shutdown", err)
		}
		if err := <-serveErr; err != nil {
			logs.Error("server stopped with an error", logs.Err(err))
		}
		return 0
	}
}

// fatal logs the failure (never a config value: the errors name keys and rules only) and returns the exit code.
func fatal(stage string, err error) int {
	logs.Error(stage+" failed", logs.Err(err))
	fmt.Fprintf(os.Stderr, "%s: %v\n", stage, err)
	return 1
}
