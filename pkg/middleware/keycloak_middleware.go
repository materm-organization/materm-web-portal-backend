package middleware

import (
	"context"
	"fmt"
	"strings"

	"go-fiber/bootstrap"
	"go-fiber/modules/auth/models"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gofiber/fiber/v2"
)

var keycloakVerifier *oidc.IDTokenVerifier

func InitKeycloak() error {
	cfg := bootstrap.GlobalEnv.Keycloak
	issuerURL := fmt.Sprintf("%s/realms/%s", cfg.BaseURL, cfg.Realm)

	provider, err := oidc.NewProvider(context.Background(), issuerURL)
	if err != nil {
		return fmt.Errorf("failed to init keycloak OIDC provider: %w", err)
	}

	keycloakVerifier = provider.Verifier(&oidc.Config{
		ClientID: cfg.ClientID,
	})

	return nil
}

func KeycloakAuth(ctx *fiber.Ctx) error {
	if keycloakVerifier == nil {
		return NewErrorUnauthorized(ctx)
	}

	auth := ctx.Get("Authorization")
	if auth == "" || !strings.HasPrefix(auth, "Bearer ") {
		return NewErrorUnauthorized(ctx)
	}

	tokenStr := strings.TrimPrefix(auth, "Bearer ")

	idToken, err := keycloakVerifier.Verify(ctx.Context(), tokenStr)
	if err != nil {
		return ctx.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"status": false,
			"error":  ErrMsgInvalidAccessToken,
		})
	}

	var claims models.KeycloakClaims
	if err := idToken.Claims(&claims); err != nil {
		return ctx.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"status": false,
			"error":  ErrMsgInvalidToken,
		})
	}

	ctx.Locals("keycloak_claims", &claims)
	ctx.Locals("user_id", claims.Sub)

	return ctx.Next()
}

func RequireRealmRole(roles ...string) fiber.Handler {
	return func(ctx *fiber.Ctx) error {
		claims := GetKeycloakClaims(ctx)
		if claims == nil {
			return NewErrorUnauthorized(ctx)
		}

		for _, required := range roles {
			found := false
			for _, role := range claims.RealmAccess.Roles {
				if role == required {
					found = true
					break
				}
			}
			if !found {
				return NewErrorUnAuthorizeRole(ctx)
			}
		}

		return ctx.Next()
	}
}

func GetKeycloakClaims(ctx *fiber.Ctx) *models.KeycloakClaims {
	claims, ok := ctx.Locals("keycloak_claims").(*models.KeycloakClaims)
	if !ok {
		return nil
	}
	return claims
}
