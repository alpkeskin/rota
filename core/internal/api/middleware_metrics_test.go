package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alpkeskin/rota/core/internal/metrics"
	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestMetricsMiddlewareLabelsByRoutePattern(t *testing.T) {
	r := chi.NewRouter()
	r.Use(MetricsMiddleware())
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/proxies/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	})

	before := testutil.ToFloat64(metrics.APIRequests.WithLabelValues("/api/v1/proxies/{id}", "GET", "418"))
	for _, id := range []string{"1", "2", "3"} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/proxies/"+id, nil))
	}
	if got := testutil.ToFloat64(metrics.APIRequests.WithLabelValues("/api/v1/proxies/{id}", "GET", "418")) - before; got != 3 {
		t.Fatalf("pattern-labelled count = %v, want 3", got)
	}

	beforeUnmatched := testutil.ToFloat64(metrics.APIRequests.WithLabelValues("unmatched", "GET", "404"))
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/nope/"+strings.Repeat("x", 10), nil))
	if got := testutil.ToFloat64(metrics.APIRequests.WithLabelValues("unmatched", "GET", "404")) - beforeUnmatched; got != 1 {
		t.Fatalf("unmatched count = %v, want 1", got)
	}
}

func TestMetricsMiddlewareStatusWhenNothingWritten(t *testing.T) {
	r := chi.NewRouter()
	r.Use(MetricsMiddleware())
	r.Get("/empty", func(http.ResponseWriter, *http.Request) {}) // implicit 200
	r.Get("/ws/stream", func(http.ResponseWriter, *http.Request) {})

	before200 := testutil.ToFloat64(metrics.APIRequests.WithLabelValues("/empty", "GET", "200"))
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/empty", nil))
	if got := testutil.ToFloat64(metrics.APIRequests.WithLabelValues("/empty", "GET", "200")) - before200; got != 1 {
		t.Fatalf("empty handler not labelled 200 (delta %v)", got)
	}

	before101 := testutil.ToFloat64(metrics.APIRequests.WithLabelValues("/ws/stream", "GET", "101"))
	req := httptest.NewRequest(http.MethodGet, "/ws/stream", nil)
	req.Header.Set("Upgrade", "websocket")
	r.ServeHTTP(httptest.NewRecorder(), req)
	if got := testutil.ToFloat64(metrics.APIRequests.WithLabelValues("/ws/stream", "GET", "101")) - before101; got != 1 {
		t.Fatalf("websocket upgrade not labelled 101 (delta %v)", got)
	}
}
