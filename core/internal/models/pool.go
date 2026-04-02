package models

import "time"

// ProxyPool is a named group of proxies filtered by geo or manually managed
type ProxyPool struct {
	ID                 int        `json:"id"`
	Name               string     `json:"name"`
	Description        string     `json:"description"`
	CountryCode        *string    `json:"country_code,omitempty"`
	RegionName         *string    `json:"region_name,omitempty"`
	CityName           *string    `json:"city_name,omitempty"`
	RotationMethod     string     `json:"rotation_method"`
	StickCount         int        `json:"stick_count"`
	HealthCheckURL     string     `json:"health_check_url"`
	HealthCheckCron    string     `json:"health_check_cron"`
	HealthCheckEnabled bool       `json:"health_check_enabled"`
	AutoSync           bool       `json:"auto_sync"`
	Enabled            bool       `json:"enabled"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`

	// Computed fields (not stored)
	TotalProxies  int         `json:"total_proxies"`
	ActiveProxies int         `json:"active_proxies"`
	FailedProxies int         `json:"failed_proxies"`
	GeoFilters    []GeoFilter `json:"geo_filters,omitempty"`
}

// PoolProxy is the join between a pool and a proxy (with stats)
type PoolProxy struct {
	ProxyID         int        `json:"proxy_id"`
	Address         string     `json:"address"`
	Protocol        string     `json:"protocol"`
	Status          string     `json:"status"`
	CountryCode     *string    `json:"country_code,omitempty"`
	CountryName     *string    `json:"country_name,omitempty"`
	RegionName      *string    `json:"region_name,omitempty"`
	CityName        *string    `json:"city_name,omitempty"`
	ISP             *string    `json:"isp,omitempty"`
	Requests        int64      `json:"requests"`
	SuccessRate     float64    `json:"success_rate"`
	AvgResponseTime int        `json:"avg_response_time"`
	LastCheck       *time.Time `json:"last_check,omitempty"`
	AddedAt         time.Time  `json:"added_at"`
}

// PoolHealthCheckResult is per-proxy result from a pool health check run
type PoolHealthCheckResult struct {
	PoolID       int                    `json:"pool_id"`
	PoolName     string                 `json:"pool_name"`
	Checked      int                    `json:"checked"`
	Active       int                    `json:"active"`
	Failed       int                    `json:"failed"`
	Results      []ProxyTestResult      `json:"results"`
	StartedAt    time.Time              `json:"started_at"`
	FinishedAt   time.Time              `json:"finished_at"`
}

// GeoInfo is the result of a GeoIP lookup
type GeoInfo struct {
	CountryCode string  `json:"country_code"`
	CountryName string  `json:"country_name"`
	RegionName  string  `json:"region_name"`
	CityName    string  `json:"city_name"`
	ISP         string  `json:"isp"`
	Latitude    float64 `json:"lat"`
	Longitude   float64 `json:"lon"`
}

// CreatePoolRequest is the payload for creating a pool
type CreatePoolRequest struct {
	Name               string      `json:"name"    validate:"required"`
	Description        string      `json:"description"`
	CountryCode        *string     `json:"country_code,omitempty"`
	RegionName         *string     `json:"region_name,omitempty"`
	CityName           *string     `json:"city_name,omitempty"`
	GeoFilters         []GeoFilter `json:"geo_filters,omitempty"` // multi-location
	RotationMethod     string      `json:"rotation_method" validate:"required,oneof=roundrobin random stick"`
	StickCount         int         `json:"stick_count"     validate:"min=1"`
	HealthCheckURL     string      `json:"health_check_url"`
	HealthCheckCron    string      `json:"health_check_cron"`
	HealthCheckEnabled bool        `json:"health_check_enabled"`
	AutoSync           bool        `json:"auto_sync"`
	Enabled            bool        `json:"enabled"`
}

// UpdatePoolRequest is the payload for updating a pool
type UpdatePoolRequest struct {
	Name               string      `json:"name"`
	Description        string      `json:"description"`
	CountryCode        *string     `json:"country_code"`
	RegionName         *string     `json:"region_name"`
	CityName           *string     `json:"city_name"`
	GeoFilters         []GeoFilter `json:"geo_filters"` // replaces filters when provided
	RotationMethod     string      `json:"rotation_method" validate:"omitempty,oneof=roundrobin random stick"`
	StickCount         int         `json:"stick_count"     validate:"omitempty,min=1"`
	HealthCheckURL     string      `json:"health_check_url"`
	HealthCheckCron    string      `json:"health_check_cron"`
	HealthCheckEnabled *bool       `json:"health_check_enabled"`
	AutoSync           *bool       `json:"auto_sync"`
	Enabled            *bool       `json:"enabled"`
}

// GeoFilter is one row from pool_geo_filters
type GeoFilter struct {
	CountryCode string `json:"country_code"`
	CityName    string `json:"city_name,omitempty"`
}

// GeoSummary is an aggregated view of geo distribution in the proxy table
type GeoSummary struct {
	CountryCode string `json:"country_code"`
	CountryName string `json:"country_name"`
	RegionName  string `json:"region_name"`
	CityName    string `json:"city_name"`
	Total       int    `json:"total"`
	Active      int    `json:"active"`
}

// GeoCitySummary is city-level breakdown within a country
type GeoCitySummary struct {
	CityName   string `json:"city_name"`
	RegionName string `json:"region_name"`
	Total      int    `json:"total"`
	Active     int    `json:"active"`
}
