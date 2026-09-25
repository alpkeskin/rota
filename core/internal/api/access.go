package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/alpkeskin/rota/core/internal/auth"
	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write([]byte(`{"error":"` + msg + `"}`)) //nolint:errcheck // best-effort error body
}

// authenticator resolves the caller of a protected route from a session
// token (JWT) or an API key, and stores the principal in the request context.
type authenticator struct {
	secret   []byte
	accounts *repository.AccountRepository
	keys     *repository.APIKeyRepository
	logger   *logger.Logger
}

// Middleware authenticates every request. Accepted credentials:
//   - Authorization: Bearer <session token | API key>
//   - ?token=<session token | API key> (WebSocket clients can't set headers)
//
// Sessions are checked against the account on every request (enabled, token
// version, current role), so revocations and role changes apply immediately.
func (a *authenticator) Middleware() func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenStr := extractToken(r)
			if tokenStr == "" {
				writeJSONError(w, http.StatusUnauthorized, "authorization required")
				return
			}
			p, err := a.resolve(r.Context(), tokenStr)
			if h := principalHolderFrom(r.Context()); h != nil {
				h.p, h.presented, h.credential = p, true, credentialHint(tokenStr)
			}
			if err != nil {
				if errors.Is(err, repository.ErrInvalidCredentials) || errors.Is(err, auth.ErrInvalidToken) {
					writeJSONError(w, http.StatusUnauthorized, "invalid or expired token")
					return
				}
				a.logger.Error("authentication lookup failed", "error", err)
				writeJSONError(w, http.StatusServiceUnavailable, "authentication temporarily unavailable")
				return
			}
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), p)))
		})
	}
}

func (a *authenticator) resolve(ctx context.Context, tokenStr string) (*auth.Principal, error) {
	if strings.HasPrefix(tokenStr, repository.APIKeyPrefix) {
		return a.keys.Authenticate(ctx, tokenStr)
	}
	id, version, err := auth.ParseSession(a.secret, tokenStr)
	if err != nil {
		return nil, err
	}
	acct, err := a.accounts.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if acct == nil || !acct.Enabled || acct.TokenVersion != version {
		return nil, auth.ErrInvalidToken
	}
	return &auth.Principal{
		Type:         auth.PrincipalSession,
		AccountID:    acct.ID,
		Username:     acct.Username,
		Role:         auth.Role(acct.Role),
		TokenVersion: acct.TokenVersion,
	}, nil
}

// LiveMiddleware is Middleware for long-lived connections (WebSockets): it
// re-checks the credential every interval and cancels the request context —
// which ends the stream — once the session or key is revoked, expired or
// demoted below min. Transient lookup failures don't end the stream.
func (a *authenticator) LiveMiddleware(min auth.Role, interval time.Duration) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return a.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenStr := extractToken(r)
			ctx, cancel := context.WithCancel(r.Context())
			defer cancel()
			go func() {
				t := time.NewTicker(interval)
				defer t.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-t.C:
						p, err := a.resolve(ctx, tokenStr)
						if err != nil && !errors.Is(err, repository.ErrInvalidCredentials) && !errors.Is(err, auth.ErrInvalidToken) {
							continue // infrastructure hiccup: keep the stream
						}
						if err != nil || !p.Role.AtLeast(min) {
							cancel()
							return
						}
					}
				}
			}()
			next.ServeHTTP(w, r.WithContext(ctx))
		}))
	}
}

// credentialHint identifies an API key in the audit log without storing it:
// its public prefix (the same one the dashboard shows). Session tokens get no
// hint — they carry no stable public identifier.
func credentialHint(tokenStr string) string {
	if strings.HasPrefix(tokenStr, repository.APIKeyPrefix) && len(tokenStr) >= len(repository.APIKeyPrefix)+6 {
		return tokenStr[:len(repository.APIKeyPrefix)+6]
	}
	return ""
}

// principalHolder lets AuditMiddleware, which runs outside the
// authenticator (so it also sees requests rejected with 401), learn who the
// authenticator resolved.
type principalHolder struct {
	p          *auth.Principal
	presented  bool   // a credential was sent (valid or not)
	credential string // public hint for API keys
}

type principalHolderKey struct{}

func principalHolderFrom(ctx context.Context) *principalHolder {
	h, _ := ctx.Value(principalHolderKey{}).(*principalHolder)
	return h
}

// RequireRole rejects callers whose effective role is below min with 403.
func RequireRole(min auth.Role) func(next http.Handler) http.Handler {
	msg := "this action requires the " + string(min) + " role"
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := auth.FromContext(r.Context())
			if p == nil || !p.Role.AtLeast(min) {
				writeJSONError(w, http.StatusForbidden, msg)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireSession limits a route to dashboard sessions. Credential management
// (passwords, accounts, API keys) is not available to API keys, so a leaked
// key can't mint new credentials or lock out the humans who could revoke it.
func RequireSession() func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := auth.FromContext(r.Context())
			if p == nil || p.Type != auth.PrincipalSession {
				writeJSONError(w, http.StatusForbidden, "this action requires a dashboard session; API keys can't manage credentials")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// auditRecorder is the subset of AuditRepository the middleware needs.
type auditRecorder interface {
	Record(ctx context.Context, e models.AuditEntry) error
}

// auditedReads are GET routes worth auditing because they hand out data an
// attacker would want (full inventories), even though they change nothing.
var auditedReads = map[string]bool{
	"/api/v1/proxies/export":    true,
	"/api/v1/pools/{id}/export": true,
	"/api/v1/logs/export":       true,
	"/api/v1/audit-log":         true,
}

// AuditMiddleware records every state-changing request (and the reads in
// auditedReads) with its caller, route, path parameters and outcome —
// including requests refused by RequireRole. Request bodies are never
// stored: they can carry passwords and keys.
func AuditMiddleware(rec auditRecorder, log *logger.Logger) func(next http.Handler) http.Handler {
	rejected := newIPRateLimiter(rejectedAuditPerMinute)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			holder := &principalHolder{}
			r = r.WithContext(context.WithValue(r.Context(), principalHolderKey{}, holder))
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			// Record in a defer so a handler that panics (recovered further
			// out as a 500) is still audited; the panic is then re-raised.
			defer func() {
				p := recover()
				// Requests rejected before authentication are audited only
				// when they presented a credential (a revoked or leaked one
				// in use) and at a bounded rate per IP, so anonymous floods
				// can't grow the audit log without limit.
				if holder.p != nil || (holder.presented && rejected.Allow(clientIP(r))) {
					recordAudit(r, holder, ww.Status(), p != nil, rec, log)
				}
				if p != nil {
					panic(p)
				}
			}()
			next.ServeHTTP(ww, r)
		})
	}
}

func recordAudit(r *http.Request, holder *principalHolder, status int, panicked bool, rec auditRecorder, log *logger.Logger) {
	rctx := chi.RouteContext(r.Context())
	route := ""
	if rctx != nil {
		route = rctx.RoutePattern()
	}
	mutating := r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions
	if !mutating && !auditedReads[route] {
		return
	}
	if route == "" || strings.HasSuffix(route, "/*") {
		// Rejected before the subrouter matched a route: record the path
		// the caller tried, bounded in length.
		route = r.URL.Path
		if len(route) > 200 {
			route = route[:200] + "…"
		}
	}
	switch {
	case panicked:
		status = http.StatusInternalServerError
	case status == 0:
		status = http.StatusOK
	}
	entry := models.AuditEntry{
		Action:   r.Method + " " + route,
		Resource: routeParams(rctx),
		Status:   status,
		IP:       clientIP(r),
	}
	if p := holder.p; p != nil {
		entry.ActorType = string(p.Type)
		id := p.AccountID
		entry.ActorID = &id
		entry.ActorName = p.ActorName()
		if p.Type == auth.PrincipalAPIKey {
			// Key names aren't unique; the id pins down which key acted.
			entry.Details = map[string]any{"api_key_id": p.APIKeyID}
		}
	} else {
		// Rejected by the authenticator (missing, invalid, revoked or
		// expired credential). A key's public prefix shows which one.
		entry.ActorType = "anonymous"
		if holder.credential != "" {
			entry.Details = map[string]any{"credential": holder.credential + "…"}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rec.Record(ctx, entry); err != nil {
		log.Error("failed to write audit entry", "action", entry.Action, "error", err)
	}
}

// routeParams renders chi URL parameters as "id=5 rule_id=2", sorted by key.
func routeParams(rctx *chi.Context) string {
	if rctx == nil {
		return ""
	}
	var parts []string
	for i, k := range rctx.URLParams.Keys {
		if k == "*" || i >= len(rctx.URLParams.Values) {
			continue
		}
		parts = append(parts, k+"="+rctx.URLParams.Values[i])
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
}

// clientIP is the peer address without its port. When the API trusts proxy
// headers, trustedRealIP has already rewritten RemoteAddr.
func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr // trustedRealIP stores a bare IP without a port
}

// rejectedAuditPerMinute bounds audit entries for rejected credentials per IP.
const rejectedAuditPerMinute = 30

// ipRateLimiter is a small per-IP token bucket with periodic eviction.
type ipRateLimiter struct {
	mu      sync.Mutex
	perMin  int
	buckets map[string]*ipBucket
	sweep   time.Time
}

type ipBucket struct {
	tokens float64
	last   time.Time
}

func newIPRateLimiter(perMinute int) *ipRateLimiter {
	return &ipRateLimiter{perMin: perMinute, buckets: map[string]*ipBucket{}, sweep: time.Now()}
}

// Allow reports whether ip may emit one more event now.
func (l *ipRateLimiter) Allow(ip string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.sweep) > 10*time.Minute {
		for k, b := range l.buckets {
			if now.Sub(b.last) > 10*time.Minute {
				delete(l.buckets, k)
			}
		}
		l.sweep = now
	}
	b, ok := l.buckets[ip]
	if !ok {
		b = &ipBucket{tokens: float64(l.perMin), last: now}
		l.buckets[ip] = b
	}
	b.tokens += now.Sub(b.last).Minutes() * float64(l.perMin)
	if b.tokens > float64(l.perMin) {
		b.tokens = float64(l.perMin)
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
