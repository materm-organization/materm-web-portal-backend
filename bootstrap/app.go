package bootstrap

import (
	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
)

type Application struct {
	Env     *Env
	Fiber   *fiber.App
	Redis   *redis.Client
	Swagger *SwaggerConfig
}

var GlobalEnv Env

func App() *Application {
	app := &Application{}
	app.Env = NewEnv()
	GlobalEnv = *app.Env
	app.Fiber = NewFiber(app.Env.Server)
	app.Redis = InitializeRedis(app.Env.Redis)
	// app.Swagger = NewSwaggerConfig()
	return app
}
