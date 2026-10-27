package routes

import (
	"go-fiber/modules/auth/controller"
	"go-fiber/pkg/middleware"

	"github.com/gofiber/fiber/v2"
)

// NewAuthRouter registers the only auth route that is left: the local-JWT refresh.
func NewAuthRouter(router fiber.Router, jwtAc controller.JwtAuthCtrl) {
	router.Post("/refresh", middleware.AccessRefreshToken, jwtAc.RefreshToken)
}
