package bootstrap

// General API info (title, security scheme, base path) lives above main() in cmd/server/main.go: swag reads
// it only from the file named by `swag init -g`.

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
