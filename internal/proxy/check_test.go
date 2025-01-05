package proxy

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/alpkeskin/rota/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestProxyChecker_Check(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Test-Header") == "test-value" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer ts.Close()

	tmpfile, err := os.CreateTemp("", "proxy_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	cfg := &config.Config{
		Healthcheck: config.HealthcheckConfig{
			URL:     ts.URL,
			Status:  200,
			Timeout: 5,
			Workers: 2,
			Headers: []string{"Test-Header:test-value"},
			Output: config.HealthcheckOutputConfig{
				Method: "file",
				File:   tmpfile.Name(),
			},
		},
	}

	proxyServer := &ProxyServer{
		Proxies: []*Proxy{
			{
				Host:      "http://test-proxy:8080",
				Transport: http.DefaultTransport.(*http.Transport),
			},
		},
	}

	checker := NewProxyChecker(cfg, proxyServer)
	err = checker.Check()

	assert.NoError(t, err)

	content, err := os.ReadFile(tmpfile.Name())
	assert.NoError(t, err)
	assert.Contains(t, string(content), "http://test-proxy:8080")
}

func TestProxyChecker_InvalidOutput(t *testing.T) {
	cfg := &config.Config{
		Healthcheck: config.HealthcheckConfig{
			Output: config.HealthcheckOutputConfig{
				Method: "file",
				File:   "/nonexistent/directory/file.txt",
			},
		},
	}

	checker := NewProxyChecker(cfg, &ProxyServer{})
	err := checker.Check()

	assert.Error(t, err)
	assert.Contains(t, err.Error(), msgFailedToCreateOutputFile)
}
