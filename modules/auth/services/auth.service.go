package services

import (
	"encoding/json"
	"fmt"
	"go-fiber/bootstrap"
	"go-fiber/modules/auth/models"
	"io"
	"net/http"
	"net/url"
	"strings"
)

type AuthService interface {
	SignIn(req *models.SignInRequest) (*models.KeycloakTokenResponse, error)
	RefreshToken(req *models.RefreshTokenRequest) (*models.KeycloakTokenResponse, error)
	GetUserInfo(claims *models.KeycloakClaims) (*models.UserInfoResponse, error)
}

type authService struct{}

func (s *authService) SignIn(req *models.SignInRequest) (*models.KeycloakTokenResponse, error) {
	cfg := bootstrap.GlobalEnv.Keycloak
	tokenURL := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token", cfg.BaseURL, cfg.Realm)

	data := url.Values{}
	data.Set("grant_type", "password")
	data.Set("client_id", cfg.ClientID)
	data.Set("client_secret", cfg.ClientSecret)
	data.Set("username", req.Username)
	data.Set("password", req.Password)

	resp, err := http.Post(tokenURL, "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to keycloak: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var kcErr models.KeycloakErrorResponse
		if err := json.Unmarshal(body, &kcErr); err != nil {
			return nil, fmt.Errorf("keycloak error: %s", string(body))
		}
		return nil, fmt.Errorf("%s", kcErr.Description)
	}

	var tokenResp models.KeycloakTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	return &tokenResp, nil
}

func (s *authService) RefreshToken(req *models.RefreshTokenRequest) (*models.KeycloakTokenResponse, error) {
	cfg := bootstrap.GlobalEnv.Keycloak
	tokenURL := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token", cfg.BaseURL, cfg.Realm)

	data := url.Values{}
	data.Set("grant_type", "refresh_token")
	data.Set("client_id", cfg.ClientID)
	data.Set("client_secret", cfg.ClientSecret)
	data.Set("refresh_token", req.RefreshToken)

	resp, err := http.Post(tokenURL, "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to keycloak: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var kcErr models.KeycloakErrorResponse
		if err := json.Unmarshal(body, &kcErr); err != nil {
			return nil, fmt.Errorf("keycloak error: %s", string(body))
		}
		return nil, fmt.Errorf("%s", kcErr.Description)
	}

	var tokenResp models.KeycloakTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	return &tokenResp, nil
}

func (s *authService) GetUserInfo(claims *models.KeycloakClaims) (*models.UserInfoResponse, error) {
	return &models.UserInfoResponse{
		Sub:               claims.Sub,
		PreferredUsername: claims.PreferredUsername,
		Email:             claims.Email,
		EmailVerified:     claims.EmailVerified,
		Name:              claims.Name,
		GivenName:         claims.GivenName,
		FamilyName:        claims.FamilyName,
		RealmRoles:        claims.RealmAccess.Roles,
	}, nil
}

func NewAuthService() AuthService {
	return &authService{}
}
