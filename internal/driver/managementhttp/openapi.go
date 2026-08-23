package managementhttp

import (
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
)

// GenerateOpenAPIYAML produces the management API OpenAPI specification as YAML.
// The returned bytes are a standalone OpenAPI 3.1 document suitable for code
// generation or documentation tooling.
//
// The function creates a throwaway Huma API and registers all management routes
// to extract schema metadata. No HTTP server is started and no route handlers
// are invoked.
func GenerateOpenAPIYAML() ([]byte, error) {
	mux := http.NewServeMux()
	config := huma.DefaultConfig("Ex-Otogi Management API", "1.0.0")
	config.Servers = []*huma.Server{{URL: "/"}}
	api := humago.New(mux, config)

	// registerRoutes is called with a nil query. This is safe because the
	// handlers are never invoked — only their input/output type signatures
	// are needed for OpenAPI schema generation.
	registerRoutes(api, nil)

	yaml, err := api.OpenAPI().YAML()
	if err != nil {
		return nil, fmt.Errorf("generate openapi yaml: %w", err)
	}
	return yaml, nil
}
