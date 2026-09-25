package proxy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/pkg/logger"
)

// leakySelector fails with an error that names an upstream address, the kind
// of detail that must stay in server logs.
type leakySelector struct{}

func (leakySelector) Select(context.Context) (*models.Proxy, error) {
	return nil, errors.New("dial tcp 10.66.66.66:3128: connection refused")
}
func (leakySelector) Refresh(context.Context) error { return nil }

func TestUpstreamErrorsDoNotLeakDetails(t *testing.T) {
	h := NewUpstreamProxyHandler(leakySelector{}, nil, &models.RotationSettings{Retries: 1}, logger.New("error"))

	for _, tc := range []struct {
		name  string
		req   *http.Request
		serve func(http.ResponseWriter, *http.Request)
	}{
		{"http", httptest.NewRequest(http.MethodGet, "http://example.com/", nil), h.HandleHTTPRequest},
		{"connect", httptest.NewRequest(http.MethodConnect, "http://example.com:443", nil), h.HandleConnectRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			tc.serve(w, tc.req)
			if w.Code != http.StatusBadGateway {
				t.Fatalf("status = %d, want 502", w.Code)
			}
			body := w.Body.String()
			if strings.Contains(body, "10.66.66.66") || strings.Contains(body, "refused") {
				t.Fatalf("response leaks upstream error: %q", body)
			}
			id := w.Header().Get(RequestIDHeader)
			if id == "" || !strings.Contains(body, id) {
				t.Fatalf("request id missing or not in body: header=%q body=%q", id, body)
			}
		})
	}
}
