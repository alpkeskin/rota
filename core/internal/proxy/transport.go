package proxy

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"time"

	"github.com/alpkeskin/rota/core/internal/models"
	"h12.io/socks"
)

// CreateProxyTransport creates an HTTP transport configured for the given proxy
// This is shared between proxy handler and health checker
func CreateProxyTransport(p *models.Proxy) (*http.Transport, error) {
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,             // Skip certificate verification for proxy connections
			MinVersion:         tls.VersionTLS10, // Support older TLS versions for compatibility
			MaxVersion:         0,                // Allow all TLS versions
			// Don't specify CipherSuites to accept all available ciphers for maximum compatibility
			// This is acceptable since InsecureSkipVerify is already true
		},
		// Timeouts for proxy connections
		// NOTE: Do NOT set DialContext here - it will override Proxy settings!
		// Let http.Transport handle proxy dialing automatically
		TLSHandshakeTimeout:   30 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		ExpectContinueTimeout: 10 * time.Second,
	}

	proxyURL := p.Url()

	switch p.Protocol {
	case "http", "https":
		// Set proxy URL - http.Transport will handle authentication headers automatically
		transport.Proxy = http.ProxyURL(&proxyURL)
	case "socks4", "socks4a", "socks5":
		// Create SOCKS dialer
		transport.Dial = socks.Dial(proxyURL.String())
	default:
		return nil, fmt.Errorf("unsupported proxy protocol: %s", p.Protocol)
	}

	return transport, nil
}
