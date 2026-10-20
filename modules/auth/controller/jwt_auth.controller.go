package controller

import (
	"go-fiber/modules/auth/services"
	"go-fiber/pkg/middleware"

	"github.com/gofiber/fiber/v2"
)

// JwtAuthCtrl backs /refresh with this app's own encrypted-JWT scheme instead of
// Keycloak. Standalone alternative to AuthCtrl.RefreshToken — not a replacement.
// The refresh token is read from the Authorization header (validated up front by
// the middleware.AccessRefreshToken route guard), not a JSON request body.
type JwtAuthCtrl interface {
	RefreshToken(ctx *fiber.Ctx) error
}

type jwtAuthCtrl struct {
	jwtAuthSvc services.JwtAuthService
}

func (c *jwtAuthCtrl) RefreshToken(ctx *fiber.Ctx) error {
	claims, err := middleware.GetOwnerRefresh(ctx)
	if err != nil {
		return middleware.NewErrorResponses(ctx, middleware.NewError(fiber.StatusUnauthorized, "invalid or expired refresh token"))
	}

	token, err := c.jwtAuthSvc.RefreshToken(claims.Id, claims.Role)
	if err != nil {
		return middleware.NewErrorResponses(ctx, err)
	}

	return middleware.NewSuccessResponse(ctx, token)
}

func NewJwtAuthCtrl(jwtAuthSvc services.JwtAuthService) JwtAuthCtrl {
	return &jwtAuthCtrl{jwtAuthSvc: jwtAuthSvc}
}
