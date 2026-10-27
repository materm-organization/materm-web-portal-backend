package application

import (
	authController "github.com/materm-organization/materm-web-portal-backend/modules/auth/controller"
)

type Controllers struct {
	JwtAuthController authController.JwtAuthCtrl
}

func SetupControllers(s *Services) *Controllers {
	return &Controllers{
		JwtAuthController: authController.NewJwtAuthCtrl(s.JwtAuthService),
	}
}
