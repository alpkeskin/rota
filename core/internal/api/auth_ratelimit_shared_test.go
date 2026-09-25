package api

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/alpkeskin/rota/core/internal/sharedstate"
	"github.com/alpkeskin/rota/core/pkg/logger"
)

func sharedLoginStore(t *testing.T) *sharedstate.Store {
	t.Helper()
	url := os.Getenv("ROTA_TEST_REDIS")
	if url == "" {
		t.Skip("ROTA_TEST_REDIS not set")
	}
	s, err := sharedstate.Open(url, fmt.Sprintf("rotatest%d:", rand.Int64()))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() }) //nolint:errcheck
	return s
}

func TestLoginThrottleSharedAcrossInstances(t *testing.T) {
	st := sharedLoginStore(t)
	a := newAuthRateLimiter(3, 10, 30, 0, 1, false, logger.New("error"))
	b := newAuthRateLimiter(3, 10, 30, 0, 1, false, logger.New("error"))
	a.name, a.shared = "login", st
	b.name, b.shared = "login", st

	// Failures spread over two instances add up to one block.
	hit(limitedHandler(a, http.StatusUnauthorized), "10.0.0.1")
	hit(limitedHandler(b, http.StatusUnauthorized), "10.0.0.1")
	hit(limitedHandler(a, http.StatusUnauthorized), "10.0.0.1")
	w := hit(limitedHandler(b, http.StatusOK), "10.0.0.1")
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("blocked IP on the other instance = %d, want 429", w.Code)
	}
	if ra := w.Header().Get("Retry-After"); ra == "" {
		t.Fatal("no Retry-After on a shared block")
	}
	if w := hit(limitedHandler(b, http.StatusOK), "10.0.0.2"); w.Code != http.StatusOK {
		t.Fatalf("other IP = %d, want 200", w.Code)
	}
}

func TestLoginGlobalLockoutShared(t *testing.T) {
	st := sharedLoginStore(t)
	a := newAuthRateLimiter(100, 10, 30, 3, 1, false, logger.New("error"))
	b := newAuthRateLimiter(100, 10, 30, 3, 1, false, logger.New("error"))
	a.name, a.shared = "login", st
	b.name, b.shared = "login", st
	for i := 0; i < 3; i++ {
		rl := a
		if i%2 == 1 {
			rl = b
		}
		if w := hit(limitedHandler(rl, http.StatusOK), fmt.Sprintf("10.0.1.%d", i)); w.Code != http.StatusOK {
			t.Fatalf("attempt %d = %d", i, w.Code)
		}
	}
	if w := hit(limitedHandler(a, http.StatusOK), "10.0.1.9"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("4th attempt across instances = %d, want 429 (global lockout)", w.Code)
	}
	if w := hit(limitedHandler(b, http.StatusOK), "10.0.1.10"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("lockout not visible on the other instance: %d", w.Code)
	}
}

type downLoginStore struct{}

func (downLoginStore) Hit(context.Context, string, time.Duration) (int, error) {
	return 0, errors.New("down")
}
func (downLoginStore) Block(context.Context, string, time.Duration) error { return errors.New("down") }
func (downLoginStore) Blocked(context.Context, string) (time.Duration, error) {
	return 0, errors.New("down")
}

func TestLoginThrottleFallsBackToLocal(t *testing.T) {
	rl := newAuthRateLimiter(3, 10, 30, 0, 1, false, logger.New("error"))
	rl.name, rl.shared = "login", downLoginStore{}
	h := limitedHandler(rl, http.StatusUnauthorized)
	for i := 0; i < 3; i++ {
		hit(h, "10.0.2.1")
	}
	if w := hit(h, "10.0.2.1"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("store outage disabled brute-force protection: %d", w.Code)
	}
}
