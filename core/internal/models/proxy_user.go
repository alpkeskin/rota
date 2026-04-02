package models

import "time"

// ProxyUser is a user that authenticates to the proxy server port (8000).
// Each user has a main pool and optional ordered fallback pools.
type ProxyUser struct {
	ID              int       `json:"id"`
	Username        string    `json:"username"`
	PasswordHash    string    `json:"-"` // bcrypt, never in JSON
	Enabled         bool      `json:"enabled"`
	MainPoolID      *int      `json:"main_pool_id,omitempty"`
	FallbackPoolIDs []int     `json:"fallback_pool_ids"`
	MaxRetries      int       `json:"max_retries"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`

	// Enriched fields (JOIN, not stored)
	MainPoolName string `json:"main_pool_name,omitempty"`
}

// ProxyUserWithPools is ProxyUser + full pool objects for the API
type ProxyUserWithPools struct {
	ProxyUser
	MainPool      *ProxyPool  `json:"main_pool,omitempty"`
	FallbackPools []ProxyPool `json:"fallback_pools"`
}

// CreateProxyUserRequest is the payload for POST /api/v1/proxy-users
type CreateProxyUserRequest struct {
	Username        string `json:"username"    validate:"required"`
	Password        string `json:"password"    validate:"required,min=6"`
	Enabled         bool   `json:"enabled"`
	MainPoolID      *int   `json:"main_pool_id,omitempty"`
	FallbackPoolIDs []int  `json:"fallback_pool_ids"`
	MaxRetries      int    `json:"max_retries"  validate:"min=1,max=50"`
}

// UpdateProxyUserRequest is the payload for PUT /api/v1/proxy-users/{id}
type UpdateProxyUserRequest struct {
	Password        string `json:"password,omitempty"`
	Enabled         *bool  `json:"enabled,omitempty"`
	MainPoolID      *int   `json:"main_pool_id"`      // null clears it
	FallbackPoolIDs []int  `json:"fallback_pool_ids"` // replaces list
	MaxRetries      int    `json:"max_retries,omitempty"`
}

// proxyUserContextKey is used to pass the resolved ProxyUser through request context
type proxyUserContextKey struct{}

// ProxyUserContextKey is the exported key for request context
var ProxyUserContextKey = proxyUserContextKey{}
