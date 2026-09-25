package proxy

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alpkeskin/rota/core/internal/metrics"
	"github.com/alpkeskin/rota/core/internal/models"
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

// IsEnabled reports whether legacy single-user auth is currently enforcing.
func (m *AuthMiddleware) IsEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.enabled
}

// UpdateSettings updates the authentication settings
func (m *AuthMiddleware) UpdateSettings(settings models.AuthenticationSettings) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.enabled = settings.Enabled
	m.username = settings.Username
	m.password = settings.Password
}

// Check reports whether username/password match the legacy credentials,
// in constant time. It doesn't consider whether legacy auth is enabled.
func (m *AuthMiddleware) Check(username, password string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	userMatch := subtle.ConstantTimeCompare([]byte(username), []byte(m.username))
	passMatch := subtle.ConstantTimeCompare([]byte(password), []byte(m.password))
	return userMatch&passMatch == 1
}

// HandleRequest validates proxy authentication for HTTP requests
func (m *AuthMiddleware) HandleRequest(req *http.Request) (*http.Request, *http.Response) {
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

	// Validate credentials using constant-time comparison to avoid leaking
	// which field mismatched via timing. Both comparisons always run (bitwise
	// AND, not short-circuit) so timing doesn't reveal username vs password.
	userMatch := subtle.ConstantTimeCompare([]byte(username), []byte(m.username))
	passMatch := subtle.ConstantTimeCompare([]byte(password), []byte(m.password))
	if userMatch&passMatch != 1 {
		return req, m.unauthorized()
	}

	// Authentication successful, remove the header before forwarding
	req.Header.Del("Proxy-Authorization")
	return req, nil
}

// HandleConnect validates proxy authentication for HTTPS CONNECT requests
func (m *AuthMiddleware) HandleConnect(req *http.Request) (*http.Request, *http.Response) {
	return m.HandleRequest(req)
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
	shared      SharedRate // nil = per-instance limits
}

// SharedRate is a rate limiter shared by all instances (sharedstate.Store).
type SharedRate interface {
	AllowRate(ctx context.Context, key string, limit int, period time.Duration) (bool, time.Duration, error)
}

// SetShared makes the per-IP limit cluster-wide.
func (m *RateLimitMiddleware) SetShared(sh SharedRate) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.shared = sh
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
func (m *RateLimitMiddleware) HandleRequest(req *http.Request) (*http.Request, *http.Response) {
	m.mu.RLock()
	enabled := m.enabled
	m.mu.RUnlock()
	if !enabled {
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

// Allow reports whether a request from clientIP is within the limit (always
// true when rate limiting is disabled). Used by the SOCKS5 listener.
func (m *RateLimitMiddleware) Allow(clientIP string) bool {
	m.mu.RLock()
	enabled := m.enabled
	m.mu.RUnlock()
	return !enabled || m.allow(clientIP)
}

// HandleConnect validates rate limits for HTTPS CONNECT requests
func (m *RateLimitMiddleware) HandleConnect(req *http.Request) (*http.Request, *http.Response) {
	return m.HandleRequest(req)
}

// allow checks if the request is allowed based on rate limiting
func (m *RateLimitMiddleware) allow(clientIP string) bool {
	m.mu.Lock()
	interval, maxRequests, shared := m.interval, m.maxRequests, m.shared
	m.mu.Unlock()

	// Guard against misconfiguration: a non-positive interval would make rps
	// +Inf (rate.NewLimiter panics / never limits) and a non-positive
	// maxRequests would set burst to 0 (denies every request). Treat either
	// case as "limiter effectively disabled" and allow the request through.
	if interval <= 0 || maxRequests <= 0 {
		return true
	}

	// One budget per IP across all instances; per instance while the shared
	// store is unreachable.
	if shared != nil {
		ok, _, err := shared.AllowRate(context.Background(), "ip{"+clientIP+"}", maxRequests, time.Duration(interval)*time.Second)
		if err == nil {
			return ok
		}
		metrics.SharedStateErrors.WithLabelValues("rate").Inc()
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.interval <= 0 || m.maxRequests <= 0 {
		return true
	}

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

// getClientIP extracts the client IP used as the per-IP rate-limit key.
//
// SECURITY (AUD-20): X-Forwarded-For / X-Real-IP are supplied by the caller and
// are trivially spoofable on a forward proxy — trusting them lets a client
// bypass the limit by rotating the header value. We therefore key on the real
// socket peer (RemoteAddr) only.
//
// TODO: there is currently no trusted-proxy setting. If one is added, honor the
// forwarded headers only when the immediate peer (RemoteAddr) is in the trusted
// set; until then, always use RemoteAddr.
func (m *RateLimitMiddleware) getClientIP(req *http.Request) string {
	// Use net.SplitHostPort so bare IPv6 addresses (which contain colons) are
	// handled correctly (AUD-36). Fall back to the raw value when there is no
	// port or the address is malformed.
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return req.RemoteAddr
	}
	return host
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
