package handlers

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/alpkeskin/rota/core/internal/models"
	"github.com/alpkeskin/rota/core/internal/repository"
	"github.com/alpkeskin/rota/core/pkg/logger"
)

// LogsHandler handles system logs endpoints
type LogsHandler struct {
	logRepo *repository.LogRepository
	logger  *logger.Logger
}

// NewLogsHandler creates a new LogsHandler
func NewLogsHandler(logRepo *repository.LogRepository, log *logger.Logger) *LogsHandler {
	return &LogsHandler{
		logRepo: logRepo,
		logger:  log,
	}
}

// List handles log listing with pagination and filters
//	@Summary		List logs
//	@Description	Get paginated list of system logs with optional filters
//	@Tags			logs
//	@Produce		json
//	@Param			page		query		int						false	"Page number"			default(1)
//	@Param			limit		query		int						false	"Items per page"		default(100)
//	@Param			level		query		string					false	"Filter by log level"
//	@Param			search		query		string					false	"Search term"
//	@Param			source		query		string					false	"Filter by source"
//	@Param			start_time	query		string					false	"Start time (RFC3339)"
//	@Param			end_time	query		string					false	"End time (RFC3339)"
//	@Success		200			{object}	models.LogListResponse	"List of logs"
//	@Failure		500			{object}	models.ErrorResponse
//	@Router			/logs [get]
func (h *LogsHandler) List(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit < 1 || limit > 1000 {
		limit = 100
	}

	level := r.URL.Query().Get("level")
	search := r.URL.Query().Get("search")
	source := r.URL.Query().Get("source")

	var startTime, endTime *time.Time
	if startTimeStr := r.URL.Query().Get("start_time"); startTimeStr != "" {
		if t, err := time.Parse(time.RFC3339, startTimeStr); err == nil {
			startTime = &t
		}
	}
	if endTimeStr := r.URL.Query().Get("end_time"); endTimeStr != "" {
		if t, err := time.Parse(time.RFC3339, endTimeStr); err == nil {
			endTime = &t
		}
	}

	// Get logs
	logs, total, err := h.logRepo.List(r.Context(), page, limit, level, search, source, startTime, endTime)
	if err != nil {
		h.logger.Error("failed to list logs", "error", err)
		h.errorResponse(w, http.StatusInternalServerError, "Failed to list logs")
		return
	}

	// Calculate pagination
	totalPages := int(math.Ceil(float64(total) / float64(limit)))

	response := models.LogListResponse{
		Logs: logs,
		Pagination: models.PaginationMeta{
			Page:       page,
			Limit:      limit,
			Total:      total,
			TotalPages: totalPages,
		},
	}

	h.jsonResponse(w, http.StatusOK, response)
}

// Export handles log export
//	@Summary		Export logs
//	@Description	Export system logs in various formats (txt, json)
//	@Tags			logs
//	@Produce		plain
//	@Produce		json
//	@Param			format		query	string	false	"Export format (txt/json)"	default(txt)
//	@Param			level		query	string	false	"Filter by log level"
//	@Param			source		query	string	false	"Filter by source"
//	@Param			start_time	query	string	false	"Start time (RFC3339)"
//	@Param			end_time	query	string	false	"End time (RFC3339)"
//	@Success		200			{file}	file	"Exported file"
//	@Failure		400			{object}	models.ErrorResponse
//	@Failure		500			{object}	models.ErrorResponse
//	@Router			/logs/export [get]
func (h *LogsHandler) Export(w http.ResponseWriter, r *http.Request) {
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "txt"
	}

	level := r.URL.Query().Get("level")
	source := r.URL.Query().Get("source")

	var startTime, endTime *time.Time
	if startTimeStr := r.URL.Query().Get("start_time"); startTimeStr != "" {
		if t, err := time.Parse(time.RFC3339, startTimeStr); err == nil {
			startTime = &t
		}
	}
	if endTimeStr := r.URL.Query().Get("end_time"); endTimeStr != "" {
		if t, err := time.Parse(time.RFC3339, endTimeStr); err == nil {
			endTime = &t
		}
	}

	// Get all logs matching filters
	logs, _, err := h.logRepo.List(r.Context(), 1, 100000, level, "", source, startTime, endTime)
	if err != nil {
		h.logger.Error("failed to get logs for export", "error", err)
		h.errorResponse(w, http.StatusInternalServerError, "Failed to export logs")
		return
	}

	switch format {
	case "txt":
		h.exportTxt(w, logs)
	case "json":
		h.exportJSON(w, logs)
	default:
		h.errorResponse(w, http.StatusBadRequest, "Invalid format")
	}
}

func (h *LogsHandler) exportTxt(w http.ResponseWriter, logs []models.Log) {
	filename := fmt.Sprintf("logs_%s.txt", time.Now().Format("2006-01-02"))
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))

	for _, l := range logs {
		timestamp := l.Timestamp.Format(time.RFC3339)
		line := fmt.Sprintf("[%s] [%s] %s", timestamp, l.Level, l.Message)
		if l.Details != nil && *l.Details != "" {
			line += fmt.Sprintf(" - %s", *l.Details)
		}
		fmt.Fprintln(w, line)
	}
}

func (h *LogsHandler) exportJSON(w http.ResponseWriter, logs []models.Log) {
	filename := fmt.Sprintf("logs_%s.json", time.Now().Format("2006-01-02"))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))
	json.NewEncoder(w).Encode(logs)
}

// jsonResponse sends a JSON response
func (h *LogsHandler) jsonResponse(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(data)
}

// errorResponse sends an error JSON response
func (h *LogsHandler) errorResponse(w http.ResponseWriter, statusCode int, message string) {
	response := models.ErrorResponse{
		Error: message,
	}
	h.jsonResponse(w, statusCode, response)
}
