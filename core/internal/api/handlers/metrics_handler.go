package handlers

import (
	"encoding/json"
	"net/http"
	"runtime"
	"time"

	"github.com/alpkeskin/rota/core/pkg/logger"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"
)

// MetricsHandler handles system metrics requests
type MetricsHandler struct {
	logger *logger.Logger
}

// NewMetricsHandler creates a new metrics handler
func NewMetricsHandler(log *logger.Logger) *MetricsHandler {
	return &MetricsHandler{
		logger: log,
	}
}

// SystemMetrics represents system resource metrics
type SystemMetrics struct {
	Memory  MemoryMetrics  `json:"memory"`
	CPU     CPUMetrics     `json:"cpu"`
	Disk    DiskMetrics    `json:"disk"`
	Runtime RuntimeMetrics `json:"runtime"`
}

// MemoryMetrics represents memory usage metrics
type MemoryMetrics struct {
	Total      uint64  `json:"total"`
	Used       uint64  `json:"used"`
	Available  uint64  `json:"available"`
	Percentage float64 `json:"percentage"`
}

// CPUMetrics represents CPU usage metrics
type CPUMetrics struct {
	Percentage float64 `json:"percentage"`
	Cores      int     `json:"cores"`
}

// DiskMetrics represents disk usage metrics
type DiskMetrics struct {
	Total      uint64  `json:"total"`
	Used       uint64  `json:"used"`
	Free       uint64  `json:"free"`
	Percentage float64 `json:"percentage"`
}

// RuntimeMetrics represents Go runtime metrics
type RuntimeMetrics struct {
	Goroutines   int    `json:"goroutines"`
	Threads      int    `json:"threads"`
	GCPauseCount uint32 `json:"gc_pause_count"`
	MemAlloc     uint64 `json:"mem_alloc"`
	MemSys       uint64 `json:"mem_sys"`
}

// GetSystemMetrics retrieves current system metrics
//	@Summary		System metrics
//	@Description	Get current system resource metrics (CPU, memory, disk, runtime)
//	@Tags			metrics
//	@Produce		json
//	@Success		200	{object}	SystemMetrics	"System metrics"
//	@Router			/metrics/system [get]
func (h *MetricsHandler) GetSystemMetrics(w http.ResponseWriter, r *http.Request) {
	metrics := h.collectSystemMetrics()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(metrics)
}

// collectSystemMetrics collects all system metrics
func (h *MetricsHandler) collectSystemMetrics() *SystemMetrics {
	metrics := &SystemMetrics{}

	// Memory metrics
	vmStat, err := mem.VirtualMemory()
	if err != nil {
		// If memory stats fail, log warning and use zeros
		h.logger.Warn("failed to get memory stats", "error", err)
		metrics.Memory = MemoryMetrics{
			Total:      0,
			Used:       0,
			Available:  0,
			Percentage: 0,
		}
	} else {
		metrics.Memory = MemoryMetrics{
			Total:      vmStat.Total,
			Used:       vmStat.Used,
			Available:  vmStat.Available,
			Percentage: vmStat.UsedPercent,
		}
	}

	// CPU metrics
	// Use 100ms interval for more reliable CPU percentage measurement
	cpuPercentages, err := cpu.Percent(100*time.Millisecond, false)
	if err != nil {
		// If CPU percentage fails, just set to 0 and log warning
		h.logger.Warn("failed to get CPU percentage", "error", err)
		cpuPercentages = []float64{0.0}
	}
	cpuPercent := 0.0
	if len(cpuPercentages) > 0 {
		cpuPercent = cpuPercentages[0]
	}
	cpuCount, err := cpu.Counts(true)
	if err != nil {
		h.logger.Warn("failed to get CPU count", "error", err)
		cpuCount = runtime.NumCPU() // Fallback to runtime package
	}
	metrics.CPU = CPUMetrics{
		Percentage: cpuPercent,
		Cores:      cpuCount,
	}

	// Disk metrics (root partition)
	diskStat, err := disk.Usage("/")
	if err != nil {
		// If disk stats fail, log warning and use zeros
		h.logger.Warn("failed to get disk usage", "error", err)
		metrics.Disk = DiskMetrics{
			Total:      0,
			Used:       0,
			Free:       0,
			Percentage: 0,
		}
	} else {
		metrics.Disk = DiskMetrics{
			Total:      diskStat.Total,
			Used:       diskStat.Used,
			Free:       diskStat.Free,
			Percentage: diskStat.UsedPercent,
		}
	}

	// Runtime metrics
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	metrics.Runtime = RuntimeMetrics{
		Goroutines:   runtime.NumGoroutine(),
		Threads:      runtime.GOMAXPROCS(0),
		GCPauseCount: m.NumGC,
		MemAlloc:     m.Alloc,
		MemSys:       m.Sys,
	}

	return metrics
}
