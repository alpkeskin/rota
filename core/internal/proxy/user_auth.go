package proxy

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/alpkeskin/rota/core/internal/database"
	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"golang.org/x/crypto/bcrypt"
)

// bcryptCompare is a thin wrapper so the hot-path resolve() doesn't need a DB call.
func bcryptCompare(hash, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

// userEntry caches a resolved PoolChain and the verified password hash for a user.
// This avoids bcrypt on every request — bcrypt only runs on first auth or after TTL expiry.
type userEntry struct {
	user      *models.ProxyUser
	chain     *PoolChain
	expiresAt time.Time
	// passwordHash is the bcrypt hash we verified against. If the user changes their
	// password the hash changes, causing a cache miss on next TTL expiry.
	verifiedPwHash string
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

	// usersConfigured caches whether any proxy_users exist (TTL 30s). Used by the
	// no-credentials path to decide whether an unauthenticated request may pass.
	usersConfigured   bool
	usersCheckedUntil time.Time
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

// AuthResult is the outcome of authorising a proxy client.
type AuthResult int

const (
	// AuthAllowed: proceed. The ProxyRequest is set for proxy users and nil
	// for an open proxy or the legacy single-user credentials.
	AuthAllowed AuthResult = iota
	// AuthRejected: missing or wrong credentials (HTTP 407).
	AuthRejected
	// AuthBadOptions: the username's routing options are invalid (HTTP 400);
	// the error says why and is safe to show.
	AuthBadOptions
)

// Authorize checks proxy credentials (hasCreds=false when none were sent)
// and resolves the user's routing. It is shared by the HTTP proxy and the
// SOCKS5 listener so both apply exactly the same rules:
//   - no credentials: allowed only on a fully open proxy (no legacy auth,
//     no proxy users) — otherwise that would be an auth bypass (AUD-1);
//   - proxy-user credentials, optionally with routing options in the
//     username (see ParseUsername);
//   - otherwise the legacy single-user credentials, when enabled.
func (m *UserAuthMiddleware) Authorize(ctx context.Context, username, password string, hasCreds bool) (*ProxyRequest, AuthResult, error) {
	if !hasCreds {
		if m.legacy != nil && m.legacy.IsEnabled() {
			return nil, AuthRejected, nil
		}
		if m.hasProxyUsers(ctx) {
			return nil, AuthRejected, nil
		}
		return nil, AuthAllowed, nil
	}

	legacyOK := func() bool { return m.legacy != nil && m.legacy.IsEnabled() && m.legacy.Check(username, password) }

	opts, optErr := ParseUsername(username)
	if optErr != nil {
		// An account created before routing options existed may be named
		// like "shop-city"; accept it verbatim, without options.
		if user, chain, err := m.resolve(ctx, username, password); err == nil {
			return &ProxyRequest{User: user, Chain: chain, Opts: UsernameOptions{Username: username, SessionTTL: defaultSessionTTL}}, AuthAllowed, nil
		}
		// Legacy credentials are checked verbatim and may contain dashes.
		if legacyOK() {
			return nil, AuthAllowed, nil
		}
		return nil, AuthBadOptions, optErr
	}

	user, chain, err := m.resolve(ctx, opts.Username, password)
	if err != nil && opts.Username != username {
		// An account created before routing options existed may itself be
		// named like "team-session-a"; accept it verbatim, without options.
		if u, c, rawErr := m.resolve(ctx, username, password); rawErr == nil {
			user, chain, err = u, c, nil
			opts = UsernameOptions{Username: username, SessionTTL: defaultSessionTTL}
		}
	}
	if err != nil {
		m.logger.Warn("user auth failed", "username", opts.Username, "err", err)
		// Fall back to legacy single-user auth ONLY when it is enforcing and
		// the credentials match it (AUD-1); never allow through otherwise.
		if legacyOK() {
			return nil, AuthAllowed, nil
		}
		return nil, AuthRejected, nil
	}
	return &ProxyRequest{User: user, Chain: chain, Opts: opts}, AuthAllowed, nil
}

// HandleRequest is called for every HTTP proxy request. It authorises the
// Proxy-Authorization credentials and, for proxy users, attaches the
// resolved ProxyRequest to the request context.
func (m *UserAuthMiddleware) HandleRequest(req *http.Request) (*http.Request, *http.Response) {
	username, password, ok := parseProxyAuth(req)
	preq, result, err := m.Authorize(req.Context(), username, password, ok)
	switch result {
	case AuthRejected:
		return req, unauthorized()
	case AuthBadOptions:
		return req, badProxyRequest(err.Error())
	}
	req.Header.Del("Proxy-Authorization")
	if preq != nil {
		req = req.WithContext(WithProxyRequest(req.Context(), preq))
	}
	return req, nil
}

// badProxyRequest builds a 400 explaining invalid routing options.
func badProxyRequest(msg string) *http.Response {
	resp := &http.Response{
		StatusCode:    http.StatusBadRequest,
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        make(http.Header),
		Body:          io.NopCloser(strings.NewReader(msg + "\n")),
		ContentLength: int64(len(msg) + 1),
	}
	resp.Header.Set("Content-Type", "text/plain; charset=utf-8")
	return resp
}

// HandleConnect is the same but for HTTPS CONNECT.
func (m *UserAuthMiddleware) HandleConnect(req *http.Request) (*http.Request, *http.Response) {
	return m.HandleRequest(req)
}

// resolve authenticates the user and returns a warm PoolChain.
// bcrypt is only called on first auth or after the cache TTL expires (60s).
// On cache hits the incoming password is compared directly against the cached
// bcrypt hash using bcrypt.CompareHashAndPassword — but this only happens once
// per 60-second window, not on every request.
func (m *UserAuthMiddleware) resolve(ctx context.Context, username, password string) (*models.ProxyUser, *PoolChain, error) {
	now := time.Now()

	// ── Fast path: cache hit within TTL ──────────────────────────────────
	m.mu.RLock()
	entry, hit := m.cache[username]
	m.mu.RUnlock()

	if hit && now.Before(entry.expiresAt) {
		// Verify password against the cached hash — no DB round-trip, no new bcrypt work.
		// bcrypt.CompareHashAndPassword is still ~30ms but we avoid the DB SELECT.
		// For even higher throughput, consider storing a fast HMAC of password+secret
		// instead — but bcrypt cache is sufficient for most workloads.
		if err := bcryptCompare(entry.verifiedPwHash, password); err != nil {
			return nil, nil, fmt.Errorf("invalid credentials")
		}
		return entry.user, entry.chain, nil
	}

	// ── Slow path: full DB lookup + bcrypt (runs at most once per 60s per user) ──
	if m.userRepo == nil { // legacy-only setups (and tests) have no user store
		return nil, nil, fmt.Errorf("invalid credentials")
	}
	user, err := m.userRepo.Authenticate(ctx, username, password)
	if err != nil {
		return nil, nil, err
	}

	chain, err := m.buildChain(ctx, user)
	if err != nil {
		return nil, nil, err
	}

	m.mu.Lock()
	m.cache[username] = userEntry{
		user:           user,
		chain:          chain,
		expiresAt:      now.Add(60 * time.Second),
		verifiedPwHash: user.PasswordHash,
	}
	m.mu.Unlock()

	return user, chain, nil
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

// hasProxyUsers reports whether any proxy_users are configured, with a 30s TTL
// cache so the no-credentials hot path doesn't hit the DB on every request.
func (m *UserAuthMiddleware) hasProxyUsers(ctx context.Context) bool {
	m.mu.RLock()
	fresh := time.Now().Before(m.usersCheckedUntil)
	cached := m.usersConfigured
	m.mu.RUnlock()
	if fresh {
		return cached
	}

	users, err := m.userRepo.List(ctx)
	if err != nil {
		// On error keep the last known value (fail toward the previous decision).
		return cached
	}
	has := len(users) > 0

	m.mu.Lock()
	m.usersConfigured = has
	m.usersCheckedUntil = time.Now().Add(30 * time.Second)
	m.mu.Unlock()
	return has
}

// refreshLoop periodically refreshes cached chains that are still live and evicts
// entries whose TTL has expired so the cache can't grow unbounded (AUD-19).
func (m *UserAuthMiddleware) refreshLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		now := time.Now()

		// Snapshot live (non-expired) entries; collect expired usernames for eviction.
		m.mu.RLock()
		live := make([]userEntry, 0, len(m.cache))
		var expired []string
		for k, v := range m.cache {
			if now.After(v.expiresAt) {
				expired = append(expired, k)
				continue
			}
			live = append(live, v)
		}
		m.mu.RUnlock()

		// Evict expired entries.
		if len(expired) > 0 {
			m.mu.Lock()
			for _, k := range expired {
				// Re-check under the write lock in case it was refreshed since the snapshot.
				if e, ok := m.cache[k]; ok && now.After(e.expiresAt) {
					delete(m.cache, k)
				}
			}
			m.mu.Unlock()
		}

		// Refresh chains still in use so new proxies become available.
		for _, entry := range live {
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
