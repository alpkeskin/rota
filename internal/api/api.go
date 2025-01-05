package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"
	"time"

	"github.com/alpkeskin/rota/internal/config"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
)

const (
	msgApiServerStarted       = "API server started"
	msgCertRequested          = "cert requested"
	msgFailedToCreateCert     = "failed to create cert"
	msgFailedToWriteCert      = "failed to write cert"
	msgMetricsRequested       = "metrics requested"
	msgMethodNotAllowed       = "method not allowed"
	msgFailedToCollectMetrics = "failed to collect metrics"
	msgFailedToWriteMetrics   = "failed to write metrics"
)

type Api struct {
	cfg *config.Config
}

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

func NewApi(cfg *config.Config) *Api {
	return &Api{cfg: cfg}
}

func (a *Api) Serve() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", a.handleMetrics)
	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", a.cfg.Api.Port),
		Handler: mux,
	}

	slog.Info(msgApiServerStarted, "port", a.cfg.Api.Port)

	return server.ListenAndServe()
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (a *Api) handleMetrics(w http.ResponseWriter, r *http.Request) {
	rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
	w = rw

	defer func() {
		slog.Info(msgMetricsRequested,
			"status", rw.statusCode,
			"method", r.Method,
			"url", r.URL.String(),
			"ip", r.RemoteAddr,
		)
	}()

	if r.Method != http.MethodGet {
		http.Error(w, msgMethodNotAllowed, http.StatusMethodNotAllowed)
		return
	}

	metrics, err := collectMetrics()
	if err != nil {
		slog.Error(msgFailedToCollectMetrics, "error", err)
		http.Error(w, msgFailedToCollectMetrics, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	err = json.NewEncoder(w).Encode(metrics)
	if err != nil {
		slog.Error(msgFailedToWriteMetrics, "error", err)
		http.Error(w, msgFailedToWriteMetrics, http.StatusInternalServerError)
		return
	}
}

func collectMetrics() (*metrics, error) {
	metrics := &metrics{
		Timestamp: time.Now().Format(time.RFC3339),
		Status:    "healthy",
	}

	if vmStat, err := mem.VirtualMemory(); err == nil {
		metrics.TotalMemory = vmStat.Total / 1024 / 1024
		metrics.UsedMemory = vmStat.Used / 1024 / 1024
		metrics.MemoryUsage = vmStat.UsedPercent
	}

	if cpuPercent, err := cpu.Percent(time.Second, false); err == nil && len(cpuPercent) > 0 {
		metrics.CPUUsage = cpuPercent[0]
	}

	if diskStat, err := disk.Usage("/"); err == nil {
		metrics.DiskTotal = diskStat.Total / 1024 / 1024 / 1024
		metrics.DiskUsed = diskStat.Used / 1024 / 1024 / 1024
		metrics.DiskUsage = diskStat.UsedPercent
	}

	metrics.GoRoutines = runtime.NumGoroutine()
	metrics.ThreadCount = runtime.GOMAXPROCS(0)
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	metrics.GCPauses = memStats.NumGC

	return metrics, nil
}
