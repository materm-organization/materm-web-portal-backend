package bootstrap

// @title Materm Web Portal API
// @version 1.0.0
// @description Backend API for the Materm Web Portal

// IMPORTANT: every /api/v1 endpoint requires the server-to-server portal key in the X-Portal-Key header
// (doc/08 section 2.9). The header is documented as a security scheme, its value is never shown.

// @termsOfService http://swagger.io/terms/

// @contact.name API Support
// @contact.url http://www.swagger.io/support
// @contact.email support@swagger.io

// @license.name Apache 2.0
// @license.url http://www.apache.org/licenses/LICENSE-2.0.html

// @BasePath /api/v1
// @schemes http https

// @securityDefinitions.apikey PortalKey
// @in header
// @name X-Portal-Key
// @description Shared secret of the portal-frontend server (portal.frontendKeys).

import (
	"fmt"
	// _ "github.com/materm-organization/materm-web-portal-backend/docs"
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
func NewSwaggerConfig(port int) *SwaggerConfig {
	host := fmt.Sprintf("localhost:%d", port)
	return &SwaggerConfig{
		Title:       "Materm Web Portal API",
		Description: "Backend API for the Materm Web Portal",
		Version:     "1.0.0",
		Host:        host,
		BasePath:    "/api/v1",
		Schemes:     []string{"http", "https"},
	}
}
