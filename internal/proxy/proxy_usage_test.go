package proxy

import (
	"testing"
	"time"

	"github.com/alpkeskin/rota/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestUpdateProxyUsage(t *testing.T) {
	ps := NewProxyServer(&config.Config{})
	proxy := &Proxy{
		Host:   "test.proxy:8080",
		Scheme: "http",
	}

	reqInfo := requestInfo{
		id:      "test-id",
		url:     "http://example.com",
		startAt: time.Now(),
	}

	duration := 100 * time.Millisecond
	ps.updateProxyUsage(proxy, reqInfo, duration, "success")

	assert.Equal(t, "success", proxy.LatestUsageStatus)
	assert.Equal(t, 1, proxy.UsageCount)
	assert.Equal(t, duration.String(), proxy.LatestUsageDuration)
	assert.Equal(t, duration.String(), proxy.AvgUsageDuration)
	assert.Equal(t, 1, len(ps.ProxyHistory))
}

func TestProxyHistoryLimit(t *testing.T) {
	ps := NewProxyServer(&config.Config{})
	proxy := &Proxy{
		Host:   "test.proxy:8080",
		Scheme: "http",
	}

	reqInfo := requestInfo{
		id:      "test-id",
		url:     "http://example.com",
		startAt: time.Now(),
	}

	for i := 0; i < 1010; i++ {
		ps.updateProxyUsage(proxy, reqInfo, time.Millisecond, "success")
	}

	assert.Equal(t, 1000, len(ps.ProxyHistory))
}
