package bootstrap

// @title Materm Web Portal API
// @version 1.0.0
// @description Backend API for the Materm Web Portal

// IMPORTANT: All API endpoints require an Access token for authentication.
// Please contact the administrator to obtain your API key.
// Include the API key in the X-API-Key header for all requests.

// @termsOfService http://swagger.io/terms/

// @contact.name API Support
// @contact.url http://www.swagger.io/support
// @contact.email support@swagger.io

// @license.name Apache 2.0
// @license.url http://www.apache.org/licenses/LICENSE-2.0.html

// @BasePath /api/v1
// @schemes http https

// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
// @description Type "Bearer" followed by a space and JWT token.

import (
	"fmt"
	// _ "go-fiber/docs"
)

// SwaggerConfig holds Swagger configuration
type SwaggerConfig struct {
	Title       string
	Description string
	Version     string
	Host        string
	BasePath    string
	Schemes     []string
}

// NewSwaggerConfig creates a new Swagger configuration
func NewSwaggerConfig() *SwaggerConfig {
	host := fmt.Sprintf("localhost:%d", GlobalEnv.Server.Port)
	return &SwaggerConfig{
		Title:       "Materm Web Portal API",
		Description: "Backend API for the Materm Web Portal",
		Version:     "1.0.0",
		Host:        host,
		BasePath:    "/api/v1",
		Schemes:     []string{"http", "https"},
	}
}
