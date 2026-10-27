package application

import (
	authController "go-fiber/modules/auth/controller"
)

type Controllers struct {
	JwtAuthController authController.JwtAuthCtrl
}

func SetupControllers(s *Services) *Controllers {
	return &Controllers{
		JwtAuthController: authController.NewJwtAuthCtrl(s.JwtAuthService),
	}
}
