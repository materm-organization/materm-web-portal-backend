package application

import (
	as "go-fiber/modules/auth/services"
)

type Services struct {
	JwtAuthService as.JwtAuthService
}

func SetupServices(_ *Repositories) *Services {
	return &Services{
		JwtAuthService: as.NewJwtAuthService(),
	}
}
