package api

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alpkeskin/rota/core/internal/metrics"
	"github.com/alpkeskin/rota/core/pkg/logger"
)

// authRateLimiter protects the login endpoint against brute-force attacks.
//
// Two independent mechanisms run simultaneously:
//
//  1. Per-IP limiter — tracks failed attempts per IP address within a sliding
//     window (AuthIPWindowMinutes). Once an IP exceeds AuthIPMaxAttempts failures
//     it is blocked for AuthIPBlockMinutes regardless of success/failure.
//
//  2. Global limiter — counts ALL login attempts (not just failures) across all
//     IPs within the last 60 seconds. If the count exceeds AuthGlobalMaxPerMinute
//     the endpoint is locked for AuthGlobalLockoutMin minutes for everyone.
//     A globalMax <= 0 disables it (used for the working-proxies export, where
//     legitimate polling volume is unbounded and a global lockout would let
//     one anonymous client deny the endpoint to every user).
//
// Only 401 responses count as failures: a 403 means the caller authenticated
// but lacks permission, which is not a guessing attempt.
//
// Both counters live in memory and are safe for concurrent access.
// They are intentionally NOT persisted — a restart clears them, which is fine
// since the goal is to blunt online attacks, not forensic accounting.
type authRateLimiter struct {
	mu  sync.Mutex
	log *logger.Logger

	// name keys this limiter's state in the shared store ("login", "export").
	name string
	// shared, when set, keeps the counters and blocks in Redis so every
	// instance enforces them; the in-memory state is used while it fails.
	shared LoginStore

	// trustProxyHeaders: honour X-Forwarded-For / X-Real-IP only when true, i.e.
	// when behind a trusted reverse proxy. When false (direct exposure) these are
	// ignored so they can't be spoofed to bypass per-IP throttling (AUD-20).
	trustProxyHeaders bool

	// per-IP state
	ipAttempts map[string][]time.Time // timestamps of failed attempts per IP
	ipBlocked  map[string]time.Time   // unblock time per IP

	// global state
	globalAttempts  []time.Time // timestamps of ALL attempts (last 60 s)
	globalLockUntil time.Time   // when the global lockout expires

	// config (immutable after construction)
	ipMaxAttempts   int
	ipWindow        time.Duration
	ipBlockDuration time.Duration
	globalMax       int
	globalLockout   time.Duration
}

// LoginStore holds login throttling state shared by all instances
// (sharedstate.Store).
type LoginStore interface {
	Hit(ctx context.Context, key string, window time.Duration) (int, error)
	Block(ctx context.Context, key string, d time.Duration) error
	Blocked(ctx context.Context, key string) (time.Duration, error)
}

func newAuthRateLimiter(
	ipMaxAttempts, ipWindowMin, ipBlockMin int,
	globalMax, globalLockoutMin int,
	trustProxyHeaders bool,
	log *logger.Logger,
) *authRateLimiter {
	rl := &authRateLimiter{
		log:               log,
		trustProxyHeaders: trustProxyHeaders,
		ipAttempts:        make(map[string][]time.Time),
		ipBlocked:         make(map[string]time.Time),
		ipMaxAttempts:     ipMaxAttempts,
		ipWindow:          time.Duration(ipWindowMin) * time.Minute,
		ipBlockDuration:   time.Duration(ipBlockMin) * time.Minute,
		globalMax:         globalMax,
		globalLockout:     time.Duration(globalLockoutMin) * time.Minute,
	}
	// Background cleanup every 5 minutes
	go rl.cleanup()
	return rl
}

// Middleware returns an http.Handler middleware that enforces rate limits.
// It wraps the next handler and:
//   - returns 429 immediately if the global lockout or a per-IP block is active
//   - records every attempt for the global counter
//   - records failed attempts (non-200 response) for the per-IP counter
func (rl *authRateLimiter) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := rl.clientIP(r)
			now := time.Now()

			if rl.shared != nil && rl.serveShared(w, r, next, ip) {
				return
			}

			rl.mu.Lock()

			// ── 1. Global lockout check ──────────────────────────────────────
			if now.Before(rl.globalLockUntil) {
				remainingExact := rl.globalLockUntil.Sub(now)
				remaining := remainingExact.Truncate(time.Second)
				rl.mu.Unlock()
				rl.log.Warn("auth global lockout active",
					"ip", ip,
					"remaining", remaining.String(),
				)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", retryAfter(remainingExact))
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(`{"error":"Authentication temporarily disabled due to too many requests. Try again later."}`)) //nolint:errcheck // best-effort body on a 429
				return
			}

			// ── 2. Per-IP block check ────────────────────────────────────────
			if unblockAt, blocked := rl.ipBlocked[ip]; blocked && now.Before(unblockAt) {
				remainingExact := unblockAt.Sub(now)
				remaining := remainingExact.Truncate(time.Second)
				rl.mu.Unlock()
				rl.log.Warn("auth per-IP block active",
					"ip", ip,
					"remaining", remaining.String(),
				)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", retryAfter(remainingExact))
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(`{"error":"Too many failed authentication attempts from your IP. Try again later."}`)) //nolint:errcheck // best-effort body on a 429
				return
			}

			// ── 3. Record attempt for global counter ─────────────────────────
			if rl.globalMax > 0 {
				cutoff1m := now.Add(-time.Minute)
				rl.globalAttempts = filterAfter(rl.globalAttempts, cutoff1m)
				rl.globalAttempts = append(rl.globalAttempts, now)
			}

			if rl.globalMax > 0 && len(rl.globalAttempts) > rl.globalMax {
				rl.globalLockUntil = now.Add(rl.globalLockout)
				rl.log.Warn("auth global rate limit exceeded — engaging lockout",
					"attempts_per_min", len(rl.globalAttempts),
					"limit", rl.globalMax,
					"lockout", rl.globalLockout.String(),
				)
				rl.mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", retryAfter(rl.globalLockout))
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(`{"error":"Authentication temporarily disabled due to too many requests. Try again later."}`)) //nolint:errcheck // best-effort body on a 429
				return
			}

			rl.mu.Unlock()

			// ── 4. Execute the actual login handler ──────────────────────────
			ww := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(ww, r)

			// ── 5. On failure, record per-IP attempt ─────────────────────────
			if ww.status == http.StatusUnauthorized {
				rl.mu.Lock()
				cutoffWindow := now.Add(-rl.ipWindow)
				prev := filterAfter(rl.ipAttempts[ip], cutoffWindow)
				prev = append(prev, now)
				rl.ipAttempts[ip] = prev

				if len(prev) >= rl.ipMaxAttempts {
					rl.ipBlocked[ip] = now.Add(rl.ipBlockDuration)
					rl.log.Warn("auth per-IP rate limit exceeded — IP blocked",
						"ip", ip,
						"attempts", len(prev),
						"limit", rl.ipMaxAttempts,
						"block_until", rl.ipBlocked[ip].Format(time.RFC3339),
					)
				}
				rl.mu.Unlock()
			}
		})
	}
}

const (
	msgGlobalLockout = `{"error":"Authentication temporarily disabled due to too many requests. Try again later."}`
	msgIPBlocked     = `{"error":"Too many failed authentication attempts from your IP. Try again later."}`
)

func writeTooMany(w http.ResponseWriter, wait time.Duration, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", retryAfter(wait))
	w.WriteHeader(http.StatusTooManyRequests)
	w.Write([]byte(body)) //nolint:errcheck // best-effort body on a 429
}

// serveShared applies the limits with state kept in the shared store. It
// returns false, having written nothing, when the store can't be read, and
// the caller then applies the in-memory limits instead.
func (rl *authRateLimiter) serveShared(w http.ResponseWriter, r *http.Request, next http.Handler, ip string) bool {
	ctx := r.Context()
	globalKey, ipKey := rl.name+":global", rl.name+":ip:"+ip
	var globalLeft time.Duration
	if rl.globalMax > 0 {
		var err error
		if globalLeft, err = rl.shared.Blocked(ctx, globalKey); err != nil {
			metrics.SharedStateErrors.WithLabelValues("login").Inc()
			return false
		}
	}
	ipLeft, err := rl.shared.Blocked(ctx, ipKey)
	if err != nil {
		metrics.SharedStateErrors.WithLabelValues("login").Inc()
		return false
	}
	if globalLeft > 0 {
		rl.log.Warn("auth global lockout active", "ip", ip, "remaining", globalLeft.Truncate(time.Second).String())
		writeTooMany(w, globalLeft, msgGlobalLockout)
		return true
	}
	if ipLeft > 0 {
		rl.log.Warn("auth per-IP block active", "ip", ip, "remaining", ipLeft.Truncate(time.Second).String())
		writeTooMany(w, ipLeft, msgIPBlocked)
		return true
	}

	// From here on a store failure no longer changes the outcome: the
	// attempt is served, and only its bookkeeping is lost.
	if rl.globalMax > 0 {
		n, err := rl.shared.Hit(ctx, globalKey, time.Minute)
		if err != nil {
			metrics.SharedStateErrors.WithLabelValues("login").Inc()
		} else if n > rl.globalMax {
			if err := rl.shared.Block(ctx, globalKey, rl.globalLockout); err != nil {
				metrics.SharedStateErrors.WithLabelValues("login").Inc()
			}
			rl.log.Warn("auth global rate limit exceeded — engaging lockout",
				"attempts_per_min", n, "limit", rl.globalMax, "lockout", rl.globalLockout.String())
			writeTooMany(w, rl.globalLockout, msgGlobalLockout)
			return true
		}
	}

	ww := &statusWriter{ResponseWriter: w, status: http.StatusOK}
	next.ServeHTTP(ww, r)

	if ww.status == http.StatusUnauthorized {
		n, err := rl.shared.Hit(ctx, rl.name+":fail:"+ip, rl.ipWindow)
		if err != nil {
			metrics.SharedStateErrors.WithLabelValues("login").Inc()
		} else if n >= rl.ipMaxAttempts {
			if err := rl.shared.Block(ctx, ipKey, rl.ipBlockDuration); err != nil {
				metrics.SharedStateErrors.WithLabelValues("login").Inc()
			}
			rl.log.Warn("auth per-IP rate limit exceeded — IP blocked",
				"ip", ip, "attempts", n, "limit", rl.ipMaxAttempts,
				"block_until", time.Now().Add(rl.ipBlockDuration).Format(time.RFC3339))
		}
	}
	return true
}

// cleanup removes stale entries every 5 minutes to prevent unbounded growth.
func (rl *authRateLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		rl.mu.Lock()
		// Remove expired IP blocks
		for ip, unblockAt := range rl.ipBlocked {
			if now.After(unblockAt) {
				delete(rl.ipBlocked, ip)
				delete(rl.ipAttempts, ip)
			}
		}
		// Trim old per-IP attempt slices
		for ip, times := range rl.ipAttempts {
			filtered := filterAfter(times, now.Add(-rl.ipWindow))
			if len(filtered) == 0 {
				delete(rl.ipAttempts, ip)
			} else {
				rl.ipAttempts[ip] = filtered
			}
		}
		// Trim global attempts older than 1 minute
		rl.globalAttempts = filterAfter(rl.globalAttempts, now.Add(-time.Minute))
		rl.mu.Unlock()
	}
}

// filterAfter returns only timestamps that are after cutoff.
func filterAfter(ts []time.Time, cutoff time.Time) []time.Time {
	i := 0
	for _, t := range ts {
		if t.After(cutoff) {
			ts[i] = t
			i++
		}
	}
	return ts[:i]
}

// clientIP extracts the client IP for rate-limiting. X-Forwarded-For / X-Real-IP
// are honoured ONLY when trustProxyHeaders is set (i.e. behind a trusted reverse
// proxy); otherwise they are ignored so a directly-exposed API can't be tricked
// into treating every spoofed header value as a fresh IP and bypassing the
// per-IP login block (AUD-20).
func (rl *authRateLimiter) clientIP(r *http.Request) string {
	if rl.trustProxyHeaders {
		if ip := forwardedIP(r); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// forwardedIP returns the client IP a trusted reverse proxy reported, or "".
// X-Forwarded-For comes first because proxies (the bundled Caddy, ingress
// controllers) set or append it themselves, whereas X-Real-IP and
// True-Client-IP are often passed through from the client unchanged.
func forwardedIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// X-Forwarded-For may be "client, proxy1, proxy2" — take the first.
		if idx := strings.IndexByte(xff, ','); idx >= 0 {
			xff = xff[:idx]
		}
		if ip := net.ParseIP(strings.TrimSpace(xff)); ip != nil {
			return ip.String()
		}
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		if ip := net.ParseIP(strings.TrimSpace(xri)); ip != nil {
			return ip.String()
		}
	}
	return ""
}

// trustedRealIP sets RemoteAddr to the client IP reported by the trusted
// reverse proxy, with the same precedence the login limiter uses, so audit
// entries and per-IP limits agree on who the client is. (chi's RealIP
// prefers True-Client-IP and X-Real-IP, which clients can set freely
// through proxies that only manage X-Forwarded-For.)
func trustedRealIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ip := forwardedIP(r); ip != "" {
			r.RemoteAddr = ip
		}
		next.ServeHTTP(w, r)
	})
}

// retryAfter formats d as the delay-seconds form of the Retry-After header
// (RFC 9110 §10.2.3), rounding up so clients never retry early.
func retryAfter(d time.Duration) string {
	secs := int64((d + time.Second - 1) / time.Second)
	if secs < 1 {
		secs = 1
	}
	return strconv.FormatInt(secs, 10)
}

// statusWriter wraps http.ResponseWriter to capture the status code.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (sw *statusWriter) WriteHeader(code int) {
	sw.status = code
	sw.ResponseWriter.WriteHeader(code)
}
