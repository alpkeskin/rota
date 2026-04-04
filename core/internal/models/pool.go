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
	SyncMode           string     `json:"sync_mode"` // "auto" | "manual"
	Enabled            bool       `json:"enabled"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`

	// Computed fields (not stored)
	TotalProxies  int          `json:"total_proxies"`
	ActiveProxies int          `json:"active_proxies"`
	FailedProxies int          `json:"failed_proxies"`
	GeoFilters    []GeoFilter  `json:"geo_filters,omitempty"`
	ISPFilters    []string     `json:"isp_filters,omitempty"`
	TagFilters    []string     `json:"tag_filters,omitempty"`
}

// PoolAlertRule defines a webhook alert for a pool when active proxies drop below threshold
type PoolAlertRule struct {
	ID                int        `json:"id"`
	PoolID            int        `json:"pool_id"`
	Enabled           bool       `json:"enabled"`
	MinActiveProxies  int        `json:"min_active_proxies"`
	WebhookURL        string     `json:"webhook_url"`
	WebhookMethod     string     `json:"webhook_method"`
	LastFiredAt       *time.Time `json:"last_fired_at,omitempty"`
	CooldownMinutes   int        `json:"cooldown_minutes"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// CreatePoolAlertRuleRequest is the payload for creating an alert rule
type CreatePoolAlertRuleRequest struct {
	Enabled          bool   `json:"enabled"`
	MinActiveProxies int    `json:"min_active_proxies" validate:"required,min=1"`
	WebhookURL       string `json:"webhook_url"       validate:"required,url"`
	WebhookMethod    string `json:"webhook_method"    validate:"omitempty,oneof=POST GET"`
	CooldownMinutes  int    `json:"cooldown_minutes"  validate:"min=1"`
}

// PoolAlertPayload is what we POST to the webhook URL
type PoolAlertPayload struct {
	Event         string    `json:"event"`
	PoolID        int       `json:"pool_id"`
	PoolName      string    `json:"pool_name"`
	ActiveProxies int       `json:"active_proxies"`
	TotalProxies  int       `json:"total_proxies"`
	Threshold     int       `json:"threshold"`
	FiredAt       time.Time `json:"fired_at"`
}

// PoolProxy is the join between a pool and a proxy (with stats)
type PoolProxy struct {
	ProxyID         int        `json:"proxy_id"`
	Address         string     `json:"address"`
	Protocol        string     `json:"protocol"`
	Username        *string    `json:"-"`
	Password        *string    `json:"-"`
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

// ToProxy converts a PoolProxy to a Proxy (for transport creation with auth).
func (pp *PoolProxy) ToProxy() *Proxy {
	return &Proxy{
		ID:       pp.ProxyID,
		Address:  pp.Address,
		Protocol: pp.Protocol,
		Username: pp.Username,
		Password: pp.Password,
	}
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
	GeoFilters         []GeoFilter `json:"geo_filters,omitempty"`  // multi-location
	ISPFilters         []string    `json:"isp_filters,omitempty"`  // ISP name substrings
	TagFilters         []string    `json:"tag_filters,omitempty"`  // proxy tags (AND logic)
	RotationMethod     string      `json:"rotation_method" validate:"required,oneof=roundrobin random stick"`
	StickCount         int         `json:"stick_count"     validate:"min=1"`
	HealthCheckURL     string      `json:"health_check_url"`
	HealthCheckCron    string      `json:"health_check_cron"`
	HealthCheckEnabled bool        `json:"health_check_enabled"`
	AutoSync           bool        `json:"auto_sync"`
	SyncMode           string      `json:"sync_mode" validate:"omitempty,oneof=auto manual"`
	Enabled            bool        `json:"enabled"`
}

// UpdatePoolRequest is the payload for updating a pool
type UpdatePoolRequest struct {
	Name               string      `json:"name"`
	Description        string      `json:"description"`
	CountryCode        *string     `json:"country_code"`
	RegionName         *string     `json:"region_name"`
	CityName           *string     `json:"city_name"`
	GeoFilters         []GeoFilter `json:"geo_filters"`  // replaces filters when provided
	ISPFilters         []string    `json:"isp_filters"`  // replaces ISP filters
	TagFilters         []string    `json:"tag_filters"`  // replaces tag filters
	RotationMethod     string      `json:"rotation_method" validate:"omitempty,oneof=roundrobin random stick"`
	StickCount         int         `json:"stick_count"     validate:"omitempty,min=1"`
	HealthCheckURL     string      `json:"health_check_url"`
	HealthCheckCron    string      `json:"health_check_cron"`
	HealthCheckEnabled *bool       `json:"health_check_enabled"`
	AutoSync           *bool       `json:"auto_sync"`
	SyncMode           string      `json:"sync_mode" validate:"omitempty,oneof=auto manual"`
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
