package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/pkg/logger"
)

// DashboardHandler handles dashboard endpoints
type DashboardHandler struct {
	dashboardRepo *repository.DashboardRepository
	proxyRepo     *repository.ProxyRepository
	logger        *logger.Logger
}

// NewDashboardHandler creates a new DashboardHandler
func NewDashboardHandler(dashboardRepo *repository.DashboardRepository, proxyRepo *repository.ProxyRepository, log *logger.Logger) *DashboardHandler {
	return &DashboardHandler{
		dashboardRepo: dashboardRepo,
		proxyRepo:     proxyRepo,
		logger:        log,
	}
}

// GetStats handles dashboard statistics requests
//	@Summary		Dashboard statistics
//	@Description	Get dashboard statistics including proxy and request metrics
//	@Tags			dashboard
//	@Produce		json
//	@Success		200	{object}	models.DashboardStats	"Dashboard statistics"
//	@Failure		500	{object}	models.ErrorResponse
//	@Router			/dashboard/stats [get]
func (h *DashboardHandler) GetStats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.dashboardRepo.GetStats(r.Context())
	if err != nil {
		h.logger.Error("failed to get dashboard stats", "error", err)
		h.errorResponse(w, http.StatusInternalServerError, "Failed to get dashboard stats")
		return
	}

	h.jsonResponse(w, http.StatusOK, stats)
}

// GetResponseTimeChart handles response time chart requests
//	@Summary		Response time chart
//	@Description	Get response time chart data for visualization
//	@Tags			dashboard
//	@Produce		json
//	@Param			interval	query		string							false	"Time interval (e.g., 4h, 24h)"	default(4h)
//	@Success		200			{object}	models.ResponseTimeChartData	"Chart data"
//	@Failure		500			{object}	models.ErrorResponse
//	@Router			/dashboard/charts/response-time [get]
func (h *DashboardHandler) GetResponseTimeChart(w http.ResponseWriter, r *http.Request) {
	interval := r.URL.Query().Get("interval")
	if interval == "" {
		interval = "4h"
	}

	data, err := h.dashboardRepo.GetResponseTimeChart(r.Context(), interval)
	if err != nil {
		h.logger.Error("failed to get response time chart", "error", err)
		h.errorResponse(w, http.StatusInternalServerError, "Failed to get response time chart")
		return
	}

	response := models.ResponseTimeChartData{
		Data: data,
	}

	h.jsonResponse(w, http.StatusOK, response)
}

// GetSuccessRateChart handles success rate chart requests
//	@Summary		Success rate chart
//	@Description	Get success rate chart data for visualization
//	@Tags			dashboard
//	@Produce		json
//	@Param			interval	query		string							false	"Time interval (e.g., 4h, 24h)"	default(4h)
//	@Success		200			{object}	models.SuccessRateChartData		"Chart data"
//	@Failure		500			{object}	models.ErrorResponse
//	@Router			/dashboard/charts/success-rate [get]
func (h *DashboardHandler) GetSuccessRateChart(w http.ResponseWriter, r *http.Request) {
	interval := r.URL.Query().Get("interval")
	if interval == "" {
		interval = "4h"
	}

	data, err := h.dashboardRepo.GetSuccessRateChart(r.Context(), interval)
	if err != nil {
		h.logger.Error("failed to get success rate chart", "error", err)
		h.errorResponse(w, http.StatusInternalServerError, "Failed to get success rate chart")
		return
	}

	response := models.SuccessRateChartData{
		Data: data,
	}

	h.jsonResponse(w, http.StatusOK, response)
}

// jsonResponse sends a JSON response
func (h *DashboardHandler) jsonResponse(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(data)
}

// errorResponse sends an error JSON response
func (h *DashboardHandler) errorResponse(w http.ResponseWriter, statusCode int, message string) {
	response := models.ErrorResponse{
		Error: message,
	}
	h.jsonResponse(w, statusCode, response)
}
