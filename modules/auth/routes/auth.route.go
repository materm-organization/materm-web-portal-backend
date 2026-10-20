package routes

import (
	"go-fiber/modules/auth/controller"
	"go-fiber/pkg/middleware"

	"github.com/gofiber/fiber/v2"
)

func NewAuthRouter(router fiber.Router, ac controller.AuthCtrl, jwtAc controller.JwtAuthCtrl) {
	router.Post("/signin", ac.SignIn)
	router.Post("/refresh", middleware.AccessRefreshToken, jwtAc.RefreshToken)
	authRouter := router.Group("/auth", middleware.KeycloakAuth)

	authRouter.Get("/me", ac.GetMe)
}
