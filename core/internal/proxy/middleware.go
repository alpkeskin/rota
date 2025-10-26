package proxy

import (
	"encoding/base64"
	"net/http"
	"strings"
	"sync"

	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/elazarl/goproxy"
	"golang.org/x/time/rate"
)

// AuthMiddleware handles proxy authentication
type AuthMiddleware struct {
	enabled  bool
	username string
	password string
	mu       sync.RWMutex
}

// NewAuthMiddleware creates a new authentication middleware
func NewAuthMiddleware(settings models.AuthenticationSettings) *AuthMiddleware {
	return &AuthMiddleware{
		enabled:  settings.Enabled,
		username: settings.Username,
		password: settings.Password,
	}
}

// UpdateSettings updates the authentication settings
func (m *AuthMiddleware) UpdateSettings(settings models.AuthenticationSettings) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.enabled = settings.Enabled
	m.username = settings.Username
	m.password = settings.Password
}

// HandleRequest validates proxy authentication for HTTP requests
func (m *AuthMiddleware) HandleRequest(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.enabled {
		return req, nil
	}

	// Check Proxy-Authorization header
	proxyAuth := req.Header.Get("Proxy-Authorization")
	if proxyAuth == "" {
		return req, m.unauthorized()
	}

	// Parse Basic authentication
	if !strings.HasPrefix(proxyAuth, "Basic ") {
		return req, m.unauthorized()
	}

	encoded := strings.TrimPrefix(proxyAuth, "Basic ")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return req, m.unauthorized()
	}

	// Split username:password
	credentials := strings.SplitN(string(decoded), ":", 2)
	if len(credentials) != 2 {
		return req, m.unauthorized()
	}

	username := credentials[0]
	password := credentials[1]

	// Validate credentials
	if username != m.username || password != m.password {
		return req, m.unauthorized()
	}

	// Authentication successful, remove the header before forwarding
	req.Header.Del("Proxy-Authorization")
	return req, nil
}

// HandleConnect validates proxy authentication for HTTPS CONNECT requests
func (m *AuthMiddleware) HandleConnect(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
	return m.HandleRequest(req, ctx)
}

// unauthorized returns a 407 Proxy Authentication Required response
func (m *AuthMiddleware) unauthorized() *http.Response {
	resp := &http.Response{
		StatusCode: http.StatusProxyAuthRequired,
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
	}
	resp.Header.Set("Proxy-Authenticate", `Basic realm="Rota Proxy"`)
	return resp
}

// RateLimitMiddleware handles per-IP rate limiting
type RateLimitMiddleware struct {
	enabled     bool
	interval    int // seconds
	maxRequests int
	limiters    map[string]*rate.Limiter
	mu          sync.RWMutex
}

// NewRateLimitMiddleware creates a new rate limiting middleware
func NewRateLimitMiddleware(settings models.RateLimitSettings) *RateLimitMiddleware {
	return &RateLimitMiddleware{
		enabled:     settings.Enabled,
		interval:    settings.Interval,
		maxRequests: settings.MaxRequests,
		limiters:    make(map[string]*rate.Limiter),
	}
}

// UpdateSettings updates the rate limit settings
func (m *RateLimitMiddleware) UpdateSettings(settings models.RateLimitSettings) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.enabled = settings.Enabled
	m.interval = settings.Interval
	m.maxRequests = settings.MaxRequests

	// Clear existing limiters to apply new settings
	m.limiters = make(map[string]*rate.Limiter)
}

// HandleRequest validates rate limits for HTTP requests
func (m *RateLimitMiddleware) HandleRequest(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
	if !m.enabled {
		return req, nil
	}

	// Get client IP
	clientIP := m.getClientIP(req)

	// Check rate limit
	if !m.allow(clientIP) {
		return req, m.tooManyRequests()
	}

	return req, nil
}

// HandleConnect validates rate limits for HTTPS CONNECT requests
func (m *RateLimitMiddleware) HandleConnect(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
	return m.HandleRequest(req, ctx)
}

// allow checks if the request is allowed based on rate limiting
func (m *RateLimitMiddleware) allow(clientIP string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Get or create limiter for this IP
	limiter, exists := m.limiters[clientIP]
	if !exists {
		// Create new limiter: maxRequests per interval seconds
		// Convert to requests per second
		rps := float64(m.maxRequests) / float64(m.interval)
		limiter = rate.NewLimiter(rate.Limit(rps), m.maxRequests)
		m.limiters[clientIP] = limiter
	}

	return limiter.Allow()
}

// getClientIP extracts the client IP from the request
func (m *RateLimitMiddleware) getClientIP(req *http.Request) string {
	// Try X-Forwarded-For header first
	if xff := req.Header.Get("X-Forwarded-For"); xff != "" {
		ips := strings.Split(xff, ",")
		if len(ips) > 0 {
			return strings.TrimSpace(ips[0])
		}
	}

	// Try X-Real-IP header
	if xri := req.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}

	// Fall back to RemoteAddr
	ip := req.RemoteAddr
	// Remove port if present
	if idx := strings.LastIndex(ip, ":"); idx != -1 {
		ip = ip[:idx]
	}
	return ip
}

// tooManyRequests returns a 429 Too Many Requests response
func (m *RateLimitMiddleware) tooManyRequests() *http.Response {
	return &http.Response{
		StatusCode: http.StatusTooManyRequests,
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
	}
}

// CleanupLimiters removes limiters for IPs that haven't been seen recently
// Should be called periodically to prevent memory leaks
func (m *RateLimitMiddleware) CleanupLimiters() {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Simple cleanup: clear all limiters
	// In production, you might want to track last access time and only remove stale entries
	if len(m.limiters) > 10000 {
		m.limiters = make(map[string]*rate.Limiter)
	}
}
