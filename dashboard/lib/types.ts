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
