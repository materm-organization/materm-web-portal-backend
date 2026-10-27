package bootstrap

import (
	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"

	"github.com/materm-organization/materm-web-portal-backend/pkg/logs"
)

// Application is the assembled infrastructure of the server. Redis is nil when Redis was unreachable in dev
// (degraded mode: use cache.NewRedisRepository, which then returns cache.Unavailable()).
type Application struct {
	Env     *Env
	Fiber   *fiber.App
	Redis   *redis.Client
	Swagger *SwaggerConfig
}

// App loads and validates the configuration (fail fast: every violated rule is reported by key, never by
// value), then builds Fiber and Redis. Warnings are logged. It does not connect to PostgreSQL: cmd/application
// does, so that the same wiring runs in the boot tests.
func App() (*Application, error) {
	env, err := NewEnv()
	if err != nil {
		return nil, err
	}
	warnings, err := env.Validate()
	for _, w := range warnings {
		logs.Warn("config: " + w)
	}
	if err != nil {
		return nil, err
	}

	app := &Application{Env: env}
	app.Fiber = NewFiber(env.Server)
	app.Redis, err = InitializeRedis(env.Redis, env.Strict())
	if err != nil {
		return nil, err
	}
	if !env.Strict() {
		app.Swagger = NewSwaggerConfig(env.Server.Port)
	}
	return app, nil
}
