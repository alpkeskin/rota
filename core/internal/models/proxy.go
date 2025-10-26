package models

import "time"

// Proxy represents a proxy server
type Proxy struct {
	ID                 int       `json:"id"`
	Address            string    `json:"address"`
	Protocol           string    `json:"protocol"`
	Username           *string   `json:"username,omitempty"`
	Password           *string   `json:"-"` // Never expose password in JSON
	Status             string    `json:"status"`
	Requests           int64     `json:"requests"`
	SuccessfulRequests int64     `json:"-"`
	FailedRequests     int64     `json:"-"`
	AvgResponseTime    int       `json:"avg_response_time"`
	LastCheck          *time.Time `json:"last_check,omitempty"`
	LastError          *string   `json:"-"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// ProxyWithStats represents a proxy with calculated statistics
type ProxyWithStats struct {
	ID              int        `json:"id"`
	Address         string     `json:"address"`
	Protocol        string     `json:"protocol"`
	Username        *string    `json:"username,omitempty"`
	Status          string     `json:"status"`
	Requests        int64      `json:"requests"`
	SuccessRate     float64    `json:"success_rate"`
	AvgResponseTime int        `json:"avg_response_time"`
	LastCheck       *time.Time `json:"last_check,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// CreateProxyRequest represents a request to create a proxy
type CreateProxyRequest struct {
	Address  string  `json:"address" validate:"required"`
	Protocol string  `json:"protocol" validate:"required,oneof=http https socks4 socks4a socks5"`
	Username *string `json:"username,omitempty"`
	Password *string `json:"password,omitempty"`
}

// UpdateProxyRequest represents a request to update a proxy
type UpdateProxyRequest struct {
	Address  string  `json:"address"`
	Protocol string  `json:"protocol" validate:"omitempty,oneof=http https socks4 socks4a socks5"`
	Username *string `json:"username,omitempty"`
	Password *string `json:"password,omitempty"`
}

// BulkCreateProxyRequest represents a request to create multiple proxies
type BulkCreateProxyRequest struct {
	Proxies []CreateProxyRequest `json:"proxies" validate:"required,min=1"`
}

// BulkDeleteProxyRequest represents a request to delete multiple proxies
type BulkDeleteProxyRequest struct {
	IDs []int `json:"ids" validate:"required,min=1"`
}

// ProxyTestResult represents the result of testing a proxy
type ProxyTestResult struct {
	ID           int        `json:"id"`
	Address      string     `json:"address"`
	Status       string     `json:"status"`
	ResponseTime *int       `json:"response_time,omitempty"`
	Error        *string    `json:"error,omitempty"`
	TestedAt     time.Time  `json:"tested_at"`
}

// ProxyListResponse represents a paginated list of proxies
type ProxyListResponse struct {
	Proxies    []ProxyWithStats `json:"proxies"`
	Pagination PaginationMeta   `json:"pagination"`
}

// PaginationMeta represents pagination metadata
type PaginationMeta struct {
	Page       int `json:"page"`
	Limit      int `json:"limit"`
	Total      int `json:"total"`
	TotalPages int `json:"total_pages"`
}
