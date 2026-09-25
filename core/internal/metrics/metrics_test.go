package metrics

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func scrape(t *testing.T, h http.Handler, auth string) (int, string) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	b, _ := io.ReadAll(w.Result().Body)
	return w.Code, string(b)
}

func TestHandlerExposesMetrics(t *testing.T) {
	ObserveProxyRequest("http", "success", 150*time.Millisecond)
	code, body := scrape(t, Handler(""), "")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	for _, want := range []string{
		`rota_proxy_requests_total{kind="http",outcome="success"}`,
		`rota_proxy_request_duration_seconds_bucket{kind="http",outcome="success",le="0.25"}`,
		`rota_build_info{version=`,
		`go_goroutines`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %q", want)
		}
	}
}

func TestHandlerToken(t *testing.T) {
	h := Handler("s3cret")
	for _, tc := range []struct {
		auth string
		want int
	}{
		{"", http.StatusUnauthorized},
		{"Bearer wrong", http.StatusUnauthorized},
		{"Basic s3cret", http.StatusUnauthorized},
		{"Bearer s3cret", http.StatusOK},
	} {
		if code, _ := scrape(t, h, tc.auth); code != tc.want {
			t.Errorf("auth %q: status = %d, want %d", tc.auth, code, tc.want)
		}
	}
}

func TestUpstreamCollector(t *testing.T) {
	c := &upstreamCollector{}
	ok := func(context.Context) (map[string]int, error) { return map[string]int{"active": 3, "failed": 1}, nil }
	fail := func(context.Context) (map[string]int, error) { return nil, errors.New("db down") }

	RegisterUpstreamInventory(func(ctx context.Context) (map[string]int, error) { return c.count(ctx) })

	c.count = ok
	_, body := scrape(t, Handler(""), "")
	for _, want := range []string{`rota_upstream_proxies{status="active"} 3`, `rota_upstream_proxies{status="failed"} 1`, `rota_upstream_inventory_up 1`} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %q", want)
		}
	}

	c.count = fail
	code, body := scrape(t, Handler(""), "")
	if code != http.StatusOK || !strings.Contains(body, "rota_upstream_inventory_up 0") || strings.Contains(body, "rota_upstream_proxies{") {
		t.Errorf("failing inventory: status %d, body lacks up=0 or still has counts", code)
	}
}

func TestMethodLabelBounded(t *testing.T) {
	for m, want := range map[string]string{"GET": "GET", "DELETE": "DELETE", "M0": "OTHER", "get": "OTHER", "": "OTHER"} {
		if got := MethodLabel(m); got != want {
			t.Errorf("MethodLabel(%q) = %q, want %q", m, got, want)
		}
	}
}
