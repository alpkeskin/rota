// API Response Types

export interface Proxy {
  id: number
  address: string
  protocol: "http" | "https" | "socks4" | "socks4a" | "socks5"
  status: "active" | "failed" | "idle"
  requests: number
  success_rate: number
  avg_response_time: number
  last_check: string
  username?: string
  created_at: string
  updated_at: string
}

export interface ProxiesResponse {
  proxies: Proxy[]
  pagination: {
    page: number
    limit: number
    total: number
    total_pages: number
  }
}

export interface DashboardStats {
  active_proxies: number
  total_proxies: number
  total_requests: number
  avg_success_rate: number
  avg_response_time: number
  request_growth: number
  success_rate_growth: number
  response_time_delta: number
}

export interface ChartDataPoint {
  time: string
  value?: number
  success?: number
  failure?: number
}

export interface ChartResponse {
  data: ChartDataPoint[]
}

export interface LogEntry {
  id: string
  timestamp: string
  level: "info" | "warning" | "error" | "success"
  message: string
  details?: string
  metadata?: Record<string, any>
}

export interface LogsResponse {
  logs: LogEntry[]
  pagination: {
    page: number
    limit: number
    total: number
    total_pages: number
  }
}

export interface SystemMetrics {
  memory: {
    total: number
    used: number
    available: number
    percentage: number
  }
  cpu: {
    percentage: number
    cores: number
  }
  disk: {
    total: number
    used: number
    free: number
    percentage: number
  }
  runtime: {
    goroutines: number
    threads: number
    gc_pause_count: number
    mem_alloc: number
    mem_sys: number
  }
}

export interface Settings {
  authentication: {
    enabled: boolean
    username: string
    password: string
  }
  rotation: {
    method: "random" | "roundrobin" | "least_conn" | "time_based"
    time_based?: {
      interval: number
    }
    remove_unhealthy: boolean
    fallback: boolean
    fallback_max_retries: number
    follow_redirect: boolean
    timeout: number
    retries: number
    allowed_protocols: string[]
    max_response_time: number
    min_success_rate: number
  }
  rate_limit: {
    enabled: boolean
    interval: number
    max_requests: number
  }
  healthcheck: {
    timeout: number
    workers: number
    url: string
    status: number
    headers: string[]
  }
  log_retention: {
    enabled: boolean
    retention_days: number
    compression_after_days: number
    cleanup_interval_hours: number
  }
}

export interface AuthResponse {
  token: string
  user: {
    username: string
  }
}

export interface ApiError {
  error: string
  details?: string
}

// Request Types
export interface AddProxyRequest {
  address: string
  protocol: "http" | "https" | "socks4" | "socks4a" | "socks5"
  username?: string
  password?: string
}

export interface UpdateProxyRequest {
  address?: string
  protocol?: "http" | "https" | "socks4" | "socks4a" | "socks5"
  username?: string
  password?: string
}

export interface BulkProxyRequest {
  proxies: AddProxyRequest[]
}

export interface BulkDeleteRequest {
  ids: number[]
}

export interface ProxyTestResult {
  id: number
  address: string
  status: "active" | "failed"
  response_time?: number
  error?: string
  tested_at: string
  duration?: number // Alias for response_time for better clarity
}

// ── Proxy Sources ──────────────────────────────────────────────────────────
export interface ProxySource {
  id: number
  name: string
  url: string
  protocol: "http" | "https" | "socks4" | "socks4a" | "socks5"
  enabled: boolean
  interval_minutes: number
  last_fetched_at?: string
  last_count: number        // newly imported on last fetch
  last_total: number        // total lines returned on last fetch
  last_error?: string
  cleanup_enabled: boolean
  cleanup_days: number
  created_at: string
  updated_at: string
}

export interface CreateSourceRequest {
  name: string
  url: string
  protocol: "http" | "https" | "socks4" | "socks4a" | "socks5"
  enabled: boolean
  interval_minutes: number
  cleanup_enabled?: boolean
  cleanup_days?: number
}

export interface UpdateSourceRequest {
  name?: string
  url?: string
  protocol?: string
  enabled?: boolean
  interval_minutes?: number
  cleanup_enabled?: boolean
  cleanup_days?: number
}

// ── Proxy Pools ────────────────────────────────────────────────────────────
export interface ProxyPool {
  id: number
  name: string
  description: string
  country_code?: string
  region_name?: string
  city_name?: string
  rotation_method: "roundrobin" | "random" | "stick"
  stick_count: number
  health_check_url: string
  health_check_cron: string
  health_check_enabled: boolean
  auto_sync: boolean
  sync_mode: "auto" | "manual"
  enabled: boolean
  total_proxies: number
  active_proxies: number
  failed_proxies: number
  geo_filters?: GeoFilter[]
  isp_filters?: string[]
  tag_filters?: string[]
  created_at: string
  updated_at: string
}

export interface PoolAlertRule {
  id: number
  pool_id: number
  enabled: boolean
  min_active_proxies: number
  webhook_url: string
  webhook_method: "POST" | "GET"
  last_fired_at?: string
  cooldown_minutes: number
  created_at: string
  updated_at: string
}

export interface CreatePoolAlertRuleRequest {
  enabled: boolean
  min_active_proxies: number
  webhook_url: string
  webhook_method?: "POST" | "GET"
  cooldown_minutes?: number
}

export interface PoolProxy {
  proxy_id: number
  address: string
  protocol: string
  status: string
  country_code?: string
  country_name?: string
  region_name?: string
  city_name?: string
  isp?: string
  requests: number
  success_rate: number
  avg_response_time: number
  last_check?: string
  added_at: string
}

export interface GeoSummaryItem {
  country_code: string
  country_name: string
  region_name: string
  city_name: string
  total: number
  active: number
}

export interface GeoCityItem {
  city_name: string
  region_name: string
  total: number
  active: number
}

export interface GeoFilter {
  country_code: string
  city_name?: string
}

export interface PoolHealthCheckResult {
  pool_id: number
  pool_name: string
  checked: number
  active: number
  failed: number
  results: ProxyTestResult[]
  started_at: string
  finished_at: string
}

export type HCJobStatus = "pending" | "running" | "done" | "failed"

export interface HCJob {
  id: string
  pool_id: number
  pool_name: string
  status: HCJobStatus
  progress: number
  total: number
  active: number
  failed: number
  check_url: string
  workers: number
  error?: string
  started_at: string
  updated_at: string
  finished_at?: string
  results?: ProxyTestResult[]
}

// ── Proxy Users ────────────────────────────────────────────────────────────
export interface ProxyUser {
  id: number
  username: string
  enabled: boolean
  main_pool_id?: number
  main_pool_name?: string
  fallback_pool_ids: number[]
  max_retries: number
  requests_per_minute: number
  created_at: string
  updated_at: string
}

export interface CreateProxyUserRequest {
  username: string
  password: string
  enabled: boolean
  main_pool_id?: number | null
  fallback_pool_ids: number[]
  max_retries: number
  requests_per_minute?: number
}

export interface UpdateProxyUserRequest {
  password?: string
  enabled?: boolean
  main_pool_id?: number | null
  fallback_pool_ids?: number[]
  max_retries?: number
  requests_per_minute?: number
}

export interface CreatePoolRequest {
  name: string
  description?: string
  country_code?: string
  region_name?: string
  city_name?: string
  geo_filters?: GeoFilter[]
  isp_filters?: string[]
  tag_filters?: string[]
  rotation_method: "roundrobin" | "random" | "stick"
  stick_count: number
  health_check_url?: string
  health_check_cron?: string
  health_check_enabled: boolean
  auto_sync: boolean
  sync_mode?: "auto" | "manual"
  enabled: boolean
}

export interface ProxyWithTags {
  id: number
  address: string
  protocol: string
  status: string
  tags: string[]
  country_code?: string
  country_name?: string
  city_name?: string
  isp?: string
}
