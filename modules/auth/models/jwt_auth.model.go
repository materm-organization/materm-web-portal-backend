package models

// TokenResponse is the OAuth2-style token pair returned by this app's local JWT
// refresh flow (pkg/middleware.GenerateJWTToken).
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

// JwtAccessTokenExpiresInSeconds mirrors the MaxAge passed to jwt.SignEncrypted for
// the access token in middleware.GenerateJWTToken (7 days).
const JwtAccessTokenExpiresInSeconds = 7 * 24 * 60 * 60
