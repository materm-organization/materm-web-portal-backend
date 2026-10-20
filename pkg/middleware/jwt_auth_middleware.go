package middleware

import (
	"encoding/json"
	"fmt"
	"go-fiber/bootstrap"
	"go-fiber/pkg/logs"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/kataras/jwt"
	"go.uber.org/zap"
)

type ClaimsToken struct {
	Id        string `json:"id,omitempty"`
	Role      string `json:"role,omitempty"`
	IssuedAt  int64  `json:"iat,omitempty"`
	ExpiresAt int64  `json:"exp,omitempty"`
}

type TokenPair struct {
	AccessToken  json.RawMessage `json:"access_token,omitempty"`
	RefreshToken json.RawMessage `json:"refresh_token,omitempty"`
}

func GenerateJWTToken(id string, role string) (*TokenPair, error) {
	expiry := time.Hour * 24 * 7 // expire in 7 days
	standardClaims := ClaimsToken{
		Id:        id,
		Role:      role,
		IssuedAt:  time.Now().Unix(),
		ExpiresAt: time.Now().Add(expiry).Unix(),
	}
	encrypt, _, err := jwt.GCM([]byte(bootstrap.GlobalEnv.JWT.Secret), nil)
	if err != nil {

		return nil, err
	}

	token, err := jwt.SignEncrypted(jwt.HS256, []byte(bootstrap.GlobalEnv.JWT.Secret), encrypt, standardClaims, jwt.MaxAge(expiry))
	if err != nil {

		return nil, err
	}
	reEncrypt, _, _ := jwt.GCM([]byte(bootstrap.GlobalEnv.JWT.RefreshSecret), nil)
	refreshToken, err := jwt.SignEncrypted(jwt.HS256, []byte(bootstrap.GlobalEnv.JWT.RefreshSecret), reEncrypt, standardClaims, jwt.MaxAge(time.Hour*24*10)) // expire in 10 days
	if err != nil {

		return nil, err
	}
	tokenPairData := jwt.NewTokenPair(token, refreshToken)
	return &TokenPair{
		AccessToken:  BytesQuote(tokenPairData.AccessToken),
		RefreshToken: BytesQuote(tokenPairData.RefreshToken),
	}, nil
}

func AccessToken(ctx *fiber.Ctx) error {
	_, decrypt, _ := jwt.GCM([]byte(bootstrap.GlobalEnv.JWT.Secret), nil)
	auth := ctx.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return NewErrorUnauthorized(ctx)
	}
	jwtFromHeader := strings.TrimSpace(auth[7:])
	verifiedToken, err := jwt.VerifyEncrypted(jwt.HS256, []byte(bootstrap.GlobalEnv.JWT.Secret), decrypt, []byte(jwtFromHeader))
	if err != nil {
		logs.Error("JWT verification error in AccessToken middleware: %s", zap.String("error", err.Error()))
		return NewErrorUnauthorized(ctx)
	}
	var claims ClaimsToken
	err = verifiedToken.Claims(&claims)
	if err != nil {
		return ctx.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"status": false,
			"result": err.Error(),
		})
	}
	return ctx.Next()
}

func AccessRefreshToken(ctx *fiber.Ctx) error {
	_, decrypt, _ := jwt.GCM([]byte(bootstrap.GlobalEnv.JWT.RefreshSecret), nil)
	auth := ctx.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return NewErrorUnauthorized(ctx)
	}
	jwtFromHeader := strings.TrimSpace(auth[7:])
	verifiedToken, err := jwt.VerifyEncrypted(jwt.HS256, []byte(bootstrap.GlobalEnv.JWT.RefreshSecret), decrypt, []byte(jwtFromHeader))
	if err != nil {
		return ctx.Status(fiber.StatusExpectationFailed).JSON(fiber.Map{
			"status": false,
			"result": err.Error(),
		})
	}
	var claims ClaimsToken
	err = verifiedToken.Claims(&claims)
	if err != nil {
		return ctx.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"status": false,
			"result": err.Error(),
		})
	}
	return ctx.Next()
}
func GetOwnerAccessToken(c *fiber.Ctx) (string, error) {
	_, decrypt, _ := jwt.GCM([]byte(bootstrap.GlobalEnv.JWT.Secret), nil)
	auth := c.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return "", NewErrorUnauthorized(c)
	}
	jwtFromHeader := strings.TrimSpace(auth[7:])
	verifiedToken, err := jwt.VerifyEncrypted(jwt.HS256, []byte(bootstrap.GlobalEnv.JWT.Secret), decrypt, []byte(jwtFromHeader))
	if err != nil {
		return "", err
	}
	var claims ClaimsToken
	err = verifiedToken.Claims(&claims)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%v", claims.Id), nil
}
func GetInfoAccessToken(c *fiber.Ctx) (*ClaimsToken, error) {
	_, decrypt, _ := jwt.GCM([]byte(bootstrap.GlobalEnv.JWT.Secret), nil)
	auth := c.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return nil, NewErrorUnauthorized(c)
	}
	jwtFromHeader := strings.TrimSpace(auth[7:])
	verifiedToken, err := jwt.VerifyEncrypted(jwt.HS256, []byte(bootstrap.GlobalEnv.JWT.Secret), decrypt, []byte(jwtFromHeader))
	if err != nil {
		return nil, err
	}
	var claims ClaimsToken
	err = verifiedToken.Claims(&claims)
	if err != nil {
		return nil, err
	}
	return &claims, nil
}

func GetOwnerRefresh(c *fiber.Ctx) (*ClaimsToken, error) {
	_, decrypt, _ := jwt.GCM([]byte(bootstrap.GlobalEnv.JWT.RefreshSecret), nil)
	auth := c.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return nil, NewErrorUnauthorized(c)
	}
	jwtFromHeader := strings.TrimSpace(auth[7:])

	verifiedToken, err := jwt.VerifyEncrypted(jwt.HS256, []byte(bootstrap.GlobalEnv.JWT.RefreshSecret), decrypt, []byte(jwtFromHeader))
	if err != nil {
		return nil, err
	}
	var claims ClaimsToken
	err = verifiedToken.Claims(&claims)
	if err != nil {
		return nil, err
	}
	return &claims, nil
}

// TODO: Refresh token
func GenerateRefreshToken(ctx *fiber.Ctx) (*TokenPair, error) {
	auth := ctx.Get("Authorization")
	if auth == "" {
		return nil, NewErrorUnauthorized(ctx)
	}
	claims, err := GetOwnerRefresh(ctx)
	if err != nil {
		return nil, err
	}
	standardClaims := ClaimsToken{
		Id:        claims.Id,
		IssuedAt:  time.Now().Unix(),
		ExpiresAt: time.Now().Add(time.Hour * 24 * 7).Unix(),
	}
	encrypt, _, _ := jwt.GCM([]byte(bootstrap.GlobalEnv.JWT.Secret), nil)
	token, err := jwt.SignEncrypted(jwt.HS256, []byte(bootstrap.GlobalEnv.JWT.Secret), encrypt, standardClaims, jwt.MaxAge(time.Second*15))
	if err != nil {

		return nil, err
	}
	reEncrypt, _, _ := jwt.GCM([]byte(bootstrap.GlobalEnv.JWT.RefreshSecret), nil)
	refreshToken, err := jwt.SignEncrypted(jwt.HS256, []byte(bootstrap.GlobalEnv.JWT.RefreshSecret), reEncrypt, standardClaims, jwt.MaxAge(time.Hour*24*8))
	if err != nil {

		return nil, err
	}
	tokenPairData := jwt.NewTokenPair(token, refreshToken)
	return &TokenPair{
		AccessToken:  BytesQuote(tokenPairData.AccessToken),
		RefreshToken: BytesQuote(tokenPairData.RefreshToken),
	}, nil
}

func BytesQuote(b []byte) []byte {
	dst := make([]byte, len(b)+2)
	dst[0] = '"'
	copy(dst[1:], b)
	dst[len(dst)-1] = '"'
	return dst
}
