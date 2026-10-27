package routes

import (
	"github.com/materm-organization/materm-web-portal-backend/modules/auth/controller"
	"github.com/materm-organization/materm-web-portal-backend/pkg/middleware"

	"github.com/gofiber/fiber/v2"
)

// NewAuthRouter registers the only auth route that is left: the local-JWT refresh.
func NewAuthRouter(router fiber.Router, jwtAc controller.JwtAuthCtrl) {
	router.Post("/refresh", middleware.AccessRefreshToken, jwtAc.RefreshToken)
}
