package middleware

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"testing"

	"github.com/alpkeskin/rota/internal/config"
	"github.com/elazarl/goproxy"
	"github.com/stretchr/testify/assert"
)

func TestProxyAuth(t *testing.T) {
	cfg := &config.Config{
		Proxy: config.ProxyConfig{
			Authentication: config.ProxyAuthenticationConfig{
				Username: "testuser",
				Password: "testpass",
			},
		},
	}
	middleware := NewMiddleware(cfg)

	tests := []struct {
		name          string
		authHeader    string
		expectedError error
	}{
		{
			name:          "no auth header",
			authHeader:    "",
			expectedError: fmt.Errorf("no auth header"),
		},
		{
			name:          "invalid base64",
			authHeader:    "Basic invalid-base64",
			expectedError: fmt.Errorf("invalid auth credentials"),
		},
		{
			name:          "invalid auth format",
			authHeader:    "Basic " + base64.StdEncoding.EncodeToString([]byte("invalid")),
			expectedError: fmt.Errorf("invalid auth credentials"),
		},
		{
			name:          "wrong credentials",
			authHeader:    "Basic " + base64.StdEncoding.EncodeToString([]byte("wrong:credentials")),
			expectedError: fmt.Errorf("invalid auth credentials"),
		},
		{
			name:          "valid credentials",
			authHeader:    "Basic " + base64.StdEncoding.EncodeToString([]byte("testuser:testpass")),
			expectedError: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "https://example.com", nil)
			if tt.authHeader != "" {
				req.Header.Set("Proxy-Authorization", tt.authHeader)
			}

			ctx := &goproxy.ProxyCtx{
				Req: req,
			}

			err := middleware.ProxyAuth(ctx)
			if tt.expectedError == nil {
				assert.Nil(t, err)
			} else {
				assert.Equal(t, tt.expectedError.Error(), err.Error())
			}
		})
	}
}
