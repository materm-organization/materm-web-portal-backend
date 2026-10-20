package application

import (
	as "go-fiber/modules/auth/services"
)

type Services struct {
	AuthService    as.AuthService
	JwtAuthService as.JwtAuthService
}

func SetupServices(_ *Repositories) *Services {
	return &Services{
		AuthService:    as.NewAuthService(),
		JwtAuthService: as.NewJwtAuthService(),
	}
}
