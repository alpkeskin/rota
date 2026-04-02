package proxy

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alpkeskin/rota/core/internal/database"
	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/elazarl/goproxy"
)

// userChainKey is the context key that carries the resolved *PoolChain.
type userChainKey struct{}

// UserChainContextKey is exported for use in the handler.
var UserChainContextKey = userChainKey{}

// userEntry caches a resolved PoolChain for a given user to avoid DB round-trips on every request.
type userEntry struct {
	chain     *PoolChain
	expiresAt time.Time
}

// UserAuthMiddleware resolves Proxy-Authorization credentials against proxy_users.
// When a matching enabled user is found it attaches a *PoolChain to the request context.
// If user-based auth is not configured (no proxy_users) and the legacy single-user
// auth is enabled, it falls through to the original AuthMiddleware behaviour.
type UserAuthMiddleware struct {
	userRepo    *repository.UserRepository
	poolRepo    *repository.PoolRepository
	db          *database.DB
	logger      *logger.Logger
	rotSettings *models.RotationSettings

	// legacy fallback (original single-user auth)
	legacy *AuthMiddleware

	// cache: username -> userEntry (TTL 60s)
	mu    sync.RWMutex
	cache map[string]userEntry
}

// NewUserAuthMiddleware creates the middleware.
func NewUserAuthMiddleware(
	userRepo *repository.UserRepository,
	poolRepo *repository.PoolRepository,
	db *database.DB,
	legacy *AuthMiddleware,
	rotSettings *models.RotationSettings,
	log *logger.Logger,
) *UserAuthMiddleware {
	m := &UserAuthMiddleware{
		userRepo:    userRepo,
		poolRepo:    poolRepo,
		db:          db,
		legacy:      legacy,
		rotSettings: rotSettings,
		logger:      log,
		cache:       make(map[string]userEntry),
	}
	// background goroutine: refresh all cached chains every 30s
	go m.refreshLoop()
	return m
}

// HandleRequest is called for every HTTP proxy request.
// It reads Proxy-Authorization, looks up the user, builds a PoolChain and stores
// it in the request context so the handler can use it.
func (m *UserAuthMiddleware) HandleRequest(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
	username, password, ok := parseProxyAuth(req)
	if !ok {
		// No credentials provided — check legacy auth
		return m.legacy.HandleRequest(req, ctx)
	}

	chain, err := m.resolve(req.Context(), username, password)
	if err != nil {
		m.logger.Warn("user auth failed", "username", username, "err", err)
		// If legacy auth is enabled and credentials don't match a proxy_user,
		// still try the legacy path (single admin user)
		if m.legacy != nil {
			if _, resp := m.legacy.HandleRequest(req, ctx); resp != nil {
				return req, resp
			}
			// legacy auth passed with these same credentials → allow without pool chain
			return req, nil
		}
		return req, unauthorized()
	}

	// Attach chain to context and strip the Proxy-Authorization header
	newCtx := context.WithValue(req.Context(), UserChainContextKey, chain)
	req = req.WithContext(newCtx)
	req.Header.Del("Proxy-Authorization")
	return req, nil
}

// HandleConnect is the same but for HTTPS CONNECT.
func (m *UserAuthMiddleware) HandleConnect(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
	return m.HandleRequest(req, ctx)
}

// resolve authenticates the user and returns a warm PoolChain.
func (m *UserAuthMiddleware) resolve(ctx context.Context, username, password string) (*PoolChain, error) {
	// Check cache
	m.mu.RLock()
	entry, hit := m.cache[username]
	m.mu.RUnlock()

	if hit && time.Now().Before(entry.expiresAt) {
		// Still need to verify password on each request (bcrypt is cached inside the user record check)
		if _, err := m.userRepo.Authenticate(ctx, username, password); err != nil {
			return nil, err
		}
		return entry.chain, nil
	}

	// Full lookup
	user, err := m.userRepo.Authenticate(ctx, username, password)
	if err != nil {
		return nil, err
	}

	chain, err := m.buildChain(ctx, user)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	m.cache[username] = userEntry{chain: chain, expiresAt: time.Now().Add(60 * time.Second)}
	m.mu.Unlock()

	return chain, nil
}

// buildChain constructs an ordered PoolChain for a user: [mainPool, ...fallbackPools].
func (m *UserAuthMiddleware) buildChain(ctx context.Context, user *models.ProxyUser) (*PoolChain, error) {
	var pools []models.ProxyPool

	// Main pool
	if user.MainPoolID != nil {
		p, err := m.poolRepo.GetByID(ctx, *user.MainPoolID)
		if err != nil {
			return nil, err
		}
		if p != nil {
			pools = append(pools, *p)
		}
	}

	// Fallback pools in order
	for _, fbID := range user.FallbackPoolIDs {
		p, err := m.poolRepo.GetByID(ctx, fbID)
		if err != nil || p == nil {
			continue
		}
		pools = append(pools, *p)
	}

	maxRetry := user.MaxRetries
	if maxRetry <= 0 {
		maxRetry = 5
	}

	chain := NewPoolChain(m.db, pools, maxRetry, m.logger)
	chain.Refresh(ctx)
	return chain, nil
}

// refreshLoop periodically refreshes all cached chains so new proxies become available.
func (m *UserAuthMiddleware) refreshLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		m.mu.RLock()
		entries := make(map[string]userEntry, len(m.cache))
		for k, v := range m.cache {
			entries[k] = v
		}
		m.mu.RUnlock()

		for _, entry := range entries {
			entry.chain.Refresh(ctx)
		}
		cancel()
	}
}

// InvalidateUser removes a user's cached chain (call after user is updated/deleted).
func (m *UserAuthMiddleware) InvalidateUser(username string) {
	m.mu.Lock()
	delete(m.cache, username)
	m.mu.Unlock()
}

// parseProxyAuth extracts username+password from the Proxy-Authorization header.
func parseProxyAuth(req *http.Request) (string, string, bool) {
	auth := req.Header.Get("Proxy-Authorization")
	if auth == "" {
		return "", "", false
	}
	if !strings.HasPrefix(auth, "Basic ") {
		return "", "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(auth, "Basic "))
	if err != nil {
		return "", "", false
	}
	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// unauthorized builds a 407 response (standalone, no receiver needed).
func unauthorized() *http.Response {
	resp := &http.Response{
		StatusCode: http.StatusProxyAuthRequired,
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
	}
	resp.Header.Set("Proxy-Authenticate", `Basic realm="Rota Proxy"`)
	return resp
}
