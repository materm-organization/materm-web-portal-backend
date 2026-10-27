package application

import (
	as "github.com/materm-organization/materm-web-portal-backend/modules/auth/services"
)

type Services struct {
	JwtAuthService as.JwtAuthService
}

func SetupServices(_ *Repositories) *Services {
	return &Services{
		JwtAuthService: as.NewJwtAuthService(),
	}
}
