package main

import (
	"fmt"
	"go-fiber/bootstrap"
	"go-fiber/cmd/application"
	"go-fiber/pkg/middleware"
	"log"
)

func main() {
	app := bootstrap.App()

	initMiddleware(app)

	repos := application.SetupRepositories(app.Env.Database, app.Redis)
	services := application.SetupServices(repos)
	controllers := application.SetupControllers(services)
	application.SetupRouter(app.Fiber, controllers)

	log.Fatal(app.Fiber.Listen(fmt.Sprintf(":%d", app.Env.Server.Port)))
}

func initMiddleware(app *bootstrap.Application) {
	if app.Env.Keycloak.Enabled {
		if err := middleware.InitKeycloak(); err != nil {
			log.Fatalf("Failed to initialize Keycloak: %v", err)
		}
	}
}
