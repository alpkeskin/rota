package proxy

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/alpkeskin/rota/core/internal/models"
	proxyDialer "golang.org/x/net/proxy"
	"h12.io/socks"
)

// transportCache caches *http.Transport per proxy to avoid creating a new
// transport (and new connection pool) for every request.
var transportCache sync.Map

// transportCacheKey builds the cache key for a proxy. It includes the credentials
// so two proxies sharing the same host:port but different creds don't collide, and
// so a credential change produces a different key (AUD-16).
//
// It deliberately does NOT include updated_at: that column is bumped on every
// request (updateProxyStats sets updated_at=NOW()), which would rotate the key on
// every 30s selector refresh — throwing away warm keep-alive connection pools and
// leaking orphaned transports into the cache. Credential rotation is instead
// handled by the explicit ClearTransportCache() wired into proxy Update/Delete.
func transportCacheKey(p *models.Proxy) string {
	user := ""
	if p.Username != nil {
		user = *p.Username
	}
	pass := ""
	if p.Password != nil {
		pass = *p.Password
	}
	return fmt.Sprintf("%s://%s|%s:%s", p.Protocol, p.Address, user, pass)
}

// GetOrCreateTransport returns a cached transport for the given proxy,
// or creates and caches a new one.
func GetOrCreateTransport(p *models.Proxy) (*http.Transport, error) {
	key := transportCacheKey(p)
	if t, ok := transportCache.Load(key); ok {
		return t.(*http.Transport), nil
	}
	t, err := CreateProxyTransport(p)
	if err != nil {
		return nil, err
	}
	actual, _ := transportCache.LoadOrStore(key, t)
	return actual.(*http.Transport), nil
}

// InvalidateTransport removes any cached transport(s) for the given proxy,
// regardless of credentials/version. Callers should invoke this when a proxy is
// updated or deleted so the next request rebuilds with fresh settings.
func InvalidateTransport(p *models.Proxy) {
	prefix := p.Protocol + "://" + p.Address + "|"
	transportCache.Range(func(k, _ any) bool {
		if ks, ok := k.(string); ok && strings.HasPrefix(ks, prefix) {
			transportCache.Delete(ks)
		}
		return true
	})
}

// ClearTransportCache drops all cached transports.
func ClearTransportCache() {
	transportCache.Range(func(k, _ any) bool {
		transportCache.Delete(k)
		return true
	})
}

// proxyDial connects to HTTP(S) upstream proxies.
var proxyDial = &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}

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
		// Timeouts for proxy connections. Without a dial timeout an
		// unreachable proxy fails only by the client timeout, which reads
		// as inconclusive rather than as a proxy fault.
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
		// DialContext dials the proxy itself (the transport then speaks to
		// it per Proxy), so this bounds connecting to the proxy.
		transport.DialContext = proxyDial.DialContext
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

		dialer, err := proxyDialer.SOCKS5("tcp", p.Address, auth, socksForward)
		if err != nil {
			return nil, fmt.Errorf("failed to create SOCKS5 dialer: %w", err)
		}

		transport.Dial = dialer.Dial
	default:
		return nil, fmt.Errorf("unsupported proxy protocol: %s", p.Protocol)
	}

	return transport, nil
}
