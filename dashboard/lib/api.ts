import {
  Proxy,
  ProxiesResponse,
  DashboardStats,
  ChartResponse,
  LogsResponse,
  SystemMetrics,
  Settings,
  AuthResponse,
  AddProxyRequest,
  UpdateProxyRequest,
  BulkProxyRequest,
  BulkDeleteRequest,
  ProxyTestResult,
  ProxySource,
  CreateSourceRequest,
  UpdateSourceRequest,
  ProxyPool,
  PoolProxy,
  GeoSummaryItem,
  GeoCityItem,
  PoolHealthCheckResult,
  HCJob,
  CreatePoolRequest,
  ProxyUser,
  CreateProxyUserRequest,
  UpdateProxyUserRequest,
} from "./types"

const API_BASE_URL = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8001"

class ApiClient {
  private baseUrl: string
  private token: string | null = null

  constructor(baseUrl: string = API_BASE_URL) {
    this.baseUrl = baseUrl
    // Load token from localStorage if available
    if (typeof window !== "undefined") {
      this.token = localStorage.getItem("auth_token")
    }
  }

  setToken(token: string) {
    this.token = token
    if (typeof window !== "undefined") {
      localStorage.setItem("auth_token", token)
    }
  }

  clearToken() {
    this.token = null
    if (typeof window !== "undefined") {
      localStorage.removeItem("auth_token")
    }
  }

  private getHeaders(): HeadersInit {
    const headers: HeadersInit = {
      "Content-Type": "application/json",
    }
    if (this.token) {
      headers["Authorization"] = `Bearer ${this.token}`
    }
    return headers
  }

  private async request<T>(
    endpoint: string,
    options: RequestInit = {}
  ): Promise<T> {
    const url = `${this.baseUrl}${endpoint}`
    const response = await fetch(url, {
      ...options,
      headers: {
        ...this.getHeaders(),
        ...options.headers,
      },
    })

    if (!response.ok) {
      const error = await response.json().catch(() => ({
        error: `HTTP ${response.status}: ${response.statusText}`,
      }))
      throw new Error(error.error || error.message || "Request failed")
    }

    // Handle 204 No Content
    if (response.status === 204) {
      return {} as T
    }

    return response.json()
  }

  // Authentication
  async login(username: string, password: string): Promise<AuthResponse> {
    const response = await this.request<AuthResponse>("/api/v1/auth/login", {
      method: "POST",
      body: JSON.stringify({ username, password }),
    })
    this.setToken(response.token)
    return response
  }

  async getAdminInfo(): Promise<{ username: string }> {
    return this.request("/api/v1/auth/me")
  }

  async changePassword(opts: {
    current_password: string
    new_password: string
    new_username?: string
  }): Promise<{ message: string; username: string; token: string }> {
    const res = await this.request<{ message: string; username: string; token: string }>(
      "/api/v1/auth/change-password",
      { method: "POST", body: JSON.stringify(opts) }
    )
    // Update stored token so session stays valid after username/password change
    this.setToken(res.token)
    return res
  }

  // Dashboard
  async getDashboardStats(): Promise<DashboardStats> {
    return this.request<DashboardStats>("/api/v1/dashboard/stats")
  }

  async getResponseTimeChart(interval: string = "4h"): Promise<ChartResponse> {
    return this.request<ChartResponse>(
      `/api/v1/dashboard/charts/response-time?interval=${interval}`
    )
  }

  async getSuccessRateChart(interval: string = "4h"): Promise<ChartResponse> {
    return this.request<ChartResponse>(
      `/api/v1/dashboard/charts/success-rate?interval=${interval}`
    )
  }

  // Proxies
  async getProxies(params?: {
    page?: number
    limit?: number
    search?: string
    status?: string
    protocol?: string
    sort?: string
    order?: "asc" | "desc"
  }): Promise<ProxiesResponse> {
    const searchParams = new URLSearchParams()
    if (params) {
      Object.entries(params).forEach(([key, value]) => {
        if (value !== undefined) {
          searchParams.append(key, value.toString())
        }
      })
    }
    const query = searchParams.toString()
    return this.request<ProxiesResponse>(
      `/api/v1/proxies${query ? `?${query}` : ""}`
    )
  }

  async addProxy(proxy: AddProxyRequest): Promise<Proxy> {
    return this.request<Proxy>("/api/v1/proxies", {
      method: "POST",
      body: JSON.stringify(proxy),
    })
  }

  async updateProxy(id: number, proxy: UpdateProxyRequest): Promise<Proxy> {
    return this.request<Proxy>(`/api/v1/proxies/${id}`, {
      method: "PUT",
      body: JSON.stringify(proxy),
    })
  }

  async deleteProxy(id: number): Promise<void> {
    return this.request<void>(`/api/v1/proxies/${id}`, {
      method: "DELETE",
    })
  }

  async bulkAddProxies(request: BulkProxyRequest): Promise<{
    created: number
    failed: number
    results: Array<{ address: string; status: string; id?: string }>
  }> {
    return this.request("/api/v1/proxies/bulk", {
      method: "POST",
      body: JSON.stringify(request),
    })
  }

  async bulkDeleteProxies(request: BulkDeleteRequest): Promise<{
    deleted: number
    message: string
  }> {
    return this.request("/api/v1/proxies/bulk-delete", {
      method: "POST",
      body: JSON.stringify(request),
    })
  }

  async testProxy(id: number): Promise<ProxyTestResult> {
    return this.request<ProxyTestResult>(`/api/v1/proxies/${id}/test`, {
      method: "POST",
    })
  }

  async exportProxies(format: "txt" | "json" | "csv" = "txt", status?: string): Promise<Blob> {
    const params = new URLSearchParams({ format })
    if (status) params.append("status", status)

    const response = await fetch(
      `${this.baseUrl}/api/v1/proxies/export?${params.toString()}`,
      {
        headers: this.getHeaders(),
      }
    )

    if (!response.ok) {
      throw new Error("Export failed")
    }

    return response.blob()
  }

  async reloadProxies(): Promise<{ status: string; message: string }> {
    return this.request("/api/v1/proxies/reload", {
      method: "POST",
    })
  }

  // Logs
  async getLogs(params?: {
    page?: number
    limit?: number
    level?: string
    search?: string
    source?: string
    start_time?: string
    end_time?: string
  }): Promise<LogsResponse> {
    const searchParams = new URLSearchParams()
    if (params) {
      Object.entries(params).forEach(([key, value]) => {
        if (value !== undefined) {
          searchParams.append(key, value.toString())
        }
      })
    }
    const query = searchParams.toString()
    return this.request<LogsResponse>(`/api/v1/logs${query ? `?${query}` : ""}`)
  }

  async exportLogs(format: "txt" | "json" = "txt", params?: {
    level?: string
    source?: string
    start_time?: string
    end_time?: string
  }): Promise<Blob> {
    const searchParams = new URLSearchParams({ format })
    if (params) {
      Object.entries(params).forEach(([key, value]) => {
        if (value !== undefined) {
          searchParams.append(key, value.toString())
        }
      })
    }

    const response = await fetch(
      `${this.baseUrl}/api/v1/logs/export?${searchParams.toString()}`,
      {
        headers: this.getHeaders(),
      }
    )

    if (!response.ok) {
      throw new Error("Export failed")
    }

    return response.blob()
  }

  // System Metrics
  async getSystemMetrics(): Promise<SystemMetrics> {
    return this.request<SystemMetrics>("/api/v1/metrics/system")
  }

  // Settings
  async getSettings(): Promise<Settings> {
    return this.request<Settings>("/api/v1/settings")
  }

  async updateSettings(settings: Partial<Settings>): Promise<{
    message: string
    config: Settings
  }> {
    return this.request("/api/v1/settings", {
      method: "PUT",
      body: JSON.stringify(settings),
    })
  }

  async resetSettings(): Promise<{
    message: string
    config: Settings
  }> {
    return this.request("/api/v1/settings/reset", {
      method: "POST",
    })
  }

  // ── Proxy Sources ─────────────────────────────────────────────────────────
  async getSources(): Promise<{ sources: ProxySource[] }> {
    return this.request("/api/v1/sources")
  }

  async createSource(req: CreateSourceRequest): Promise<ProxySource> {
    return this.request("/api/v1/sources", { method: "POST", body: JSON.stringify(req) })
  }

  async updateSource(id: number, req: UpdateSourceRequest): Promise<ProxySource> {
    return this.request(`/api/v1/sources/${id}`, { method: "PUT", body: JSON.stringify(req) })
  }

  async deleteSource(id: number): Promise<void> {
    return this.request(`/api/v1/sources/${id}`, { method: "DELETE" })
  }

  async fetchSourceNow(id: number): Promise<{ source: ProxySource; imported: number }> {
    return this.request(`/api/v1/sources/${id}/fetch`, { method: "POST" })
  }

  async enrichGeo(): Promise<{ enriched: number }> {
    return this.request("/api/v1/sources/enrich-geo", { method: "POST" })
  }

  // ── Proxy Pools ───────────────────────────────────────────────────────────
  async getPools(): Promise<{ pools: ProxyPool[] }> {
    return this.request("/api/v1/pools")
  }

  async getPool(id: number): Promise<ProxyPool> {
    return this.request(`/api/v1/pools/${id}`)
  }

  async createPool(req: CreatePoolRequest): Promise<ProxyPool> {
    return this.request("/api/v1/pools", { method: "POST", body: JSON.stringify(req) })
  }

  async updatePool(id: number, req: Partial<CreatePoolRequest>): Promise<ProxyPool> {
    return this.request(`/api/v1/pools/${id}`, { method: "PUT", body: JSON.stringify(req) })
  }

  async deletePool(id: number): Promise<void> {
    return this.request(`/api/v1/pools/${id}`, { method: "DELETE" })
  }

  async getPoolProxies(id: number): Promise<{ proxies: PoolProxy[] }> {
    return this.request(`/api/v1/pools/${id}/proxies`)
  }

  async addPoolProxies(id: number, proxyIds: number[]): Promise<{ added: number }> {
    return this.request(`/api/v1/pools/${id}/proxies`, {
      method: "POST",
      body: JSON.stringify({ proxy_ids: proxyIds }),
    })
  }

  async removePoolProxies(id: number, proxyIds: number[]): Promise<{ removed: number }> {
    return this.request(`/api/v1/pools/${id}/proxies`, {
      method: "DELETE",
      body: JSON.stringify({ proxy_ids: proxyIds }),
    })
  }

  async syncPool(id: number): Promise<{ synced: number }> {
    return this.request(`/api/v1/pools/${id}/sync`, { method: "POST" })
  }

  async healthCheckPool(
    id: number,
    url?: string,
    workers?: number
  ): Promise<{ job_id: string; pool_id: number; status: string }> {
    return this.request(`/api/v1/pools/${id}/health-check`, {
      method: "POST",
      body: JSON.stringify({ url: url ?? "", workers: workers ?? 20 }),
    })
  }

  async getHealthCheckJob(poolId: number, jobId: string): Promise<HCJob> {
    return this.request(`/api/v1/pools/${poolId}/health-check/${jobId}`)
  }

  async getHealthCheckJobs(poolId: number): Promise<{ jobs: HCJob[] }> {
    return this.request(`/api/v1/pools/${poolId}/health-check/jobs`)
  }

  async getGeoSummary(): Promise<{ geo: GeoSummaryItem[] }> {
    return this.request("/api/v1/pools/geo-summary")
  }

  async getGeoByCountry(): Promise<{ geo: GeoSummaryItem[] }> {
    return this.request("/api/v1/pools/geo-countries")
  }

  async getGeoCities(countryCode: string): Promise<{ cities: GeoCityItem[] }> {
    return this.request(`/api/v1/pools/geo-cities/${countryCode}`)
  }

  // ── Proxy Users ───────────────────────────────────────────────────────────
  async getProxyUsers(): Promise<{ users: ProxyUser[] }> {
    return this.request("/api/v1/proxy-users")
  }

  async getProxyUser(id: number): Promise<ProxyUser> {
    return this.request(`/api/v1/proxy-users/${id}`)
  }

  async createProxyUser(req: CreateProxyUserRequest): Promise<ProxyUser> {
    return this.request("/api/v1/proxy-users", { method: "POST", body: JSON.stringify(req) })
  }

  async updateProxyUser(id: number, req: UpdateProxyUserRequest): Promise<ProxyUser> {
    return this.request(`/api/v1/proxy-users/${id}`, { method: "PUT", body: JSON.stringify(req) })
  }

  async deleteProxyUser(id: number): Promise<void> {
    return this.request(`/api/v1/proxy-users/${id}`, { method: "DELETE" })
  }

  // WebSocket connections
  createDashboardWebSocket(onMessage: (data: DashboardStats) => void): WebSocket {
    const wsUrl = this.baseUrl.replace(/^http/, "ws")
    const ws = new WebSocket(`${wsUrl}/ws/dashboard${this.token ? `?token=${this.token}` : ""}`)

    ws.onmessage = (event) => {
      const data = JSON.parse(event.data)
      if (data.type === "stats_update") {
        onMessage(data.data)
      }
    }

    return ws
  }

  createLogsWebSocket(
    onMessage: (log: any) => void,
    levels?: string[],
    source?: string
  ): WebSocket {
    const wsUrl = this.baseUrl.replace(/^http/, "ws")
    const ws = new WebSocket(`${wsUrl}/ws/logs${this.token ? `?token=${this.token}` : ""}`)

    ws.onopen = () => {
      if (levels && levels.length > 0 || source) {
        ws.send(JSON.stringify({
          action: "filter",
          levels: levels || [],
          source: source || ""
        }))
      }
    }

    ws.onmessage = (event) => {
      const log = JSON.parse(event.data)
      onMessage(log)
    }

    return ws
  }
}

// Export singleton instance
export const api = new ApiClient()

// Export class for custom instances
export { ApiClient }
