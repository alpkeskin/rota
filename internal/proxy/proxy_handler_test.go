package proxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alpkeskin/rota/internal/config"
	"github.com/elazarl/goproxy"
	"github.com/stretchr/testify/assert"
)

func TestHandleRequest(t *testing.T) {
	cfg := &config.Config{
		Proxy: config.ProxyConfig{
			Authentication: config.ProxyAuthenticationConfig{
				Enabled: false,
			},
			Rotation: config.ProxyRotationConfig{
				Timeout:            1,
				Retries:            1,
				FallbackMaxRetries: 1,
				Method:             "roundrobin",
				RemoveUnhealthy:    true,
			},
		},
	}

	ps := NewProxyServer(cfg)
	proxy := &Proxy{
		Host:   "invalid-proxy:8080",
		Scheme: "http",
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return nil, fmt.Errorf("forced connection error")
			},
			ResponseHeaderTimeout: time.Millisecond,
			DisableKeepAlives:     true,
		},
	}
	ps.AddProxy(proxy)

	req := httptest.NewRequest("GET", "http://example.com", nil)
	ctx := &goproxy.ProxyCtx{
		Req:  req,
		Resp: nil,
	}

	modifiedReq, resp := ps.handleRequest(req, ctx)

	assert.NotNil(t, resp)
	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
	assert.Nil(t, modifiedReq)
}

func TestRemoveHopHeaders(t *testing.T) {
	ps := NewProxyServer(&config.Config{})
	req := httptest.NewRequest("GET", "http://example.com", nil)

	for _, header := range hopHeaders {
		req.Header.Add(header, "test-value")
	}

	ps.removeHopHeaders(req)

	for _, header := range hopHeaders {
		assert.Empty(t, req.Header.Get(header))
	}
}
