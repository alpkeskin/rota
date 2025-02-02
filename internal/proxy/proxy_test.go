package proxy

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/alpkeskin/rota/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestNewProxyServer(t *testing.T) {
	cfg := &config.Config{
		Proxy: config.ProxyConfig{
			Port: 8080,
		},
	}

	ps := NewProxyServer(cfg)

	assert.NotNil(t, ps)
	assert.NotNil(t, ps.goProxy)
	assert.Empty(t, ps.Proxies)
	assert.Empty(t, ps.ProxyHistory)
	assert.Equal(t, cfg, ps.cfg)
	assert.Equal(t, 0, len(ps.Proxies))
	assert.Equal(t, 0, len(ps.ProxyHistory))
}

func TestAddProxy(t *testing.T) {
	ps := NewProxyServer(&config.Config{})
	proxy := &Proxy{
		Host:   "test.proxy:8080",
		Scheme: "http",
	}

	ps.AddProxy(proxy)

	assert.Equal(t, 1, len(ps.Proxies))
	assert.Equal(t, proxy, ps.Proxies[0])
}

func TestSetUpHandlers(t *testing.T) {
	cfg := &config.Config{
		Proxy: config.ProxyConfig{
			Port: 8080,
		},
	}

	ps := NewProxyServer(cfg)

	ps.setUpHandlers()

	assert.NotNil(t, ps.goProxy)
}

func TestGetProxy(t *testing.T) {
	tests := []struct {
		name           string
		rotationMethod string
		proxyCount     int
		wantNil        bool
	}{
		{
			name:           "Random rotation",
			rotationMethod: "random",
			proxyCount:     3,
			wantNil:        false,
		},
		{
			name:           "Round robin rotation",
			rotationMethod: "roundrobin",
			proxyCount:     3,
			wantNil:        false,
		},
		{
			name:           "Invalid rotation method",
			rotationMethod: "invalid",
			proxyCount:     3,
			wantNil:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				Proxy: config.ProxyConfig{
					Rotation: config.ProxyRotationConfig{
						Method: tt.rotationMethod,
					},
				},
			}
			ps := NewProxyServer(cfg)

			for i := 0; i < tt.proxyCount; i++ {
				ps.AddProxy(&Proxy{
					Scheme: "http",
					Host:   fmt.Sprintf("example%d.com", i),
				})
			}

			proxy := ps.getProxy()
			if tt.wantNil {
				assert.Nil(t, proxy)
			} else {
				assert.NotNil(t, proxy)
			}
		})
	}
}

func TestRemoveUnhealthyProxy(t *testing.T) {
	ps := NewProxyServer(&config.Config{})

	proxy1 := &Proxy{Host: "proxy1.com"}
	proxy2 := &Proxy{Host: "proxy2.com"}
	proxy3 := &Proxy{Host: "proxy3.com"}

	ps.AddProxy(proxy1)
	ps.AddProxy(proxy2)
	ps.AddProxy(proxy3)

	ps.removeUnhealthyProxy(proxy2)

	assert.Len(t, ps.Proxies, 2)
	assert.Equal(t, proxy1, ps.Proxies[0])
	assert.Equal(t, proxy3, ps.Proxies[1])
}

func TestUnauthorizedResponse(t *testing.T) {
	ps := NewProxyServer(&config.Config{})
	req, _ := http.NewRequest("GET", "http://example.com", nil)

	reqInfo := requestInfo{
		id:      "test-id",
		request: req,
	}

	_, resp := ps.unauthorizedResponse(reqInfo)

	assert.Equal(t, StatusProxyAuthRequired, resp.StatusCode)
	assert.Equal(t, "Proxy Authentication Required", resp.Status)
}

func TestBadGatewayResponse(t *testing.T) {
	ps := NewProxyServer(&config.Config{})
	req, _ := http.NewRequest("GET", "http://example.com", nil)

	reqInfo := requestInfo{
		id:      "test-id",
		request: req,
	}

	_, resp := ps.badGatewayResponse(reqInfo, errors.New("test error"))

	assert.Equal(t, StatusBadGateway, resp.StatusCode)
	assert.Equal(t, "Bad Gateway", resp.Status)
}

func TestProxyRotation(t *testing.T) {
	ps := NewProxyServer(&config.Config{
		Proxy: config.ProxyConfig{
			Rotation: config.ProxyRotationConfig{
				Method: "least_conn",
			},
		},
	})

	proxy1 := &Proxy{Host: "proxy1", UsageCount: 5}
	proxy2 := &Proxy{Host: "proxy2", UsageCount: 3}
	proxy3 := &Proxy{Host: "proxy3", UsageCount: 3}

	ps.AddProxy(proxy1)
	ps.AddProxy(proxy2)
	ps.AddProxy(proxy3)

	selectedProxy := ps.getProxy()
	assert.Contains(t, []*Proxy{proxy2, proxy3}, selectedProxy)
}
