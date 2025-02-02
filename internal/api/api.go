package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"
	"sort"
	"time"

	"github.com/alpkeskin/rota/internal/config"
	"github.com/alpkeskin/rota/internal/proxy"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
)

type Api struct {
	cfg         *config.Config
	proxyServer *proxy.ProxyServer
	startTime   time.Time
}

func NewApi(cfg *config.Config, proxyServer *proxy.ProxyServer) *Api {
	return &Api{cfg: cfg, proxyServer: proxyServer, startTime: time.Now()}
}

func (a *Api) Serve() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", a.handleMetrics)
	mux.HandleFunc("/healthz", a.handleHealthcheck)
	mux.HandleFunc("/proxies", a.handleProxies)
	mux.HandleFunc("/history", a.handleHistory)
	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", a.cfg.Api.Port),
		Handler: mux,
	}

	slog.Info(msgApiServerStarted, "port", a.cfg.Api.Port)

	return server.ListenAndServe()
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

func (a *Api) handleHealthcheck(w http.ResponseWriter, r *http.Request) {
	rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
	w = rw

	defer func() {
		slog.Info(msgHealthcheckRequested,
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

	duration := time.Since(a.startTime)
	uptime := fmt.Sprintf("%d days %d hours %d minutes %d seconds",
		int(duration.Hours())/24,
		int(duration.Hours())%24,
		int(duration.Minutes())%60,
		int(duration.Seconds())%60,
	)
	response := map[string]any{
		"status":    "healthy",
		"timestamp": time.Now().Format(time.RFC3339),
		"uptime":    uptime,
		"coffee":    "☕",
	}

	w.Header().Set("Content-Type", "application/json")
	err := json.NewEncoder(w).Encode(response)
	if err != nil {
		slog.Error(msgFailedToWriteHealthcheck, "error", err)
		http.Error(w, msgFailedToWriteHealthcheck, http.StatusInternalServerError)
		return
	}
}

func (a *Api) handleProxies(w http.ResponseWriter, r *http.Request) {
	rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
	w = rw

	defer func() {
		slog.Info(msgProxiesRequested,
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

	responses := make([]proxyResponse, len(a.proxyServer.Proxies))
	for i, p := range a.proxyServer.Proxies {
		responses[i] = proxyResponse{
			Scheme:              p.Scheme,
			Host:                p.Host,
			LatestUsageStatus:   p.LatestUsageStatus,
			LatestUsageAt:       p.LatestUsageAt,
			LatestUsageDuration: p.LatestUsageDuration,
			AvgUsageDuration:    p.AvgUsageDuration,
			UsageCount:          p.UsageCount,
		}
	}

	jsonProxies, err := json.Marshal(responses)
	if err != nil {
		slog.Error(msgFailedToWriteProxies, "error", err)
		http.Error(w, msgFailedToWriteProxies, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(jsonProxies)
	if err != nil {
		slog.Error(msgFailedToWriteProxies, "error", err)
		http.Error(w, msgFailedToWriteProxies, http.StatusInternalServerError)
		return
	}
}

func (a *Api) handleHistory(w http.ResponseWriter, r *http.Request) {
	rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
	w = rw

	defer func() {
		slog.Info(msgHistoryRequested,
			"status", rw.statusCode,
			"method", r.Method,
			"url", r.URL.String(),
			"ip", r.RemoteAddr,
		)
	}()

	history := a.proxyServer.ProxyHistory
	sort.Slice(history, func(i, j int) bool {
		return history[i].UsedAt > history[j].UsedAt
	})

	jsonHistory, err := json.Marshal(history)
	if err != nil {
		slog.Error(msgFailedToWriteHistory, "error", err)
		http.Error(w, msgFailedToWriteHistory, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(jsonHistory)
	if err != nil {
		slog.Error(msgFailedToWriteHistory, "error", err)
		http.Error(w, msgFailedToWriteHistory, http.StatusInternalServerError)
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
