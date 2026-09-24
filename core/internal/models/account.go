package models

import "time"

// Account is a dashboard/API login. Its role decides what it may do.
type Account struct {
	ID           int        `json:"id"`
	Username     string     `json:"username"`
	Role         string     `json:"role"`
	Enabled      bool       `json:"enabled"`
	TokenVersion int        `json:"-"`
	LastLoginAt  *time.Time `json:"last_login_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// CreateAccountRequest is the payload for POST /api/v1/accounts.
type CreateAccountRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

// UpdateAccountRequest is the payload for PUT /api/v1/accounts/{id}. Omitted
// fields are left unchanged; setting a password signs the account out.
type UpdateAccountRequest struct {
	Role     *string `json:"role,omitempty"`
	Enabled  *bool   `json:"enabled,omitempty"`
	Password *string `json:"password,omitempty"`
}

// APIKey is a long-lived credential for automation. The secret itself is
// only returned once, when the key is created.
type APIKey struct {
	ID         int        `json:"id"`
	AccountID  int        `json:"account_id"`
	Username   string     `json:"username"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Role       string     `json:"role"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// CreateAPIKeyRequest is the payload for POST /api/v1/api-keys.
type CreateAPIKeyRequest struct {
	Name          string `json:"name"`
	Role          string `json:"role"`            // defaults to the caller's role
	ExpiresInDays int    `json:"expires_in_days"` // 0 = never expires
}

// CreateAPIKeyResponse returns the new key's secret exactly once.
type CreateAPIKeyResponse struct {
	APIKey
	Key string `json:"key"`
}

// AuditEntry is one recorded action.
type AuditEntry struct {
	ID        int64          `json:"id"`
	At        time.Time      `json:"at"`
	ActorType string         `json:"actor_type"`
	ActorID   *int           `json:"actor_id,omitempty"`
	ActorName string         `json:"actor_name"`
	Action    string         `json:"action"`
	Resource  string         `json:"resource"`
	Status    int            `json:"status"`
	IP        string         `json:"ip"`
	Details   map[string]any `json:"details,omitempty"`
}

// AuditLogResponse is a page of audit entries.
type AuditLogResponse struct {
	Entries []AuditEntry `json:"entries"`
	Total   int          `json:"total"`
	Page    int          `json:"page"`
	Limit   int          `json:"limit"`
}
