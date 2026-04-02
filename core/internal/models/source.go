package models

import "time"

// ProxySource represents a remote URL that provides a list of proxies
type ProxySource struct {
	ID              int        `json:"id"`
	Name            string     `json:"name"`
	URL             string     `json:"url"`
	Protocol        string     `json:"protocol"`
	Enabled         bool       `json:"enabled"`
	IntervalMinutes int        `json:"interval_minutes"`
	LastFetchedAt   *time.Time `json:"last_fetched_at,omitempty"`
	LastCount       int        `json:"last_count"`
	LastError       *string    `json:"last_error,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// CreateProxySourceRequest is the payload for creating a source
type CreateProxySourceRequest struct {
	Name            string `json:"name"     validate:"required"`
	URL             string `json:"url"      validate:"required,url"`
	Protocol        string `json:"protocol" validate:"required,oneof=http https socks4 socks4a socks5"`
	Enabled         bool   `json:"enabled"`
	IntervalMinutes int    `json:"interval_minutes" validate:"min=1"`
}

// UpdateProxySourceRequest is the payload for updating a source
type UpdateProxySourceRequest struct {
	Name            string `json:"name"`
	URL             string `json:"url"`
	Protocol        string `json:"protocol" validate:"omitempty,oneof=http https socks4 socks4a socks5"`
	Enabled         *bool  `json:"enabled"`
	IntervalMinutes int    `json:"interval_minutes" validate:"omitempty,min=1"`
}
