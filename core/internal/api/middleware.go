package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alpkeskin/rota/core/internal/metrics"
	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/golang-jwt/jwt/v5"
)

// LoggerMiddleware logs HTTP requests
func LoggerMiddleware(log *logger.Logger) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)

			duration := time.Since(start)

			log.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"duration", duration.String(),
				"bytes", ww.BytesWritten(),
				"remote_addr", r.RemoteAddr,
				"request_id", middleware.GetReqID(r.Context()),
			)
		})
	}
}

// MetricsMiddleware records per-route request counts and latency. It labels
// by chi route pattern (e.g. /api/v1/proxies/{id}), never the raw path, so
// label cardinality stays bounded; unmatched paths share one label.
func MetricsMiddleware() func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)

			route := "unmatched"
			if rctx := chi.RouteContext(r.Context()); rctx != nil {
				if p := rctx.RoutePattern(); p != "" {
					route = p
				}
			}
			status := ww.Status()
			if status == 0 {
				// Nothing was written through w: either the connection was
				// hijacked for a WebSocket upgrade, or the handler returned
				// without writing, which net/http answers with an empty 200.
				if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
					status = http.StatusSwitchingProtocols
				} else {
					status = http.StatusOK
				}
			}
			statusLabel := strconv.Itoa(status)
			metrics.APIRequests.WithLabelValues(route, r.Method, statusLabel).Inc()
			// Long-lived WebSocket streams would swamp the latency histogram.
			if !strings.HasPrefix(route, "/ws/") {
				metrics.APIRequestDuration.WithLabelValues(route, r.Method).Observe(time.Since(start).Seconds())
			}
		})
	}
}

// JWTMiddleware validates Bearer tokens on every request in the protected group.
// Accepts token from:
//   - Authorization: Bearer <token> header (standard API calls)
//   - ?token=<token> query param (WebSocket connections)
//
// Returns 401 if missing, invalid, or expired.
func JWTMiddleware(secret string) func(next http.Handler) http.Handler {
	key := []byte(secret)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenStr := extractToken(r)
			if tokenStr == "" {
				w.Header().Set("Content-Type", "application/json")
				http.Error(w, `{"error":"authorization required"}`, http.StatusUnauthorized)
				return
			}

			token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
				if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, jwt.ErrSignatureInvalid
				}
				return key, nil
			})
			if err != nil || !token.Valid {
				w.Header().Set("Content-Type", "application/json")
				http.Error(w, `{"error":"invalid or expired token"}`, http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// extractToken pulls the JWT from the Authorization header or ?token query param.
func extractToken(r *http.Request) string {
	// 1. Authorization: Bearer <token>
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	// 2. ?token=<token>  (used by WebSocket clients)
	if t := r.URL.Query().Get("token"); t != "" {
		return t
	}
	return ""
}

// OptionsMiddleware handles all OPTIONS requests with 200 OK
// This ensures CORS preflight requests are properly handled
func OptionsMiddleware() func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "OPTIONS" {
				// Set basic CORS headers for preflight
				w.Header().Set("Access-Control-Allow-Origin", "*")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type, X-CSRF-Token")
				w.Header().Set("Access-Control-Max-Age", "300")
				w.WriteHeader(http.StatusOK)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
