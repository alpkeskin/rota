package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alpkeskin/rota/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestNewApi(t *testing.T) {
	cfg := &config.Config{
		Api: config.ApiConfig{
			Port: 8081,
		},
	}

	api := NewApi(cfg)
	assert.NotNil(t, api)
	assert.Equal(t, cfg, api.cfg)
}

func TestHandleMetrics(t *testing.T) {
	testCases := []struct {
		name          string
		method        string
		expectedCode  int
		checkResponse bool
	}{
		{
			name:          "Success",
			method:        http.MethodGet,
			expectedCode:  http.StatusOK,
			checkResponse: true,
		},
		{
			name:          "Invalid HTTP method",
			method:        http.MethodPost,
			expectedCode:  http.StatusMethodNotAllowed,
			checkResponse: false,
		},
	}

	cfg := &config.Config{
		Api: config.ApiConfig{
			Port: 8080,
		},
	}
	api := NewApi(cfg)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "/metrics", nil)
			w := httptest.NewRecorder()

			api.handleMetrics(w, req)

			assert.Equal(t, tc.expectedCode, w.Code)

			if tc.checkResponse {
				var response metrics
				err := json.NewDecoder(w.Body).Decode(&response)
				assert.NoError(t, err)

				assert.NotEmpty(t, response.Timestamp)
				assert.NotEmpty(t, response.Status)
				assert.NotZero(t, response.TotalMemory)
				assert.NotZero(t, response.GoRoutines)
				assert.NotZero(t, response.ThreadCount)
			}
		})
	}
}

func TestCollectMetrics(t *testing.T) {
	metrics, err := collectMetrics()

	assert.NoError(t, err)
	assert.NotNil(t, metrics)

	assert.NotEmpty(t, metrics.Timestamp)
	assert.Equal(t, "healthy", metrics.Status)
	assert.GreaterOrEqual(t, metrics.TotalMemory, uint64(0))
	assert.GreaterOrEqual(t, metrics.CPUUsage, float64(0))
	assert.GreaterOrEqual(t, metrics.DiskTotal, uint64(0))
	assert.GreaterOrEqual(t, metrics.GoRoutines, 0)
	assert.GreaterOrEqual(t, metrics.ThreadCount, 0)
}
