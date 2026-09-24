package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sort"
	"strings"
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
		Type:      auth.PrincipalSession,
		AccountID: acct.ID,
		Username:  acct.Username,
		Role:      auth.Role(acct.Role),
	}, nil
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
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			// Record in a defer so a handler that panics (recovered further
			// out as a 500) is still audited; the panic is then re-raised.
			defer func() {
				p := recover()
				recordAudit(r, ww.Status(), p != nil, rec, log)
				if p != nil {
					panic(p)
				}
			}()
			next.ServeHTTP(ww, r)
		})
	}
}

func recordAudit(r *http.Request, status int, panicked bool, rec auditRecorder, log *logger.Logger) {
	rctx := chi.RouteContext(r.Context())
	route := ""
	if rctx != nil {
		route = rctx.RoutePattern()
	}
	mutating := r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions
	if !mutating && !auditedReads[route] {
		return
	}
	if route == "" {
		route = "unmatched"
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
	if p := auth.FromContext(r.Context()); p != nil {
		entry.ActorType = string(p.Type)
		id := p.AccountID
		entry.ActorID = &id
		entry.ActorName = p.ActorName()
	} else {
		entry.ActorType = "anonymous"
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
// headers, chi's RealIP middleware has already rewritten RemoteAddr.
func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr // RealIP stores a bare IP without a port
}
