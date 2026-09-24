// Package auth holds the dashboard/API access model: roles, the principal an
// authenticated request acts as, and session tokens.
package auth

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Role is an access level. Roles are ordered: each includes the ones below.
type Role string

const (
	// RoleViewer can read everything except secrets, accounts and the audit log.
	RoleViewer Role = "viewer"
	// RoleOperator can also change proxies, sources, pools and proxy users.
	RoleOperator Role = "operator"
	// RoleAdmin can also change settings, manage accounts and read the audit log.
	RoleAdmin Role = "admin"
)

// Roles lists every role from least to most privileged.
var Roles = []Role{RoleViewer, RoleOperator, RoleAdmin}

func (r Role) rank() int {
	switch r {
	case RoleViewer:
		return 1
	case RoleOperator:
		return 2
	case RoleAdmin:
		return 3
	}
	return 0
}

// Valid reports whether r is a known role.
func (r Role) Valid() bool { return r.rank() > 0 }

// AtLeast reports whether r grants everything min grants.
func (r Role) AtLeast(min Role) bool { return r.Valid() && r.rank() >= min.rank() }

// Min returns the less privileged of two roles.
func Min(a, b Role) Role {
	if a.rank() <= b.rank() {
		return a
	}
	return b
}

// ParseRole validates a role name.
func ParseRole(s string) (Role, error) {
	r := Role(s)
	if !r.Valid() {
		return "", fmt.Errorf("invalid role %q (must be viewer, operator or admin)", s)
	}
	return r, nil
}

// PrincipalType says how a request authenticated.
type PrincipalType string

const (
	// PrincipalSession is a dashboard login (JWT).
	PrincipalSession PrincipalType = "session"
	// PrincipalAPIKey is an API key.
	PrincipalAPIKey PrincipalType = "api_key"
)

// Principal is who an authenticated request acts as.
type Principal struct {
	Type      PrincipalType
	AccountID int
	Username  string
	// Role is the effective role: for an API key, the lower of the key's role
	// and its owner's current role.
	Role       Role
	APIKeyID   int
	APIKeyName string
}

// ActorName is a human-readable label for audit entries.
func (p *Principal) ActorName() string {
	if p.Type == PrincipalAPIKey {
		return p.Username + " (key: " + p.APIKeyName + ")"
	}
	return p.Username
}

type principalKey struct{}

// WithPrincipal returns a context carrying p.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// FromContext returns the request's principal, or nil if unauthenticated.
func FromContext(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey{}).(*Principal)
	return p
}

// SessionTTL is how long a dashboard session token is valid.
const SessionTTL = 24 * time.Hour

// sessionClaims are the JWT claims of a dashboard session. The role is not
// trusted from the token: it is re-read from the account on every request,
// so role changes and revocations apply immediately.
type sessionClaims struct {
	TokenVersion int    `json:"tv"`
	Username     string `json:"username"`
	jwt.RegisteredClaims
}

// ErrInvalidToken is returned for malformed, expired or wrongly signed tokens.
var ErrInvalidToken = errors.New("invalid or expired token")

// IssueSession signs a session token for an account at its current token
// version. Bumping the account's version revokes every token issued before.
func IssueSession(secret []byte, accountID, tokenVersion int, username string, now time.Time) (string, error) {
	claims := sessionClaims{
		TokenVersion: tokenVersion,
		Username:     username,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.Itoa(accountID),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(SessionTTL)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
}

// ParseSession verifies a session token and returns its account ID and token
// version. Tokens from before accounts existed (no subject) are rejected, so
// those sessions must sign in again once.
func ParseSession(secret []byte, tokenStr string) (accountID, tokenVersion int, err error) {
	var claims sessionClaims
	tok, err := jwt.ParseWithClaims(tokenStr, &claims, func(t *jwt.Token) (interface{}, error) {
		// Restrict to HMAC to prevent algorithm-confusion attacks.
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return secret, nil
	}, jwt.WithExpirationRequired())
	if err != nil || !tok.Valid {
		return 0, 0, ErrInvalidToken
	}
	id, err := strconv.Atoi(claims.Subject)
	if err != nil || id <= 0 {
		return 0, 0, ErrInvalidToken
	}
	return id, claims.TokenVersion, nil
}
