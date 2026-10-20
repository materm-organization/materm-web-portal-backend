package services

import (
	"strings"

	"go-fiber/modules/auth/models"
	"go-fiber/pkg/middleware"
)

// JwtAuthService refreshes tokens using this app's own encrypted-JWT scheme
// (pkg/middleware) instead of proxying to Keycloak. It is a standalone
// alternative to AuthService.RefreshToken, not a replacement for it.
type JwtAuthService interface {
	RefreshToken(id, role string) (*models.TokenResponse, error)
}

type jwtAuthService struct{}

func (s *jwtAuthService) RefreshToken(id, role string) (*models.TokenResponse, error) {
	tokenPair, err := middleware.GenerateJWTToken(id, role)
	if err != nil {
		return nil, err
	}

	return &models.TokenResponse{
		AccessToken:  strings.ReplaceAll(string(tokenPair.AccessToken), "\"", ""),
		RefreshToken: strings.ReplaceAll(string(tokenPair.RefreshToken), "\"", ""),
		ExpiresIn:    models.JwtAccessTokenExpiresInSeconds,
		TokenType:    "Bearer",
	}, nil
}

func NewJwtAuthService() JwtAuthService {
	return &jwtAuthService{}
}
