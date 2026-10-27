package services

import (
	"strings"

	"github.com/materm-organization/materm-web-portal-backend/modules/auth/models"
	"github.com/materm-organization/materm-web-portal-backend/pkg/middleware"
)

// JwtAuthService refreshes tokens using this app's own encrypted-JWT scheme
// (pkg/middleware).
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
