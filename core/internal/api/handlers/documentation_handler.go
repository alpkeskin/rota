package handlers

import (
	"fmt"
	"net/http"

	scalar "github.com/MarceloPetrucio/go-scalar-api-reference"
)

// DocumentationHandler handles API documentation endpoints
type DocumentationHandler struct{}

// NewDocumentationHandler creates a new DocumentationHandler
func NewDocumentationHandler() *DocumentationHandler {
	return &DocumentationHandler{}
}

// ServeDocumentation serves the API documentation using Scalar
func (h *DocumentationHandler) ServeDocumentation(w http.ResponseWriter, r *http.Request) {
	// Construct the full URL for swagger.json
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	swaggerURL := fmt.Sprintf("%s://%s/api/v1/swagger.json", scheme, host)

	htmlContent, err := scalar.ApiReferenceHTML(&scalar.Options{
		SpecURL: swaggerURL,
		CustomOptions: scalar.CustomOptions{
			PageTitle: "Rota Proxy API Documentation",
		},
		DarkMode: true,
	})

	if err != nil {
		http.Error(w, fmt.Sprintf("Error generating documentation: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintln(w, htmlContent)
}
