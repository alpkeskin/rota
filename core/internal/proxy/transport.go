package proxy

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/alpkeskin/rota/core/internal/models"
	"h12.io/socks"
	proxyDialer "golang.org/x/net/proxy"
)

// CreateProxyTransport creates an HTTP transport configured for the given proxy
// This is shared between proxy handler and health checker
func CreateProxyTransport(p *models.Proxy) (*http.Transport, error) {
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,  // Skip certificate verification for proxy connections
			MinVersion:         tls.VersionTLS10, // Support older TLS versions for compatibility
			MaxVersion:         0, // Allow all TLS versions
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

	// Parse proxy URL
	var proxyURL string
	var authMasked string // For logging (hide credentials)

	if p.Username != nil && *p.Username != "" {
		// Username exists, include authentication
		if p.Password != nil && *p.Password != "" {
			// Both username and password
			proxyURL = fmt.Sprintf("%s://%s:%s@%s", p.Protocol, *p.Username, *p.Password, p.Address)
			authMasked = fmt.Sprintf("%s://[username]:[password]@%s", p.Protocol, p.Address)
		} else {
			// Only username (API key), password is empty
			proxyURL = fmt.Sprintf("%s://%s:@%s", p.Protocol, *p.Username, p.Address)
			authMasked = fmt.Sprintf("%s://[api_key]:@%s", p.Protocol, p.Address)
		}
	} else {
		// No authentication
		proxyURL = fmt.Sprintf("%s://%s", p.Protocol, p.Address)
		authMasked = proxyURL
	}

	parsedURL, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy URL %s: %w", authMasked, err)
	}

	switch p.Protocol {
	case "http", "https":
		// Set proxy URL - http.Transport will handle authentication headers automatically
		transport.Proxy = http.ProxyURL(parsedURL)
	case "socks4", "socks4a":
		// Create SOCKS4/SOCKS4A dialer using h12.io/socks
		// The Dial function accepts URI format: socks4://[user@]host:port
		transport.Dial = socks.Dial(proxyURL)
	case "socks5":
		// Create SOCKS5 dialer
		var auth *proxyDialer.Auth
		if p.Username != nil && *p.Username != "" {
			// Username exists, create auth
			password := ""
			if p.Password != nil {
				password = *p.Password
			}
			auth = &proxyDialer.Auth{
				User:     *p.Username,
				Password: password,
			}
		}

		dialer, err := proxyDialer.SOCKS5("tcp", p.Address, auth, proxyDialer.Direct)
		if err != nil {
			return nil, fmt.Errorf("failed to create SOCKS5 dialer: %w", err)
		}

		transport.Dial = dialer.Dial
	default:
		return nil, fmt.Errorf("unsupported proxy protocol: %s", p.Protocol)
	}

	return transport, nil
}
