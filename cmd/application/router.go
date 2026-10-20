package application

import (
	"fmt"
	ar "go-fiber/modules/auth/routes"
	"go-fiber/pkg/constants"
	"os"
	"time"

	"github.com/gofiber/fiber/v2"
)

func SetupRouter(app fiber.Router, c *Controllers) {
	api := app.Group(constants.MainRoute, func(ctx *fiber.Ctx) error {
		return ctx.Next()
	})

	app.Get(constants.HealthCheckRoute, HealthCheck)

	// Auth routes
	ar.NewAuthRouter(api, c.AuthController, c.JwtAuthController)
}

// HealthCheck godoc
// @Summary Show the status of server.
// @Description get the status of server.
// @Tags HealthCheck
// @Accept */*
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /api/health [get]
func HealthCheck(c *fiber.Ctx) error {
	hostname, err := os.Hostname()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("Error getting hostname: %s", err),
		})
	}
	currentTime := time.Now().Format(time.RFC3339)
	if err := c.Status(200).JSON(fiber.Map{
		"service-name": "materm-web-portal-api",
		"hostname":     hostname,
		"timestamp":    currentTime,
		"msg":          "OK",
	}); err != nil {
		return err
	}
	return nil
}
