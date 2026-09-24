package handlers

import (
	"context"
	"sync"
	"time"

	"github.com/alpkeskin/rota/core/internal/auth"
	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/pkg/logger"
)

// Limits for re-entering the current password inside a session.
const (
	passwordConfirmMaxFailures = 5
	passwordConfirmWindow      = 15 * time.Minute
)

// PasswordConfirmGuard limits guessing of the current password from inside
// a session (API key creation, password change). Those checks aren't behind
// the login limiter, so a stolen session token could otherwise brute-force
// the password and mint a lasting API key. After too many misses the
// account's sessions — including the attacker's — are revoked.
type PasswordConfirmGuard struct {
	accounts *repository.AccountRepository
	audit    *repository.AuditRepository
	logger   *logger.Logger

	mu       sync.Mutex
	failures map[int][]time.Time // account id → recent failure times
	now      func() time.Time
}

// NewPasswordConfirmGuard creates a guard.
func NewPasswordConfirmGuard(accounts *repository.AccountRepository, audit *repository.AuditRepository, log *logger.Logger) *PasswordConfirmGuard {
	return &PasswordConfirmGuard{accounts: accounts, audit: audit, logger: log, failures: map[int][]time.Time{}, now: time.Now}
}

// Succeeded clears the failure count after a correct password.
func (g *PasswordConfirmGuard) Succeeded(accountID int) {
	g.mu.Lock()
	delete(g.failures, accountID)
	g.mu.Unlock()
}

// Failed records a wrong password and, past the limit, revokes every
// session of the account. It reports whether sessions were revoked.
func (g *PasswordConfirmGuard) Failed(ctx context.Context, p *auth.Principal, ip string) bool {
	now := g.now()
	g.mu.Lock()
	recent := g.failures[p.AccountID][:0]
	for _, t := range g.failures[p.AccountID] {
		if now.Sub(t) < passwordConfirmWindow {
			recent = append(recent, t)
		}
	}
	recent = append(recent, now)
	lock := len(recent) >= passwordConfirmMaxFailures
	if lock {
		delete(g.failures, p.AccountID)
	} else {
		g.failures[p.AccountID] = recent
	}
	g.mu.Unlock()
	if !lock {
		return false
	}

	if _, err := g.accounts.RevokeSessions(ctx, p.AccountID); err != nil {
		g.logger.Error("failed to revoke sessions after repeated wrong passwords", "account_id", p.AccountID, "error", err)
	}
	g.logger.Warn("too many wrong current-password attempts; sessions revoked", "username", p.Username)
	id := p.AccountID
	actx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := g.audit.Record(actx, models.AuditEntry{
		ActorType: string(p.Type), ActorID: &id, ActorName: p.ActorName(),
		Action: "auth.sessions_revoked", Status: 401, IP: ip,
		Details: map[string]any{"reason": "too many wrong current-password attempts"},
	}); err != nil {
		g.logger.Error("failed to write audit entry", "action", "auth.sessions_revoked", "error", err)
	}
	return true
}
