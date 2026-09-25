package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/alpkeskin/rota/core/internal/database"
	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/internal/version"
	"github.com/alpkeskin/rota/core/pkg/logger"
)

var startTime = time.Now()

// HealthHandler handles health and status endpoints
type HealthHandler struct {
	db        *database.DB
	proxyRepo *repository.ProxyRepository
	logger    *logger.Logger
	// draining is set on shutdown: readiness fails so load balancers stop
	// sending new clients while in-flight work finishes.
	draining atomic.Bool
}

// SetDraining makes Readyz report not ready from now on.
func (h *HealthHandler) SetDraining() { h.draining.Store(true) }

// NewHealthHandler creates a new HealthHandler
func NewHealthHandler(db *database.DB, proxyRepo *repository.ProxyRepository, log *logger.Logger) *HealthHandler {
	return &HealthHandler{
		db:        db,
		proxyRepo: proxyRepo,
		logger:    log,
	}
}

// Health handles basic health check
//
//	@Summary		Health check
//	@Description	Check if the API server is running and healthy
//	@Tags			health
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}	"Health status information"
//	@Router			/health [get]
func (h *HealthHandler) Health(w http.ResponseWriter, r *http.Request) {
	response := map[string]interface{}{
		"status":  "healthy",
		"version": version.Version,
		"uptime":  int(time.Since(startTime).Seconds()),
	}

	h.jsonResponse(w, http.StatusOK, response)
}

// Livez reports that the process is up and serving HTTP. It never touches
// dependencies, so an orchestrator won't restart the process because the
// database is briefly unavailable.
//
//	@Summary		Liveness probe
//	@Tags			health
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}
//	@Router			/livez [get]
func (h *HealthHandler) Livez(w http.ResponseWriter, r *http.Request) {
	h.jsonResponse(w, http.StatusOK, map[string]interface{}{"status": "ok"})
}

// Readyz reports whether the instance can serve traffic: the database must
// answer a ping within two seconds. An empty proxy inventory is still ready —
// a fresh install must be reachable so proxies can be added.
//
//	@Summary		Readiness probe
//	@Tags			health
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}
//	@Failure		503	{object}	map[string]interface{}
//	@Router			/readyz [get]
func (h *HealthHandler) Readyz(w http.ResponseWriter, r *http.Request) {
	if h.draining.Load() {
		h.jsonResponse(w, http.StatusServiceUnavailable, map[string]interface{}{"status": "draining"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := h.db.Ping(ctx); err != nil {
		h.logger.Warn("readiness check failed", "check", "database", "error", err)
		h.jsonResponse(w, http.StatusServiceUnavailable, map[string]interface{}{
			"status": "not_ready",
			"checks": map[string]string{"database": "unavailable"},
		})
		return
	}
	h.jsonResponse(w, http.StatusOK, map[string]interface{}{
		"status": "ready",
		"checks": map[string]string{"database": "ok"},
	})
}

// Status handles detailed status check
//
//	@Summary		System status
//	@Description	Get detailed system status including proxy and request statistics
//	@Tags			health
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}	"Detailed status information"
//	@Failure		500	{object}	models.ErrorResponse
//	@Router			/status [get]
func (h *HealthHandler) Status(w http.ResponseWriter, r *http.Request) {
	// Get proxy stats
	proxyStats, err := h.proxyRepo.GetStats(r.Context())
	if err != nil {
		h.logger.Error("failed to get proxy stats", "error", err)
		h.errorResponse(w, http.StatusInternalServerError, "Failed to get proxy stats")
		return
	}

	// TODO: Get request stats from proxy_requests table
	// For now, using dummy data
	requestStats := map[string]interface{}{
		"total":       proxyStats["total_requests"],
		"last_minute": 0,
		"last_hour":   0,
	}

	// TODO: Get actual system stats
	systemStats := map[string]interface{}{
		"cpu_usage":    0.0,
		"memory_usage": 0,
		"memory_total": 0,
	}

	response := map[string]interface{}{
		"version": version.Version,
		"uptime":  int(time.Since(startTime).Seconds()),
		"proxies": map[string]interface{}{
			"total":  proxyStats["total"],
			"active": proxyStats["active"],
			"failed": proxyStats["failed"],
			"idle":   proxyStats["idle"],
		},
		"requests": requestStats,
		"system":   systemStats,
	}

	h.jsonResponse(w, http.StatusOK, response)
}

// DatabaseHealth handles database health check
//
//	@Summary		Database health
//	@Description	Check database connection health
//	@Tags			health
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}	"Database health information"
//	@Failure		503	{object}	models.ErrorResponse
//	@Router			/database/health [get]
func (h *HealthHandler) DatabaseHealth(w http.ResponseWriter, r *http.Request) {
	health, err := h.db.Health(r.Context())
	if err != nil {
		h.logger.Error("database health check failed", "error", err)
		h.errorResponse(w, http.StatusServiceUnavailable, "database health check failed")
		return
	}

	h.jsonResponse(w, http.StatusOK, health)
}

// DatabaseStats handles database statistics
//
//	@Summary		Database statistics
//	@Description	Get database connection pool statistics
//	@Tags			health
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}	"Database pool statistics"
//	@Router			/database/stats [get]
func (h *HealthHandler) DatabaseStats(w http.ResponseWriter, r *http.Request) {
	stats := h.db.Stats()

	response := map[string]interface{}{
		"total_conns":            stats.TotalConns(),
		"acquired_conns":         stats.AcquiredConns(),
		"idle_conns":             stats.IdleConns(),
		"max_conns":              stats.MaxConns(),
		"acquire_count":          stats.AcquireCount(),
		"acquire_duration":       stats.AcquireDuration().String(),
		"empty_acquire_count":    stats.EmptyAcquireCount(),
		"canceled_acquire_count": stats.CanceledAcquireCount(),
	}

	h.jsonResponse(w, http.StatusOK, response)
}

// jsonResponse sends a JSON response
func (h *HealthHandler) jsonResponse(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(data)
}

// errorResponse sends an error JSON response
func (h *HealthHandler) errorResponse(w http.ResponseWriter, statusCode int, message string) {
	response := models.ErrorResponse{
		Error: message,
	}
	h.jsonResponse(w, statusCode, response)
}
