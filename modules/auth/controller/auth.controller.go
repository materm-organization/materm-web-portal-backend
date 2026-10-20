package controller

import (
	"go-fiber/modules/auth/models"
	"go-fiber/modules/auth/services"
	"go-fiber/pkg/middleware"

	"github.com/gofiber/fiber/v2"
)

type AuthCtrl interface {
	SignIn(ctx *fiber.Ctx) error
	RefreshToken(ctx *fiber.Ctx) error
	GetMe(ctx *fiber.Ctx) error
}

type authCtrl struct {
	authSvc services.AuthService
}

func (c *authCtrl) SignIn(ctx *fiber.Ctx) error {
	req := new(models.SignInRequest)
	if err := ctx.BodyParser(req); err != nil {
		return middleware.NewErrorResponses(ctx, err)
	}

	token, err := c.authSvc.SignIn(req)
	if err != nil {
		return middleware.NewErrorResponses(ctx, err)
	}

	return middleware.NewSuccessResponse(ctx, token)
}

func (c *authCtrl) RefreshToken(ctx *fiber.Ctx) error {
	req := new(models.RefreshTokenRequest)
	if err := ctx.BodyParser(req); err != nil {
		return middleware.NewErrorResponses(ctx, err)
	}

	token, err := c.authSvc.RefreshToken(req)
	if err != nil {
		return middleware.NewErrorResponses(ctx, err)
	}

	return middleware.NewSuccessResponse(ctx, token)
}

func (c *authCtrl) GetMe(ctx *fiber.Ctx) error {
	claims := middleware.GetKeycloakClaims(ctx)
	if claims == nil {
		return middleware.NewErrorUnauthorized(ctx)
	}

	userInfo, err := c.authSvc.GetUserInfo(claims)
	if err != nil {
		return middleware.NewErrorResponses(ctx, err)
	}

	return middleware.NewSuccessResponse(ctx, userInfo)
}

func NewAuthCtrl(authSvc services.AuthService) AuthCtrl {
	return &authCtrl{
		authSvc: authSvc,
	}
}
