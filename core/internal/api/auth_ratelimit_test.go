package api

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/alpkeskin/rota/core/pkg/logger"
)

func limitedHandler(rl *authRateLimiter, status int) http.Handler {
	return rl.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}))
}

func hit(h http.Handler, ip string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.RemoteAddr = ip + ":1234"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestAuthRateLimiterGlobalDisabled(t *testing.T) {
	// globalMax 0: any volume of traffic (even all failures) never engages a
	// global lockout; only the offending IP is blocked.
	rl := newAuthRateLimiter(3, 10, 30, 0, 1, false, logger.New("error"))
	h := limitedHandler(rl, http.StatusUnauthorized)
	for i := 0; i < 50; i++ {
		hit(h, "10.0.0.1")
	}
	if w := hit(h, "10.0.0.1"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("attacker IP = %d, want 429", w.Code)
	}
	if w := hit(limitedHandler(rl, http.StatusOK), "10.0.0.2"); w.Code != http.StatusOK {
		t.Fatalf("other IP = %d, want 200 (no global lockout)", w.Code)
	}
}

func TestAuthRateLimiterGlobalLockoutStillWorks(t *testing.T) {
	rl := newAuthRateLimiter(1000, 10, 30, 5, 1, false, logger.New("error"))
	h := limitedHandler(rl, http.StatusOK)
	for i := 0; i < 5; i++ {
		if w := hit(h, "10.0.0.1"); w.Code != http.StatusOK {
			t.Fatalf("request %d = %d before the global cap", i, w.Code)
		}
	}
	w := hit(h, "10.0.0.9")
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("over global cap = %d, want 429", w.Code)
	}
	if secs, err := strconv.Atoi(w.Header().Get("Retry-After")); err != nil || secs != 60 {
		t.Fatalf("Retry-After = %q, want delay-seconds 60", w.Header().Get("Retry-After"))
	}
}

func TestAuthRateLimiterIgnoresForbidden(t *testing.T) {
	// 403 = authenticated but not permitted: must never block the IP.
	rl := newAuthRateLimiter(3, 10, 30, 0, 1, false, logger.New("error"))
	h := limitedHandler(rl, http.StatusForbidden)
	for i := 0; i < 10; i++ {
		if w := hit(h, "10.0.0.1"); w.Code != http.StatusForbidden {
			t.Fatalf("request %d = %d, want 403 (not throttled)", i, w.Code)
		}
	}
}

func TestRetryAfterSeconds(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0:                            "1",
		300 * time.Millisecond:       "1",
		time.Second:                  "1",
		1500 * time.Millisecond:      "2",
		29*time.Minute + time.Second: "1741",
	} {
		if got := retryAfter(d); got != want {
			t.Errorf("retryAfter(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestExportLimiterSkipsTokenRequests(t *testing.T) {
	s := &Server{exportRL: newAuthRateLimiter(2, 10, 30, 0, 0, false, logger.New("error"))}
	h := s.exportLimited(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized) // every attempt fails
	}))
	send := func(prep func(*http.Request)) int {
		r := httptest.NewRequest(http.MethodGet, "/export", nil)
		r.RemoteAddr = "10.0.0.1:1234"
		prep(r)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	withToken := func(r *http.Request) { r.Header.Set("Authorization", "Bearer rota_exp_revoked") }
	withPassword := func(r *http.Request) { r.SetBasicAuth("u", "wrong") }

	// A client polling with a revoked token never gets the IP blocked...
	for i := 0; i < 10; i++ {
		if code := send(withToken); code != http.StatusUnauthorized {
			t.Fatalf("token request %d = %d, want 401 (not limited)", i, code)
		}
	}
	if code := send(withPassword); code != http.StatusUnauthorized {
		t.Fatalf("first password attempt = %d, want 401", code)
	}
	// ...while password guessing still is.
	send(withPassword)
	if code := send(withPassword); code != http.StatusTooManyRequests {
		t.Fatalf("password guessing = %d, want 429", code)
	}
	// Token requests keep working from the blocked IP.
	if code := send(withToken); code != http.StatusUnauthorized {
		t.Fatalf("token request from blocked IP = %d, want to reach the handler", code)
	}
}

func TestRetryAfterNotEarly(t *testing.T) {
	rl := newAuthRateLimiter(1, 10, 1, 0, 0, false, logger.New("error"))
	h := limitedHandler(rl, http.StatusUnauthorized)
	hit(h, "10.0.0.5")
	w := hit(h, "10.0.0.5")
	// Blocked for 1 minute minus a few microseconds: must round up to 60.
	if got := w.Header().Get("Retry-After"); got != "60" {
		t.Fatalf("Retry-After = %q, want 60", got)
	}
}
