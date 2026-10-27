package main

import (
	"fmt"
	"go-fiber/bootstrap"
	"go-fiber/cmd/application"
	"log"
)

func main() {
	app := bootstrap.App()

	repos := application.SetupRepositories(app.Env.Database, app.Redis)
	services := application.SetupServices(repos)
	controllers := application.SetupControllers(services)
	application.SetupRouter(app.Fiber, controllers)

	log.Fatal(app.Fiber.Listen(fmt.Sprintf(":%d", app.Env.Server.Port)))
}
