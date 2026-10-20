package application

import (
	authController "go-fiber/modules/auth/controller"
)

type Controllers struct {
	AuthController    authController.AuthCtrl
	JwtAuthController authController.JwtAuthCtrl
}

func SetupControllers(s *Services) *Controllers {
	return &Controllers{
		AuthController:    authController.NewAuthCtrl(s.AuthService),
		JwtAuthController: authController.NewJwtAuthCtrl(s.JwtAuthService),
	}
}
