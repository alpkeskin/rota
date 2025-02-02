package api

import "net/http"

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

type metrics struct {
	Timestamp string  `json:"timestamp"`
	Status    string  `json:"status"`
	Uptime    float64 `json:"uptime"`

	// Memory metrics
	TotalMemory uint64  `json:"total_memory_mb"`
	UsedMemory  uint64  `json:"used_memory_mb"`
	MemoryUsage float64 `json:"memory_usage_percent"`

	// CPU metrics
	CPUUsage float64 `json:"cpu_usage_percent"`

	// Disk metrics
	DiskTotal uint64  `json:"disk_total_gb"`
	DiskUsed  uint64  `json:"disk_used_gb"`
	DiskUsage float64 `json:"disk_usage_percent"`

	// Go runtime metrics
	GoRoutines  int    `json:"goroutines"`
	ThreadCount int    `json:"threads"`
	GCPauses    uint32 `json:"gc_pauses"`
}

type proxyResponse struct {
	Scheme              string `json:"scheme"`
	Host                string `json:"host"`
	LatestUsageStatus   string `json:"latest_usage_status"`
	LatestUsageAt       string `json:"latest_usage_at"`
	LatestUsageDuration string `json:"latest_usage_duration"`
	AvgUsageDuration    string `json:"avg_usage_duration"`
	UsageCount          int    `json:"usage_count"`
}

const (
	msgApiServerStarted         = "API server started"
	msgCertRequested            = "cert requested"
	msgFailedToCreateCert       = "failed to create cert"
	msgFailedToWriteCert        = "failed to write cert"
	msgMethodNotAllowed         = "method not allowed"
	msgFailedToCollectMetrics   = "failed to collect metrics"
	msgFailedToWriteMetrics     = "failed to write metrics"
	msgFailedToWriteHealthcheck = "failed to write healthcheck"
	msgFailedToReadProxies      = "failed to read proxies"
	msgFailedToWriteProxies     = "failed to write proxies"
	msgHealthcheckRequested     = "healthcheck requested"
	msgProxiesRequested         = "proxies requested"
	msgMetricsRequested         = "metrics requested"
	msgHistoryRequested         = "history requested"
	msgFailedToWriteHistory     = "failed to write history"
)
