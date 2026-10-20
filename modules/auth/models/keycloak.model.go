package models

type SignInRequest struct {
	Username string `json:"username" validate:"required"`
	Password string `json:"password" validate:"required"`
}

type RefreshTokenRequest struct {
	RefreshToken string `json:"refresh_token" validate:"required"`
}

type KeycloakClaims struct {
	Sub               string                  `json:"sub"`
	PreferredUsername string                  `json:"preferred_username"`
	Email             string                  `json:"email"`
	EmailVerified     bool                    `json:"email_verified"`
	Name              string                  `json:"name"`
	GivenName         string                  `json:"given_name"`
	FamilyName        string                  `json:"family_name"`
	RealmAccess       RealmAccess             `json:"realm_access"`
	ResourceAccess    map[string]ClientAccess `json:"resource_access"`
	Azp               string                  `json:"azp"`
}

type RealmAccess struct {
	Roles []string `json:"roles"`
}

type ClientAccess struct {
	Roles []string `json:"roles"`
}

type KeycloakTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

type KeycloakErrorResponse struct {
	Error       string `json:"error"`
	Description string `json:"error_description"`
}

type UserInfoResponse struct {
	Sub               string   `json:"sub"`
	PreferredUsername string   `json:"preferred_username"`
	Email             string   `json:"email"`
	EmailVerified     bool     `json:"email_verified"`
	Name              string   `json:"name"`
	GivenName         string   `json:"given_name"`
	FamilyName        string   `json:"family_name"`
	RealmRoles        []string `json:"realm_roles"`
}
